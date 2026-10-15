package operational

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealth(t *testing.T) {
	h := &Health{Check: func(context.Context) error { return errors.New("secret database details") }}
	handler := h.Handler(http.NotFoundHandler())
	for path, want := range map[string]int{"/healthz": 200, "/readyz": 503, "/missing": 404} {
		r := httptest.NewRecorder()
		handler.ServeHTTP(r, httptest.NewRequest("GET", path, nil))
		if r.Code != want || strings.Contains(r.Body.String(), "secret") {
			t.Fatal(path, r.Code, r.Body.String())
		}
	}
	h.Check = nil
	h.Drain()
	r := httptest.NewRecorder()
	handler.ServeHTTP(r, httptest.NewRequest("GET", "/readyz", nil))
	if r.Code != 503 {
		t.Fatal(r.Code)
	}
}
func TestListenDefaults(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:8080", "[::1]:8080", "localhost:8080"} {
		if e := ValidateListen(addr, false, false); e != nil {
			t.Fatal(e)
		}
	}
	if ValidateListen(":8080", false, false) == nil {
		t.Fatal("public HTTP accepted")
	}
	if e := ValidateListen(":8080", true, false); e != nil {
		t.Fatal(e)
	}
}
