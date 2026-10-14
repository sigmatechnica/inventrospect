package web

import (
	"inventrospect/internal/inventory"
	"net/http"
	"strconv"
	"strings"
)

type itemInput = inventory.ItemInput

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
	if len(p) == 2 && p[0] == "items" && p[1] == "batch-get" {
		if method != "POST" {
			return nil, 0, methodError()
		}
		var in struct {
			IDs []int64 `json:"ids"`
		}
		if e := decode(r, &in); e != nil {
			return nil, 0, e
		}
		v, e := s.service.GetItems(ctx, a, in.IDs)
		return v, 200, e
	}
	if len(p) == 2 && p[0] == "agent" && p[1] == "me" {
		if method != "GET" {
			return nil, 0, methodError()
		}
		if a.TokenID == 0 {
			return nil, 0, inventory.ErrAuth
		}
		return map[string]any{"user_id": a.UserID, "token_id": a.TokenID, "scopes": a.Scopes}, 200, nil
	}
	if len(p) == 3 && p[0] == "bulk" {
		if method != "POST" {
			return nil, 0, methodError()
		}
		switch p[1] + "/" + p[2] {
		case "items/delete":
			var in inventory.DeleteInput
			if e := decode(r, &in); e != nil {
				return nil, 0, e
			}
			in.Bulk = true
			v, e := s.service.DeleteItems(ctx, a, in)
			return v, 200, e
		case "categories/create", "locations/create":
			var in struct {
				Records []inventory.NodeInput `json:"records"`
			}
			if e := decode(r, &in); e != nil {
				return nil, 0, e
			}
			v, e := s.service.BulkCreateNodes(ctx, a, p[1], in.Records)
			return v, 200, e
		case "items/create":
			var in struct {
				Records         []inventory.CreateItemInput `json:"records"`
				ClientRequestID string                      `json:"client_request_id"`
			}
			if e := decode(r, &in); e != nil {
				return nil, 0, e
			}
			v, e := s.service.BulkCreateItems(ctx, a, in.Records, in.ClientRequestID)
			return v, 200, e
		case "items/patch":
			var in struct {
				Records []inventory.PatchItemInput `json:"records"`
			}
			if e := decode(r, &in); e != nil {
				return nil, 0, e
			}
			v, e := s.service.BulkPatchItems(ctx, a, in.Records)
			return v, 200, e
		case "items/update":
			var in struct {
				Records []inventory.UpdateItemInput `json:"records"`
			}
			if e := decode(r, &in); e != nil {
				return nil, 0, e
			}
			v, e := s.service.BulkUpdateItems(ctx, a, in.Records)
			return v, 200, e
		case "stock/adjustments":
			var in struct {
				Records         []inventory.AdjustStockInput `json:"records"`
				ClientRequestID string                       `json:"client_request_id"`
			}
			if e := decode(r, &in); e != nil {
				return nil, 0, e
			}
			v, e := s.service.BulkAdjustStock(ctx, a, in.Records, in.ClientRequestID)
			return v, 200, e
		}
		return nil, 0, inventory.ErrNotFound
	}
	if p[0] == "tokens" {
		if len(p) == 1 && method == "GET" {
			v, e := s.service.Tokens(ctx, a)
			return v, 200, e
		}
		if len(p) == 1 && method == "POST" {
			var in inventory.TokenInput
			if e := decode(r, &in); e != nil {
				return nil, 0, e
			}
			v, e := s.service.CreateToken(ctx, a, in)
			return v, 201, e
		}
		if len(p) == 3 && p[2] == "limits" && method == "PUT" {
			id, e := idValue(p[1])
			if e != nil {
				return nil, 0, e
			}
			var in inventory.TokenLimits
			if e = decode(r, &in); e != nil {
				return nil, 0, e
			}
			e = s.service.UpdateTokenLimits(ctx, a, id, in)
			return in, 200, e
		}
		if len(p) == 3 && p[2] == "revoke" && method == "POST" {
			id, e := idValue(p[1])
			if e != nil {
				return nil, 0, e
			}
			e = s.service.RevokeToken(ctx, a, id)
			return map[string]bool{"revoked": true}, 200, e
		}
		return nil, 0, methodError()
	}
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
					InitialStock    inventory.Adjustment `json:"initial_stock"`
					ClientRequestID string               `json:"client_request_id"`
				}
				if e := decode(r, &in); e != nil {
					return nil, 0, e
				}
				i, e := s.service.CreateItem(ctx, a, in.Item(0), in.InitialStock, nil, in.ClientRequestID)
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
				var in inventory.NodeInput
				if e := decode(r, &in); e != nil {
					return nil, 0, e
				}
				n, e := s.service.SaveNode(ctx, a, p[0], inventory.Node{Name: in.Name, ParentID: in.ParentID}, in.ClientRequestID)
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
			if method == "DELETE" {
				dry := false
				if raw := r.URL.Query().Get("dry_run"); raw != "" {
					var err error
					dry, err = strconv.ParseBool(raw)
					if err != nil {
						return nil, 0, &inventory.Error{Code: "validation", Message: "dry_run must be true or false"}
					}
				}
				v, e := s.service.DeleteItems(ctx, a, inventory.DeleteInput{IDs: []int64{id}, DryRun: dry})
				return v, 200, e
			}
			if method == "GET" {
				i, e := s.service.Item(ctx, a, id)
				return i, 200, e
			}
			if method == "PATCH" {
				var in inventory.PatchInput
				if e = decode(r, &in); e != nil {
					return nil, 0, e
				}
				i, e := s.service.PatchItem(ctx, a, id, in)
				return i, 200, e
			}
			if method == "PUT" {
				var in itemInput
				if e = decode(r, &in); e != nil {
					return nil, 0, e
				}
				i, e := s.service.SaveItem(ctx, a, in.Item(id))
				return i, 200, e
			}
			return nil, 0, methodError()
		case "categories", "locations":
			if method == "PATCH" {
				var in struct {
					Changes map[string]any `json:"changes"`
				}
				if e = decode(r, &in); e != nil {
					return nil, 0, e
				}
				v, e := s.service.PatchNode(ctx, a, p[0], id, in.Changes)
				return v, 200, e
			}

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
			case "details":
				if method != "GET" {
					return nil, 0, methodError()
				}
				v, e := s.service.GetItems(ctx, a, []int64{id})
				if e != nil {
					return nil, 0, e
				}
				if v.Results[0].Error != nil {
					return nil, 0, v.Results[0].Error
				}
				return v.Results[0].Data, 200, nil
			case "archive":
				if method != "PUT" {
					return nil, 0, methodError()
				}
				var in struct {
					Archived         *bool `json:"archived"`
					ExpectedRevision int64 `json:"expected_revision"`
				}
				if e = decode(r, &in); e != nil {
					return nil, 0, e
				}
				if in.Archived == nil {
					return nil, 0, &inventory.Error{Code: "validation", Message: "archived is required"}
				}
				i, e := s.service.ArchiveItem(ctx, a, id, *in.Archived, in.ExpectedRevision)
				return i, 200, e
			case "attributes":
				if method != "GET" {
					return nil, 0, methodError()
				}
				i, e := s.service.Item(ctx, a, id)
				return i.Attributes, 200, e
			case "stock-summary":
				if method != "GET" {
					return nil, 0, methodError()
				}
				v, e := s.service.StockSummary(ctx, a, id)
				return v, 200, e
			case "stock-history":
				if method != "GET" {
					return nil, 0, methodError()
				}
				limit := 50
				before := int64(0)
				var err error
				if raw := r.URL.Query().Get("limit"); raw != "" {
					limit, err = strconv.Atoi(raw)
					if err != nil || limit <= 0 {
						return nil, 0, &inventory.Error{Code: "validation", Message: "Invalid limit"}
					}
				}
				if raw := r.URL.Query().Get("before_id"); raw != "" {
					before, err = idValue(raw)
					if err != nil {
						return nil, 0, err
					}
				}
				v, e := s.service.StockHistory(ctx, a, id, before, limit)
				return v, 200, e
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
			var in struct {
				inventory.Adjustment
				ClientRequestID string `json:"client_request_id"`
			}
			if e = decode(r, &in); e != nil {
				return nil, 0, e
			}
			m, e := s.service.AdjustStock(ctx, a, id, in.Adjustment, in.ClientRequestID)
			return m, 201, e
		case "transfers":
			var in struct {
				inventory.Transfer
				ClientRequestID string `json:"client_request_id"`
			}
			if e = decode(r, &in); e != nil {
				return nil, 0, e
			}
			m, e := s.service.MoveStock(ctx, a, id, in.Transfer, in.ClientRequestID)
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
