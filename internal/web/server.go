package web

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"inventrospect/internal/inventory"
)

//go:embed templates.html explorer.html style.css explorer.js
var assets embed.FS

type Server struct {
	service   *inventory.Service
	templates *template.Template
	secure    bool
	loginMu   sync.Mutex
	attempts  int
	window    time.Time
}

func New(s *inventory.Service, secure bool) http.Handler {
	funcs := template.FuncMap{"json": func(v any) string { b, _ := json.MarshalIndent(v, "", "  "); return string(b) }, "join": strings.Join, "number": func(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }, "eqid": func(p *int64, id int64) bool { return p != nil && *p == id }, "inc": func(i int) int { return i + 100 }, "dec": func(i int) int {
		if i < 100 {
			return 0
		}
		return i - 100
	}}
	srv := &Server{service: s, secure: secure, templates: template.Must(template.New("templates.html").Funcs(funcs).ParseFS(assets, "templates.html", "explorer.html"))}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /explorer.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		b, _ := assets.ReadFile("explorer.js")
		w.Write(b)
	})
	mux.HandleFunc("GET /style.css", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		b, _ := assets.ReadFile("style.css")
		w.Write(b)
	})
	mux.HandleFunc("GET /login", srv.loginPage)
	mux.HandleFunc("POST /login", srv.loginUI)
	mux.HandleFunc("POST /api/v1/login", srv.loginAPI)
	mux.HandleFunc("/api/", srv.auth(srv.api))
	mux.HandleFunc("/", srv.auth(srv.ui))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		maxBody := inventory.MaxDocumentSize + 1024*1024
		if r.Method == "POST" && (r.URL.Path == "/items/new" || r.URL.Path == "/api/v1/items") {
			maxBody = inventory.MaxInitialUploadSize + 1024*1024
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBody)
		mux.ServeHTTP(w, r)
	})
}
func csrf(token string) string {
	h := sha256.Sum256([]byte("csrf:" + token))
	return hex.EncodeToString(h[:])
}
func requestToken(r *http.Request) (string, bool) {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer "), true
	}
	c, e := r.Cookie("session")
	if e == nil {
		return c.Value, false
	}
	return "", false
}
func sameOrigin(r *http.Request) bool {
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, e := url.Parse(origin)
		return e == nil && u.Host == r.Host && (u.Scheme == "http" || u.Scheme == "https")
	}
	return true
}
func (s *Server) auth(next func(http.ResponseWriter, *http.Request, inventory.Actor)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, bearer := requestToken(r)
		a, e := s.service.Authenticate(r.Context(), token)
		if e != nil {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				s.fail(w, r, e)
			} else {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
			}
			return
		}
		if a.TokenID != 0 && (!bearer || !strings.HasPrefix(r.URL.Path, "/api/")) {
			s.fail(w, r, inventory.ErrForbidden)
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			if !sameOrigin(r) {
				s.fail(w, r, inventory.ErrForbidden)
				return
			}
			if !bearer {
				provided := r.Header.Get("X-CSRF-Token")
				if provided == "" && !strings.HasPrefix(r.URL.Path, "/api/") {
					if e = parseForm(r); e != nil {
						s.fail(w, r, e)
						return
					}
					if r.MultipartForm != nil {
						defer r.MultipartForm.RemoveAll()
					}
					provided = r.FormValue("_csrf")
				}
				if subtle.ConstantTimeCompare([]byte(provided), []byte(csrf(token))) != 1 {
					s.fail(w, r, &inventory.Error{Code: "forbidden", Message: "Missing or invalid CSRF token"})
					return
				}
			}
		}
		if r.MultipartForm != nil {
			defer r.MultipartForm.RemoveAll()
		}
		if r.Method != "GET" && r.Method != "HEAD" && r.URL.Path != "/api/v1/items/batch-get" {
			if e = s.service.AllowAgentWrite(a); e != nil {
				s.fail(w, r, e)
				return
			}
		}
		next(w, r, a)
	}
}
func parseForm(r *http.Request) error {
	var e error
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		e = r.ParseMultipartForm(1 << 20)
	} else {
		e = r.ParseForm()
	}
	if e != nil {
		return &inventory.Error{Code: "validation", Message: "Invalid or oversized form"}
	}
	return nil
}
func (s *Server) fail(w http.ResponseWriter, r *http.Request, e error) {
	problem := &inventory.Error{Code: "internal", Message: "An internal error occurred"}
	status := 500
	var known *inventory.Error
	if errors.As(e, &known) {
		problem = known
		switch known.Code {
		case "validation":
			status = 400
		case "unauthorized":
			status = 401
		case "forbidden":
			status = 403
		case "not_found":
			status = 404
		case "rate_limited":
			status = 429
			w.Header().Set("Retry-After", "60")
		case "revision_conflict", "idempotency_conflict", "idempotency_expired":
			status = 409
		case "method_not_allowed":
			status = 405
		}
	} else {
		log.Printf("%s %s: %v", r.Method, r.URL.Path, e)
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeJSON(w, status, map[string]any{"error": problem})
		return
	}
	s.render(w, r, status, map[string]any{"View": "error", "Title": "Unable to complete request", "Error": problem.Message})
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
func decode(r *http.Request, v any) error {
	typ, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if e != nil || typ != "application/json" {
		return &inventory.Error{Code: "validation", Message: "Content-Type must be application/json"}
	}
	d := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1024*1024))
	d.DisallowUnknownFields()
	if e = d.Decode(v); e != nil {
		problem := &inventory.Error{Code: "validation", Message: "Invalid JSON body, field type or unknown field"}
		var typed *json.UnmarshalTypeError
		if errors.As(e, &typed) {
			problem.Details = map[string]any{"field": typed.Field}
		}
		return problem
	}
	if e = d.Decode(new(any)); e != io.EOF {
		return &inventory.Error{Code: "validation", Message: "Expected one JSON object"}
	}
	return nil
}
func (s *Server) render(w http.ResponseWriter, r *http.Request, status int, data map[string]any) {
	token, _ := requestToken(r)
	data["CSRF"] = csrf(token)
	theme := "light"
	if c, e := r.Cookie("theme"); e == nil && (c.Value == "dark" || c.Value == "light") {
		theme = c.Value
	}
	if u, ok := data["User"].(inventory.User); ok {
		theme = u.Theme
	} else if token != "" {
		if a, e := s.service.Authenticate(r.Context(), token); e == nil {
			if u, e := s.service.CurrentUser(r.Context(), a); e == nil {
				theme = u.Theme
			}
		}
	}
	data["Theme"] = theme
	var b bytes.Buffer
	if e := s.templates.ExecuteTemplate(&b, "page", data); e != nil {
		log.Print(e)
		http.Error(w, "Template error", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	w.Write(b.Bytes())
}
func (s *Server) allowLogin() bool {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	if time.Since(s.window) > time.Minute {
		s.window = time.Now()
		s.attempts = 0
	}
	s.attempts++
	return s.attempts <= 30
}
func (s *Server) login(w http.ResponseWriter, r *http.Request, username, password string) (string, error) {
	if !sameOrigin(r) {
		return "", inventory.ErrForbidden
	}
	if !s.allowLogin() {
		w.Header().Set("Retry-After", "60")
		return "", &inventory.Error{Code: "rate_limited", Message: "Too many login attempts; try again in a minute"}
	}
	token, e := s.service.Login(r.Context(), username, password)
	if e == nil {
		if a, err := s.service.Authenticate(r.Context(), token); err == nil {
			if u, err := s.service.CurrentUser(r.Context(), a); err == nil {
				s.themeCookie(w, u.Theme)
			}
		}
		http.SetCookie(w, &http.Cookie{Name: "session", Value: token, Path: "/", HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteStrictMode, MaxAge: 86400})
	}
	return token, e
}
func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, 200, map[string]any{"View": "login", "Title": "Sign in"})
}
func (s *Server) loginUI(w http.ResponseWriter, r *http.Request) {
	if e := parseForm(r); e != nil {
		s.fail(w, r, e)
		return
	}
	_, e := s.login(w, r, r.FormValue("username"), r.FormValue("password"))
	if e != nil {
		s.fail(w, r, e)
		return
	}
	http.Redirect(w, r, "/items", 303)
}
func (s *Server) loginAPI(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if e := decode(r, &in); e != nil {
		s.fail(w, r, e)
		return
	}
	token, e := s.login(w, r, in.Username, in.Password)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	writeJSON(w, 200, map[string]any{"data": map[string]any{"token": token, "csrf_token": csrf(token), "expires_in": 86400}})
}
func idValue(s string) (int64, error) {
	id, e := strconv.ParseInt(s, 10, 64)
	if e != nil || id <= 0 {
		return 0, &inventory.Error{Code: "validation", Message: "ID must be a positive integer"}
	}
	return id, nil
}
func methodError() error {
	return &inventory.Error{Code: "method_not_allowed", Message: "Method not allowed for this resource"}
}
func searchParams(r *http.Request) (inventory.Search, error) {
	q := r.URL.Query()
	f := inventory.Search{Query: q.Get("q"), Stock: q.Get("stock"), Archived: q.Get("archived"), Sort: q.Get("sort"), Direction: q.Get("direction")}
	if raw := q.Get("attribute_filters"); raw != "" {
		d := json.NewDecoder(strings.NewReader(raw))
		d.DisallowUnknownFields()
		if e := d.Decode(&f.AttributeFilters); e != nil {
			return f, &inventory.Error{Code: "validation", Message: "Invalid attribute_filters array"}
		}
		if e := d.Decode(new(any)); e != io.EOF {
			return f, &inventory.Error{Code: "validation", Message: "Expected one attribute_filters array"}
		}
	}
	var e error
	for key, dest := range map[string]*int64{"category_id": &f.CategoryID, "location_id": &f.LocationID} {
		if q.Get(key) != "" {
			*dest, e = idValue(q.Get(key))
			if e != nil {
				return f, e
			}
		}
	}
	for key, dest := range map[string]*int{"limit": &f.Limit, "offset": &f.Offset} {
		if q.Get(key) != "" {
			*dest, e = strconv.Atoi(q.Get(key))
			if e != nil || *dest < 0 || (key == "limit" && *dest == 0) {
				return f, &inventory.Error{Code: "validation", Message: "Invalid " + key}
			}
		}
	}
	return f, nil
}
func (s *Server) download(w http.ResponseWriter, r *http.Request, a inventory.Actor, id int64) error {
	d, f, e := s.service.DocumentContent(r.Context(), a, id)
	if e != nil {
		return e
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": d.Filename}))
	http.ServeContent(w, r, d.Filename, time.Time{}, f)
	return nil
}
func (s *Server) upload(r *http.Request, a inventory.Actor, id int64) (inventory.Document, error) {
	if e := parseForm(r); e != nil {
		return inventory.Document{}, e
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	file, h, e := r.FormFile("file")
	if e != nil {
		return inventory.Document{}, &inventory.Error{Code: "validation", Message: "A file is required"}
	}
	defer file.Close()
	return s.service.AttachDocument(r.Context(), a, id, inventory.Document{Title: r.FormValue("title"), Filename: h.Filename, Type: r.FormValue("type"), SourceURL: r.FormValue("source_url"), Manufacturer: r.FormValue("manufacturer"), Revision: r.FormValue("revision")}, file)
}
func (s *Server) export(w http.ResponseWriter, r *http.Request, a inventory.Actor) error {
	var b bytes.Buffer
	if e := s.service.Export(r.Context(), a, &b); e != nil {
		return e
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="inventory.csv"`)
	_, e := w.Write(b.Bytes())
	return e
}
func itemURL(id int64) string { return fmt.Sprintf("/items/%d", id) }

func (s *Server) themeCookie(w http.ResponseWriter, theme string) {
	http.SetCookie(w, &http.Cookie{Name: "theme", Value: theme, Path: "/", MaxAge: 365 * 86400, HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteStrictMode})
}
