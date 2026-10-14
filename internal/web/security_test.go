package web

import (
	"context"
	"fmt"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"inventrospect/internal/inventory"
)

func TestBrowserAgentRevisionConflict(t *testing.T) {
	h, s, session := testServer(t)
	ctx := context.Background()
	admin := inventory.Actor{UserID: 1}
	i, e := s.SaveItem(ctx, admin, inventory.Item{Name: "Part", Notes: "Original"})
	if e != nil {
		t.Fatal(e)
	}
	form := request(h, "GET", "/items/1/edit", "", session)
	if form.Code != 200 || !strings.Contains(form.Body.String(), `name="expected_revision" value="1"`) {
		t.Fatal(form.Code, form.Body.String())
	}
	token, e := s.CreateToken(ctx, admin, inventory.TokenInput{Name: "Editor", UserID: 1, Scopes: []string{"inventory:write"}})
	if e != nil {
		t.Fatal(e)
	}
	w := request(h, "PATCH", "/api/v1/items/1", `{"changes":{"notes":"Agent update"},"expected_revision":1}`, token.Secret)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = formRequest(h, "/items/1", url.Values{"name": {"Human name"}, "notes": {"Unsaved human note"}, "expected_revision": {"1"}}, session)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "Unsaved human note") || !strings.Contains(w.Body.String(), "Reload the latest record") {
		t.Fatal(w.Code, w.Body.String())
	}
	stored, e := s.Item(ctx, admin, i.ID)
	if e != nil || stored.Notes != "Agent update" || stored.Name != "Part" || stored.Revision != 2 {
		t.Fatal(stored, e)
	}
	for _, method := range []string{"PUT", "PATCH"} {
		body := `{"name":"replacement"}`
		if method == "PATCH" {
			body = `{"changes":{"notes":"missing revision"}}`
		}
		w = request(h, method, "/api/v1/items/1", body, session)
		if w.Code != 400 {
			t.Fatal(method, w.Code, w.Body.String())
		}
	}
	w = formRequest(h, "/items/1", url.Values{"name": {"Human name"}, "notes": {"Merged note"}, "expected_revision": {"2"}}, session)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestIsolatedLoginLimits(t *testing.T) {
	s := &Server{}
	r := httptest.NewRequest("POST", "/login", nil)
	r.RemoteAddr = "192.0.2.1:1234"
	for j := 0; j < 10; j++ {
		if !s.allowLogin(r, "AccountA") {
			t.Fatal(j)
		}
	}
	if s.allowLogin(r, "accounta") {
		t.Fatal("account limit bypassed")
	}
	r.RemoteAddr = "192.0.2.2:5555"
	if s.allowLogin(r, "AccountA") {
		t.Fatal("IP change bypassed account limit")
	}
	if !s.allowLogin(r, "AccountB") {
		t.Fatal("unrelated account blocked")
	}
	for j := 0; j < 29; j++ {
		if !s.allowLogin(r, fmt.Sprint("user", j)) {
			t.Fatal(j)
		}
	}
	r.Header.Set("X-Forwarded-For", "192.0.2.3")
	if s.allowLogin(r, "fresh") {
		t.Fatal("forwarded header bypassed IP limit")
	}
	for j := 0; j < maxLoginEntries*2; j++ {
		r.RemoteAddr = fmt.Sprintf("198.51.%d.%d:123", j/256, j%256)
		s.allowLogin(r, fmt.Sprint(j))
	}
	if len(s.logins) > maxLoginEntries {
		t.Fatal(len(s.logins))
	}
	for k, w := range s.logins {
		w.Started = time.Now().Add(-2 * time.Minute)
		s.logins[k] = w
	}
	r.RemoteAddr = "192.0.2.1:1234"
	if !s.allowLogin(r, "AccountA") || len(s.logins) != 2 {
		t.Fatal("expiry cleanup failed", len(s.logins))
	}
}

