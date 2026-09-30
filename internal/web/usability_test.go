package web

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"inventrospect/internal/inventory"
)

func formRequest(h http.Handler, path string, form url.Values, token string) *httptest.ResponseRecorder {
	form.Set("_csrf", csrf(token))
	r := httptest.NewRequest("POST", path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(&http.Cookie{Name: "session", Value: token})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestNewItemFormWithStockAndFiles(t *testing.T) {
	h, s, token := testServer(t)
	a := inventory.Actor{UserID: 1}
	ctx := context.Background()
	loc, e := s.SaveNode(ctx, a, "locations", inventory.Node{Name: "Drawer"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.SetDefaultLocation(ctx, a, &loc.ID); e != nil {
		t.Fatal(e)
	}
	page := request(h, "GET", "/items/new", "", token)
	for _, fragment := range []string{`enctype="multipart/form-data"`, `name="initial_quantity"`, `step="1"`, `name="datasheets" multiple`, `name="files" multiple`, `value="1" selected`} {
		if page.Code != 200 || !strings.Contains(page.Body.String(), fragment) {
			t.Fatal(fragment, page.Code, page.Body.String())
		}
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fields := map[string]string{"_csrf": csrf(token), "name": "New motor", "initial_quantity": "6", "initial_location_id": fmt.Sprint(loc.ID), "attributes": `{"voltage":"24 V"}`}
	for k, v := range fields {
		if e = mw.WriteField(k, v); e != nil {
			t.Fatal(e)
		}
	}
	for _, f := range []struct{ field, name string }{{"datasheets", "sheet.pdf"}, {"files", "manual.txt"}, {"files", "photo.jpg"}} {
		writer, e := mw.CreateFormFile(f.field, f.name)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = writer.Write([]byte(f.name)); e != nil {
			t.Fatal(e)
		}
	}
	if e = mw.Close(); e != nil {
		t.Fatal(e)
	}
	r := httptest.NewRequest("POST", "/items/new", &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	r.AddCookie(&http.Cookie{Name: "session", Value: token})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 303 || w.Header().Get("Location") != "/items/1" {
		t.Fatal(w.Code, w.Body.String())
	}
	i, e := s.Item(ctx, a, 1)
	if e != nil || i.Quantity != 6 {
		t.Fatal(i, e)
	}
	docs, e := s.Documents(ctx, a, 1)
	if e != nil || len(docs) != 3 || docs[2].Title != "sheet.pdf" || docs[2].Type != "datasheet" || docs[1].Type != "other" {
		t.Fatal(docs, e)
	}
	page = request(h, "GET", "/items/1", "", token)
	for _, fragment := range []string{`value="1" selected`, `step="1"`, `Reason (optional)`, `Title (optional)`} {
		if !strings.Contains(page.Body.String(), fragment) {
			t.Fatal(fragment, page.Body.String())
		}
	}
	if strings.Contains(page.Body.String(), `name="reason" required`) || strings.Contains(page.Body.String(), `name="title" required`) {
		t.Fatal("optional fields still required")
	}
	w = formRequest(h, "/items/1/adjust", url.Values{"location_id": {"1"}, "quantity": {"1"}}, token)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	// Server-side validation still rejects fractional quantities if HTML validation is bypassed.
	w = formRequest(h, "/items/1/adjust", url.Values{"location_id": {"1"}, "quantity": {"0.5"}}, token)
	if w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestDefaultDeletionAndThemeRoutes(t *testing.T) {
	h, s, token := testServer(t)
	a := inventory.Actor{UserID: 1}
	ctx := context.Background()
	loc, e := s.SaveNode(ctx, a, "locations", inventory.Node{Name: "Unused"})
	if e != nil {
		t.Fatal(e)
	}
	w := formRequest(h, "/locations/default", url.Values{"default_location_id": {fmt.Sprint(loc.ID)}}, token)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "GET", "/api/v1/settings", "", token)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"default_location_id":1`) {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "GET", "/locations", "", token)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `/locations/1/delete`) {
		t.Fatal(w.Code, w.Body.String())
	}
	w = formRequest(h, "/locations/1/delete", url.Values{}, token)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	settings, e := s.Settings(ctx, a)
	if e != nil || settings.DefaultLocationID != nil {
		t.Fatal(settings, e)
	}
	w = request(h, "POST", "/api/v1/categories", `{"name":"Disposable"}`, token)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "DELETE", "/api/v1/categories/1", "", token)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "DELETE", "/api/v1/categories/1", "", token)
	if w.Code != 404 {
		t.Fatal(w.Code, w.Body.String())
	}
	viewer, e := s.SaveUser(ctx, a, inventory.User{Username: "viewer", Role: "viewer"}, "viewer-password")
	if e != nil {
		t.Fatal(e)
	}
	_ = viewer
	viewerToken, e := s.Login(ctx, "viewer", "viewer-password")
	if e != nil {
		t.Fatal(e)
	}
	w = formRequest(h, "/settings", url.Values{"theme": {"dark"}}, viewerToken)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, path := range []string{"/items", "/settings", "/locations", "/categories", "/items/999"} {
		w = request(h, "GET", path, "", viewerToken)
		if !strings.Contains(w.Body.String(), `data-theme="dark"`) {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	w = request(h, "GET", "/items", "", token)
	if !strings.Contains(w.Body.String(), `data-theme="light"`) {
		t.Fatal("other user's theme changed")
	}
	w = request(h, "GET", "/api/v1/me/preferences", "", viewerToken)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"theme":"dark"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "PUT", "/api/v1/me/preferences", `{"theme":"light"}`, viewerToken)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "PUT", "/api/v1/me/preferences", `{"theme":"invalid"}`, viewerToken)
	if w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "PUT", "/api/v1/settings", `{"default_location_id":null}`, viewerToken)
	if w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "DELETE", "/api/v1/locations/999", "", viewerToken)
	if w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	// Preferences are cookie-authenticated mutations and require CSRF too.
	r := httptest.NewRequest("POST", "/settings", strings.NewReader("theme=dark"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(&http.Cookie{Name: "session", Value: viewerToken})
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestAPICreationInitialStockAndOptionalUploadTitle(t *testing.T) {
	h, s, token := testServer(t)
	a := inventory.Actor{UserID: 1}
	ctx := context.Background()
	loc, e := s.SaveNode(ctx, a, "locations", inventory.Node{Name: "Source"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.SetDefaultLocation(ctx, a, &loc.ID); e != nil {
		t.Fatal(e)
	}
	w := request(h, "POST", "/api/v1/items", `{"name":"API item","initial_stock":{"quantity":4}}`, token)
	if w.Code != 201 || !strings.Contains(w.Body.String(), `"quantity":4`) {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "POST", "/api/v1/items", `{"name":"Invalid","initial_stock":{"quantity":1.5}}`, token)
	if w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "POST", "/api/v1/items/1/stock/adjustments", `{"location_id":1,"quantity":1}`, token)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	dst, e := s.SaveNode(ctx, a, "locations", inventory.Node{Name: "Destination"})
	if e != nil {
		t.Fatal(e)
	}
	w = request(h, "POST", "/api/v1/items/1/stock/transfers", fmt.Sprintf(`{"from_location_id":1,"to_location_id":%d,"quantity":2}`, dst.ID), token)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "DELETE", "/api/v1/locations/1", "", token)
	if w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	mw.WriteField("type", "datasheet")
	fw, e := mw.CreateFormFile("file", "datasheet.pdf")
	if e != nil {
		t.Fatal(e)
	}
	fw.Write([]byte("technical data"))
	mw.Close()
	r := httptest.NewRequest("POST", "/api/v1/items/1/documents", &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	r.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 201 || !strings.Contains(w.Body.String(), `"title":"datasheet.pdf"`) {
		t.Fatal(w.Code, w.Body.String())
	}
}
