package web

import (
	"context"
	"fmt"
	"inventrospect/internal/inventory"
	"strings"
	"testing"
	"time"
)

func TestRESTPartialRetryAndLimits(t *testing.T) {
	h, s, admin := testServer(t)
	a := inventory.Actor{UserID: 1}
	ctx := context.Background()
	token, e := s.CreateToken(ctx, a, inventory.TokenInput{Name: "agent", UserID: 1, Scopes: []string{"inventory:read", "inventory:write", "inventory:delete", "categories:write"}})
	if e != nil {
		t.Fatal(e)
	}
	key := fmt.Sprintf("%d:rest-create", time.Now().Unix())
	body := fmt.Sprintf(`{"name":"Part","tags":["keep"],"attributes":{"power":"3"},"client_request_id":%q}`, key)
	first := request(h, "POST", "/api/v1/items", body, token.Secret)
	retry := request(h, "POST", "/api/v1/items", body, token.Secret)
	if first.Code != 201 || retry.Code != 201 || first.Body.String() != retry.Body.String() {
		t.Fatal(first.Code, first.Body.String(), retry.Body.String())
	}
	w := request(h, "PATCH", "/api/v1/items/1", `{"changes":{"manufacturer":"TI"},"expected_revision":1}`, token.Secret)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"tags":["keep"]`) || !strings.Contains(w.Body.String(), `"revision":2`) {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "PATCH", "/api/v1/items/1", `{"changes":{"notes":"stale"},"expected_revision":1}`, token.Secret)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "revision_conflict") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "POST", "/api/v1/items", strings.Replace(body, "Part", "Different", 1), token.Secret)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "idempotency_conflict") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "PUT", "/api/v1/items/1", `{"name":"Replaced","expected_revision":2}`, admin)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"tags":[]`) {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "PUT", "/api/v1/items/1", `{"name":"Invalid","expected_revision":0}`, admin)
	if w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "POST", "/api/v1/categories", `{"name":"Root"}`, token.Secret)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "PATCH", "/api/v1/categories/1", `{"changes":{"name":"Renamed"}}`, token.Secret)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "POST", "/api/v1/items/batch-get", `{"ids":[1,999]}`, token.Secret)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "not_found") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "DELETE", "/api/v1/items/1?dry_run=true", "", token.Secret)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "would_delete") {
		t.Fatal(w.Code, w.Body.String())
	}
	if e = s.ConfigureLimits(1, 2); e != nil {
		t.Fatal(e)
	}
	w = request(h, "POST", "/api/v1/bulk/items/create", `{"records":[{"item":{"name":"A"}},{"item":{"name":"B"}}]}`, admin)
	if w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}
	// Use a fresh token with its own rate window. Reads remain usable after limit.
	fresh, e := s.CreateToken(ctx, a, inventory.TokenInput{Name: "limited", UserID: 1, Scopes: []string{"inventory:read", "inventory:write"}})
	if e != nil {
		t.Fatal(e)
	}
	for j := 0; j < 3; j++ {
		w = request(h, "PATCH", "/api/v1/items/1", fmt.Sprintf(`{"changes":{"notes":"x"},"expected_revision":%d}`, 3+j), fresh.Secret)
		want := 200
		if j == 2 {
			want = 429
		}
		if w.Code != want {
			t.Fatal(j, w.Code, w.Body.String())
		}
	}
	if w.Header().Get("Retry-After") == "" || !strings.Contains(w.Body.String(), `"rate_limited"`) {
		t.Fatal(w.Header(), w.Body.String())
	}
	w = request(h, "POST", "/api/v1/items/batch-get", `{"ids":[1]}`, fresh.Secret)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if e = s.RevokeToken(ctx, a, fresh.Token.ID); e != nil {
		t.Fatal(e)
	}
	w = request(h, "GET", "/api/v1/items", "", fresh.Secret)
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
}