func TestCorrectLoginDuringThrottle(t *testing.T) {
	h, _, _ := testServer(t)
	login := func(user, password, ip string) int {
		r := httptest.NewRequest("POST", "/api/v1/login", strings.NewReader(fmt.Sprintf(`{"username":%q,"password":%q}`, user, password)))
		r.Header.Set("Content-Type", "application/json")
		r.RemoteAddr = ip
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	for j := 0; j < 10; j++ {
		if code := login("other", "wrong", "192.0.2.1:1"); code != 401 {
			t.Fatal(code)
		}
	}
	if code := login("admin", "a-long-test-password", "192.0.2.2:2"); code != 200 {
		t.Fatal(code)
	}
	for j := 0; j < 9; j++ {
		login("admin", "wrong", "192.0.2.2:2")
	}
	if code := login("admin", "a-long-test-password", "192.0.2.3:3"); code != 429 {
		t.Fatal(code)
	}
}

func TestTokenLimitAdminUI(t *testing.T) {
	h, s, session := testServer(t)
	ctx := context.Background()
	admin := inventory.Actor{UserID: 1}
	token, e := s.CreateToken(ctx, admin, inventory.TokenInput{Name: "Migration", UserID: 1, Scopes: []string{"inventory:write"}})
	if e != nil {
		t.Fatal(e)
	}
	w := formRequest(h, fmt.Sprintf("/tokens/%d/limits", token.Token.ID), url.Values{"write_units_per_minute": {"2"}, "bulk_record_limit": {"2"}}, session)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "GET", "/tokens", "", session)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `name="write_units_per_minute"`) || strings.Contains(w.Body.String(), token.Secret) {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "POST", "/api/v1/bulk/items/create", `{"records":[{"item":{"name":"A"}},{"item":{"name":"B"}}]}`, token.Secret)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "POST", "/api/v1/items", `{"name":"C"}`, token.Secret)
	if w.Code != 429 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "PUT", fmt.Sprintf("/api/v1/tokens/%d/limits", token.Token.ID), `{"write_units_per_minute":3,"bulk_record_limit":2}`, session)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "POST", "/api/v1/items", `{"name":"C"}`, token.Secret)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestRESTAuthorizationMatrix(t *testing.T) {
	h, s, adminSession := testServer(t)
	ctx := context.Background()
	admin := inventory.Actor{UserID: 1}
	type identity struct {
		name, secret             string
		read, write, del, manage bool
	}
	identities := []identity{{"admin", adminSession, true, true, true, true}}
	for _, role := range []string{"editor", "viewer"} {
		u, e := s.SaveUser(ctx, admin, inventory.User{Username: role, Role: role}, "long-test-password")
		if e != nil {
			t.Fatal(e)
		}
		secret, e := s.Login(ctx, u.Username, "long-test-password")
		if e != nil {
			t.Fatal(e)
		}
		identities = append(identities, identity{role, secret, true, role == "editor", false, false})
	}
	for _, tc := range []struct {
		name             string
		scopes           []string
		read, write, del bool
	}{
		{"reader", []string{"inventory:read", "stock:read", "documents:read"}, true, false, false},
		{"writer", []string{"inventory:read", "inventory:write", "categories:write", "locations:write", "stock:write", "documents:write"}, true, true, false},
		{"deleter", []string{"inventory:delete"}, false, false, true},
	} {
		token, e := s.CreateToken(ctx, admin, inventory.TokenInput{Name: tc.name, UserID: 1, Scopes: tc.scopes})
		if e != nil {
			t.Fatal(e)
		}
		identities = append(identities, identity{tc.name, token.Secret, tc.read, tc.write, tc.del, false})
	}
	for _, who := range identities {
		t.Run(who.name, func(t *testing.T) {
			i, e := s.SaveItem(ctx, admin, inventory.Item{Name: "Test"})
			if e != nil {
				t.Fatal(e)
			}
			loc, e := s.SaveNode(ctx, admin, "locations", inventory.Node{Name: "Drawer"})
			if e != nil {
				t.Fatal(e)
			}
			cat, e := s.SaveNode(ctx, admin, "categories", inventory.Node{Name: "Parts"})
			if e != nil {
				t.Fatal(e)
			}
			for _, op := range []struct {
				method, path, body string
				allowed            bool
			}{
				{"GET", fmt.Sprintf("/api/v1/items/%d", i.ID), "", who.read},
				{"POST", "/api/v1/items/batch-get", fmt.Sprintf(`{"ids":[%d]}`, i.ID), who.read},
				{"POST", "/api/v1/items", `{"name":"Created"}`, who.write},
				{"PATCH", fmt.Sprintf("/api/v1/items/%d", i.ID), fmt.Sprintf(`{"changes":{"notes":"patched"},"expected_revision":%d}`, i.Revision), who.write},
				{"POST", fmt.Sprintf("/api/v1/items/%d/stock/adjustments", i.ID), fmt.Sprintf(`{"location_id":%d,"quantity":2}`, loc.ID), who.write},
				{"POST", "/api/v1/categories", `{"name":"New"}`, who.write},
				{"PATCH", fmt.Sprintf("/api/v1/categories/%d", cat.ID), `{"changes":{"name":"Renamed"}}`, who.write},
				{"POST", "/api/v1/locations", `{"name":"New"}`, who.write},
				{"PATCH", fmt.Sprintf("/api/v1/locations/%d", loc.ID), `{"changes":{"name":"Renamed"}}`, who.write},
				{"POST", "/api/v1/bulk/categories/create", `{"records":[{"name":"Bulk category"}]}`, who.write},
				{"POST", "/api/v1/bulk/locations/create", `{"records":[{"name":"Bulk location"}]}`, who.write},
				{"POST", "/api/v1/bulk/items/create", `{"records":[{"item":{"name":"Bulk item"}}]}`, who.write},
				{"POST", "/api/v1/bulk/stock/adjustments", fmt.Sprintf(`{"records":[{"item_id":%d,"adjustment":{"location_id":%d,"quantity":1}}]}`, i.ID, loc.ID), who.write},
				{"DELETE", fmt.Sprintf("/api/v1/items/%d?dry_run=true", i.ID), "", who.del},
				{"POST", "/api/v1/bulk/items/delete", fmt.Sprintf(`{"ids":[%d],"dry_run":true}`, i.ID), who.del},
				{"POST", "/api/v1/tokens", `{"name":"Admin managed","user_id":1,"scopes":["inventory:read"]}`, who.manage},
				{"PUT", "/api/v1/tokens/1/limits", `{"write_units_per_minute":600,"bulk_record_limit":100}`, who.manage},
			} {
				w := request(h, op.method, op.path, op.body, who.secret)
				if op.allowed {
					if w.Code >= 300 {
						t.Fatalf("%s %s: %d %s", op.method, op.path, w.Code, w.Body.String())
					}
				} else if w.Code != 403 {
					t.Fatalf("%s %s: want 403, got %d %s", op.method, op.path, w.Code, w.Body.String())
				}
			}
		})
	}
}
