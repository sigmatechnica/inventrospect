package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"inventrospect/internal/inventory"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func testServer(t *testing.T) (http.Handler, *inventory.Service, string) {
	t.Helper()
	s, e := inventory.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	if e = s.Bootstrap(context.Background(), "admin", "a-long-test-password"); e != nil {
		t.Fatal(e)
	}
	token, e := s.Login(context.Background(), "admin", "a-long-test-password")
	if e != nil {
		t.Fatal(e)
	}
	return New(s, false), s, token
}
func request(h http.Handler, method, path, body, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestAPIValidationPermissionsAndCRUD(t *testing.T) {
	h, s, token := testServer(t)
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{{"POST", "/api/v1/items", `{"name":""}`, 400}, {"POST", "/api/v1/items", `{"name":"x","unknown":1}`, 400}, {"POST", "/api/v1/items", `{"name":"x"} {}`, 400}, {"POST", "/api/v1/items", `null`, 400}, {"GET", "/api/v1/items?limit=-1", "", 400}, {"GET", "/api/v1/items?offset=no", "", 400}, {"GET", "/api/v1/items?archived=no", "", 400}, {"GET", "/api/v1/items/no", "", 400}, {"GET", "/api/v1/items/999", "", 404}, {"DELETE", "/api/v1/items/1", "", 405}} {
		w := request(h, tc.method, tc.path, tc.body, token)
		if w.Code != tc.status {
			t.Fatalf("%s %s: %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
		var body map[string]any
		if e := json.Unmarshal(w.Body.Bytes(), &body); e != nil || body["error"] == nil {
			t.Fatal("unstructured error", w.Body.String())
		}
	}
	w := request(h, "POST", "/api/v1/items", `{"name":"Bearing","attributes":{"ID":"20 mm"},"tags":["spare"]}`, token)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w.Header().Get("Location") != "/api/v1/items/1" {
		t.Fatal(w.Header())
	}
	w = request(h, "PUT", "/api/v1/items/1", `{"name":"6204 bearing","attributes":{"ID":"20 mm"}}`, token)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, path := range []string{"/api/v1/items?q=bearing", "/api/v1/items/1", "/api/v1/items/1/attributes", "/api/v1/items/1/stock", "/api/v1/items/1/documents", "/api/v1/categories", "/api/v1/locations", "/api/v1/users", "/api/v1/me", "/api/v1/export.csv"} {
		w = request(h, "GET", path, "", token)
		if w.Code != 200 {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	w = request(h, "PUT", "/api/v1/items/1/archive", `{}`, token)
	if w.Code != 400 {
		t.Fatal(w.Code)
	}
	w = request(h, "PUT", "/api/v1/items/1/archive", `{"archived":true}`, token)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	u, e := s.SaveUser(context.Background(), inventory.Actor{UserID: 1}, inventory.User{Username: "reader", Role: "viewer"}, "reader-password")
	if e != nil {
		t.Fatal(e)
	}
	_ = u
	viewer, e := s.Login(context.Background(), "reader", "reader-password")
	if e != nil {
		t.Fatal(e)
	}
	w = request(h, "POST", "/api/v1/items", `{"name":"denied"}`, viewer)
	if w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "GET", "/api/v1/items", "", "")
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
}
func TestCookieCSRFAndLogin(t *testing.T) {
	h, _, token := testServer(t)
	r := httptest.NewRequest("POST", "/api/v1/items", strings.NewReader(`{"name":"part"}`))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(&http.Cookie{Name: "session", Value: token})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	r = httptest.NewRequest("POST", "/api/v1/items", strings.NewReader(`{"name":"part"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-CSRF-Token", csrf(token))
	r.AddCookie(&http.Cookie{Name: "session", Value: token})
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	r = httptest.NewRequest("POST", "/api/v1/login", strings.NewReader(`{"username":"admin","password":"a-long-test-password"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://evil.example")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "POST", "/api/v1/login", `{"username":"admin","password":"a-long-test-password"}`, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 2 || !cookies[1].HttpOnly || cookies[1].Name != "session" || cookies[1].SameSite != http.SameSiteStrictMode {
		t.Fatal(cookies)
	}
	w = request(h, "POST", "/api/v1/logout", "", token)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	w = request(h, "GET", "/api/v1/items", "", token)
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
}
func TestUIAndDocumentWorkflow(t *testing.T) {
	h, s, token := testServer(t)
	ctx := context.Background()
	a := inventory.Actor{UserID: 1}
	cat, e := s.SaveNode(ctx, a, "categories", inventory.Node{Name: "Mechanical"})
	if e != nil {
		t.Fatal(e)
	}
	loc, e := s.SaveNode(ctx, a, "locations", inventory.Node{Name: "Workshop"})
	if e != nil {
		t.Fatal(e)
	}
	form := url.Values{"_csrf": {csrf(token)}, "name": {"Bearing <script>alert(1)</script>"}, "category_id": {fmt.Sprint(cat.ID)}, "attributes": {`{"ID":"20 mm"}`}, "tags": {"bearing, spare"}}
	r := httptest.NewRequest("POST", "/items/new", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(&http.Cookie{Name: "session", Value: token})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 303 || w.Header().Get("Location") != "/items/1" {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, e = s.AdjustStock(ctx, a, 1, inventory.Adjustment{LocationID: loc.ID, Quantity: 3, Reason: "initial"}); e != nil {
		t.Fatal(e)
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	mw.WriteField("title", "Drawing")
	mw.WriteField("type", "drawing")
	fw, e := mw.CreateFormFile("file", "drawing.pdf")
	if e != nil {
		t.Fatal(e)
	}
	io.WriteString(fw, "technical drawing")
	mw.Close()
	r = httptest.NewRequest("POST", "/api/v1/items/1/documents", &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	r.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "GET", "/api/v1/documents/1/content", "", token)
	if w.Code != 200 || w.Body.String() != "technical drawing" || !strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment;") {
		t.Fatal(w.Code, w.Header(), w.Body.String())
	}
	for _, path := range []string{"/items", "/items?q=bearing", "/items/new", "/items/1", "/items/1/edit", "/categories", "/locations", "/users", "/login"} {
		w = request(h, "GET", path, "", token)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "</html>") {
			t.Fatal(path, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "<script>alert") {
			t.Fatal("unescaped item name")
		}
	}
	// Viewer pages do not advertise writes; services separately enforce permissions.
	if _, e = s.SaveUser(ctx, a, inventory.User{Username: "viewer", Role: "viewer"}, "viewer-password"); e != nil {
		t.Fatal(e)
	}
	viewer, e := s.Login(ctx, "viewer", "viewer-password")
	if e != nil {
		t.Fatal(e)
	}
	w = request(h, "GET", "/items/1", "", viewer)
	if w.Code != 200 || strings.Contains(w.Body.String(), "Record adjustment") || strings.Contains(w.Body.String(), "Upload document") {
		t.Fatal(w.Code, w.Body.String())
	}
}
