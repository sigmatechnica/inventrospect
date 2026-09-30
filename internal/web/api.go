package web

import (
	"inventrospect/internal/inventory"
	"net/http"
	"strings"
)

type itemInput struct {
	Name         string            `json:"name"`
	Description  string            `json:"description"`
	CategoryID   *int64            `json:"category_id"`
	Manufacturer string            `json:"manufacturer"`
	MPN          string            `json:"mpn"`
	Condition    string            `json:"condition"`
	Notes        string            `json:"notes"`
	Tags         []string          `json:"tags"`
	Attributes   map[string]string `json:"attributes"`
	Archived     bool              `json:"archived"`
}

func (i itemInput) item(id int64) inventory.Item {
	return inventory.Item{ID: id, Name: i.Name, Description: i.Description, CategoryID: i.CategoryID, Manufacturer: i.Manufacturer, MPN: i.MPN, Condition: i.Condition, Notes: i.Notes, Tags: i.Tags, Attributes: i.Attributes, Archived: i.Archived}
}
func (s *Server) api(w http.ResponseWriter, r *http.Request, a inventory.Actor) {
	data, status, e := s.apiRequest(w, r, a)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	if status != 0 {
		writeJSON(w, status, map[string]any{"data": data})
	}
}
func (s *Server) apiRequest(w http.ResponseWriter, r *http.Request, a inventory.Actor) (any, int, error) {
	p := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(p) < 3 || p[0] != "api" || p[1] != "v1" {
		return nil, 0, inventory.ErrNotFound
	}
	p = p[2:]
	ctx := r.Context()
	method := r.Method
	if len(p) == 1 {
		switch p[0] {
		case "settings":
			if method == "GET" {
				settings, e := s.service.Settings(ctx, a)
				return settings, 200, e
			}
			if method == "PUT" {
				var in inventory.Settings
				if e := decode(r, &in); e != nil {
					return nil, 0, e
				}
				settings, e := s.service.SetDefaultLocation(ctx, a, in.DefaultLocationID)
				return settings, 200, e
			}
			return nil, 0, methodError()
		case "me":
			if method != "GET" {
				return nil, 0, methodError()
			}
			u, e := s.service.CurrentUser(ctx, a)
			return u, 200, e
		case "logout":
			if method != "POST" {
				return nil, 0, methodError()
			}
			token, _ := requestToken(r)
			e := s.service.Logout(ctx, token)
			s.clearSession(w)
			return map[string]bool{"logged_out": true}, 200, e
		case "export.csv":
			if method != "GET" {
				return nil, 0, methodError()
			}
			return nil, 0, s.export(w, r, a)
		case "items":
			if method == "GET" {
				f, e := searchParams(r)
				if e != nil {
					return nil, 0, e
				}
				items, e := s.service.Search(ctx, a, f)
				return items, 200, e
			}
			if method == "POST" {
				if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
					if e := parseForm(r); e != nil {
						return nil, 0, e
					}
					if r.MultipartForm != nil {
						defer r.MultipartForm.RemoveAll()
					}
					i, e := itemForm(r, 0)
					if e != nil {
						return nil, 0, e
					}
					i, e = s.createItemForm(r, a, i)
					if e == nil {
						w.Header().Set("Location", "/api/v1"+itemURL(i.ID))
					}
					return i, 201, e
				}
				var in struct {
					itemInput
					InitialStock inventory.Adjustment `json:"initial_stock"`
				}
				if e := decode(r, &in); e != nil {
					return nil, 0, e
				}
				i, e := s.service.CreateItem(ctx, a, in.item(0), in.InitialStock, nil)
				if e == nil {
					w.Header().Set("Location", "/api/v1"+itemURL(i.ID))
				}
				return i, 201, e
			}
			return nil, 0, methodError()
		case "categories", "locations":
			if method == "GET" {
				nodes, e := s.service.Nodes(ctx, a, p[0])
				return nodes, 200, e
			}
			if method == "POST" {
				var in struct {
					Name     string `json:"name"`
					ParentID *int64 `json:"parent_id"`
				}
				if e := decode(r, &in); e != nil {
					return nil, 0, e
				}
				n, e := s.service.SaveNode(ctx, a, p[0], inventory.Node{Name: in.Name, ParentID: in.ParentID})
				return n, 201, e
			}
			return nil, 0, methodError()
		case "users":
			if method == "GET" {
				users, e := s.service.Users(ctx, a)
				return users, 200, e
			}
			if method == "POST" {
				return s.apiUser(r, a, 0)
			}
			return nil, 0, methodError()
		}
	}
	if len(p) < 2 {
		return nil, 0, inventory.ErrNotFound
	}
	if len(p) == 2 && p[0] == "me" && p[1] == "preferences" {
		if method == "GET" {
			u, e := s.service.CurrentUser(ctx, a)
			return map[string]string{"theme": u.Theme}, 200, e
		}
		if method == "PUT" {
			var in struct {
				Theme string `json:"theme"`
			}
			if e := decode(r, &in); e != nil {
				return nil, 0, e
			}
			u, e := s.service.SetTheme(ctx, a, in.Theme)
			if e == nil {
				s.themeCookie(w, u.Theme)
			}
			return map[string]string{"theme": u.Theme}, 200, e
		}
		return nil, 0, methodError()
	}
	id, e := idValue(p[1])
	if e != nil {
		return nil, 0, e
	}
	if len(p) == 2 {
		switch p[0] {
		case "items":
			if method == "GET" {
				i, e := s.service.Item(ctx, a, id)
				return i, 200, e
			}
			if method == "PUT" {
				var in itemInput
				if e = decode(r, &in); e != nil {
					return nil, 0, e
				}
				i, e := s.service.SaveItem(ctx, a, in.item(id))
				return i, 200, e
			}
			return nil, 0, methodError()
		case "categories", "locations":
			if method == "DELETE" {
				e := s.service.DeleteNode(ctx, a, p[0], id)
				return map[string]bool{"deleted": true}, 200, e
			}
			if method != "PUT" {
				return nil, 0, methodError()
			}
			var in struct {
				Name     string `json:"name"`
				ParentID *int64 `json:"parent_id"`
			}
			if e = decode(r, &in); e != nil {
				return nil, 0, e
			}
			n, e := s.service.SaveNode(ctx, a, p[0], inventory.Node{ID: id, Name: in.Name, ParentID: in.ParentID})
			return n, 200, e
		case "documents":
			if method != "GET" {
				return nil, 0, methodError()
			}
			d, e := s.service.Document(ctx, a, id)
			return d, 200, e
		case "users":
			if method != "PUT" {
				return nil, 0, methodError()
			}
			return s.apiUser(r, a, id)
		}
	}
	if len(p) == 3 {
		if p[0] == "documents" && p[2] == "content" {
			if method != "GET" {
				return nil, 0, methodError()
			}
			return nil, 0, s.download(w, r, a, id)
		}
		if p[0] == "items" {
			switch p[2] {
			case "archive":
				if method != "PUT" {
					return nil, 0, methodError()
				}
				var in struct {
					Archived *bool `json:"archived"`
				}
				if e = decode(r, &in); e != nil {
					return nil, 0, e
				}
				if in.Archived == nil {
					return nil, 0, &inventory.Error{Code: "validation", Message: "archived is required"}
				}
				i, e := s.service.ArchiveItem(ctx, a, id, *in.Archived)
				return i, 200, e
			case "attributes":
				if method != "GET" {
					return nil, 0, methodError()
				}
				i, e := s.service.Item(ctx, a, id)
				return i.Attributes, 200, e
			case "stock":
				if method != "GET" {
					return nil, 0, methodError()
				}
				stock, e := s.service.Stock(ctx, a, id)
				return stock, 200, e
			case "documents":
				if method == "GET" {
					docs, e := s.service.Documents(ctx, a, id)
					return docs, 200, e
				}
				if method == "POST" {
					d, e := s.upload(r, a, id)
					return d, 201, e
				}
				return nil, 0, methodError()
			}
		}
	}
	if len(p) == 4 && p[0] == "items" && p[2] == "stock" {
		if method != "POST" {
			return nil, 0, methodError()
		}
		switch p[3] {
		case "adjustments":
			var in inventory.Adjustment
			if e = decode(r, &in); e != nil {
				return nil, 0, e
			}
			m, e := s.service.AdjustStock(ctx, a, id, in)
			return m, 201, e
		case "transfers":
			var in inventory.Transfer
			if e = decode(r, &in); e != nil {
				return nil, 0, e
			}
			m, e := s.service.MoveStock(ctx, a, id, in)
			return m, 201, e
		}
	}
	return nil, 0, inventory.ErrNotFound
}
func (s *Server) apiUser(r *http.Request, a inventory.Actor, id int64) (any, int, error) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
		Disabled bool   `json:"disabled"`
	}
	if e := decode(r, &in); e != nil {
		return nil, 0, e
	}
	u, e := s.service.SaveUser(r.Context(), a, inventory.User{ID: id, Username: in.Username, Role: in.Role, Disabled: in.Disabled}, in.Password)
	status := 200
	if id == 0 {
		status = 201
	}
	return u, status, e
}
func (s *Server) clearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: "session", Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteStrictMode})
}
