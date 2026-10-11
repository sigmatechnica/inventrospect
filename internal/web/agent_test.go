package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"inventrospect/internal/inventory"
)

func TestAdminTokenUIAndRESTScopeIsolation(t *testing.T) {
	h, s, session := testServer(t)
	ctx := context.Background()
	admin := inventory.Actor{UserID: 1}
	w := request(h, "GET", "/tokens", "", session)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Create agent token") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = formRequest(h, "/tokens", url.Values{"name": {"Reader"}, "user_id": {"1"}, "scopes": {"inventory:read"}}, session)
	if w.Code != 201 || !strings.Contains(w.Body.String(), "Copy this secret now") {
		t.Fatal(w.Code, w.Body.String())
	}
	// Extract the displayed one-time secret, then ensure it never appears on listing.
	body := w.Body.String()
	start := strings.Index(body, `value="inv_`)
	if start < 0 {
		t.Fatal("missing one-time secret")
	}
	secret := strings.Split(body[start+len(`value="`):], `"`)[0]
	w = request(h, "GET", "/tokens", "", session)
	if w.Code != 200 || strings.Contains(w.Body.String(), secret) {
		t.Fatal("secret displayed on list")
	}
	w = request(h, "GET", "/api/v1/tokens", "", session)
	if w.Code != 200 || strings.Contains(w.Body.String(), secret) || strings.Contains(w.Body.String(), "token_hash") {
		t.Fatal("secret/hash leaked")
	}
	w = request(h, "GET", "/api/v1/items", "", secret)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, tc := range []struct{ method, path, body string }{{"GET", "/api/v1/users", ""}, {"GET", "/api/v1/tokens", ""}, {"POST", "/api/v1/tokens", `{"name":"escalate","user_id":1,"scopes":["inventory:write"]}`}, {"DELETE", "/api/v1/locations/1", ""}, {"PUT", "/api/v1/settings", `{"default_location_id":null}`}, {"POST", "/api/v1/items", `{"name":"denied"}`}} {
		w = request(h, tc.method, tc.path, tc.body, secret)
		if w.Code != 403 {
			t.Fatal(tc.path, w.Code, w.Body.String())
		}
	}
	// Agent credentials never grant a browser session, including via forged cookies.
	r := httptest.NewRequest("GET", "/items", nil)
	r.AddCookie(&http.Cookie{Name: "session", Value: secret})
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
	w = formRequest(h, "/tokens/1/revoke", url.Values{}, session)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "GET", "/api/v1/items", "", secret)
	if w.Code != 401 {
		t.Fatal(w.Code, w.Body.String())
	}
	u, e := s.SaveUser(ctx, admin, inventory.User{Username: "viewer", Role: "viewer"}, "viewer-password")
	if e != nil {
		t.Fatal(e)
	}
	_ = u
	viewer, e := s.Login(ctx, "viewer", "viewer-password")
	if e != nil {
		t.Fatal(e)
	}
	w = request(h, "GET", "/tokens", "", viewer)
	if w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "POST", "/api/v1/tokens", `{"name":"API writer","user_id":1,"scopes":["inventory:write"]}`, session)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var created struct {
		Data inventory.TokenCreated `json:"data"`
	}
	if e = json.Unmarshal(w.Body.Bytes(), &created); e != nil {
		t.Fatal(e)
	}
	w = request(h, "POST", "/api/v1/bulk/items/create", `{"records":[{"item":{"name":"Created"}},{"item":{"name":""}}]}`, created.Data.Secret)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"succeeded":1`) || !strings.Contains(w.Body.String(), `"failed":1`) {
		t.Fatal(w.Code, w.Body.String())
	}
	// Strictly decode the entire batch before any record is mutated.
	w = request(h, "POST", "/api/v1/bulk/items/create", `{"records":[{"item":{"name":"Must not exist"}},{"item":{"name":"Unknown field","sql":"DROP TABLE items"}}]}`, created.Data.Secret)
	if w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}
	items, e := s.Search(ctx, admin, inventory.Search{})
	if e != nil || len(items) != 1 {
		t.Fatal(items, e)
	}
}
