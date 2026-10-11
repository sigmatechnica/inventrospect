package web

import (
	"encoding/json"
	"fmt"
	"inventrospect/internal/inventory"
	"net/http"
	"strconv"
	"strings"
)

func (s *Server) ui(w http.ResponseWriter, r *http.Request, a inventory.Actor) {
	if e := s.uiRequest(w, r, a); e != nil {
		s.fail(w, r, e)
	}
}
func (s *Server) uiRequest(w http.ResponseWriter, r *http.Request, a inventory.Actor) error {
	ctx := r.Context()
	u, e := s.service.CurrentUser(ctx, a)
	if e != nil {
		return e
	}
	data := map[string]any{"User": u, "CanEdit": u.Role != "viewer"}
	path := strings.Trim(r.URL.Path, "/")
	parts := strings.Split(path, "/")
	if r.Method == "POST" {
		if e = parseForm(r); e != nil {
			return e
		}
		if path == "logout" {
			token, _ := requestToken(r)
			if e = s.service.Logout(ctx, token); e != nil {
				return e
			}
			s.clearSession(w)
			http.Redirect(w, r, "/login", 303)
			return nil
		}
		switch parts[0] {
		case "items":
			if len(parts) == 2 && parts[1] != "" {
				id := int64(0)
				if parts[1] != "new" {
					id, e = idValue(parts[1])
					if e != nil {
						return e
					}
				}
				i, e := itemForm(r, id)
				if e != nil {
					return e
				}
				if id == 0 {
					i, e = s.createItemForm(r, a, i)
				} else {
					i, e = s.service.SaveItem(ctx, a, i)
				}
				if e != nil {
					return e
				}
				http.Redirect(w, r, itemURL(i.ID), 303)
				return nil
			}
			if len(parts) == 3 {
				id, err := idValue(parts[1])
				if err != nil {
					return err
				}
				switch parts[2] {
				case "adjust":
					loc, err := idValue(r.FormValue("location_id"))
					if err != nil {
						return err
					}
					q, err := quantityForm(r)
					if err != nil {
						return err
					}
					_, e = s.service.AdjustStock(ctx, a, id, inventory.Adjustment{LocationID: loc, Quantity: q, Reason: r.FormValue("reason")})
				case "move":
					from, err := idValue(r.FormValue("from_location_id"))
					if err != nil {
						return err
					}
					to, err := idValue(r.FormValue("to_location_id"))
					if err != nil {
						return err
					}
					q, err := quantityForm(r)
					if err != nil {
						return err
					}
					_, e = s.service.MoveStock(ctx, a, id, inventory.Transfer{FromLocationID: from, ToLocationID: to, Quantity: q, Reason: r.FormValue("reason")})
				case "documents":
					_, e = s.upload(r, a, id)
				case "archive":
					_, e = s.service.ArchiveItem(ctx, a, id, r.FormValue("archived") == "true")
				default:
					return inventory.ErrNotFound
				}
				if e != nil {
					return e
				}
				http.Redirect(w, r, itemURL(id), 303)
				return nil
			}
		case "tokens":
			if len(parts) == 3 && parts[2] == "revoke" {
				id, err := idValue(parts[1])
				if err != nil {
					return err
				}
				if err = s.service.RevokeToken(ctx, a, id); err != nil {
					return err
				}
				http.Redirect(w, r, "/tokens", 303)
				return nil
			}
			if len(parts) != 1 {
				return inventory.ErrNotFound
			}
			owner, err := idValue(r.FormValue("user_id"))
			if err != nil {
				return err
			}
			in := inventory.TokenInput{Name: r.FormValue("name"), UserID: owner, Scopes: r.Form["scopes"]}
			if expiry := strings.TrimSpace(r.FormValue("expires_at")); expiry != "" {
				in.ExpiresAt = &expiry
			}
			created, err := s.service.CreateToken(ctx, a, in)
			if err != nil {
				return err
			}
			data["View"] = "token_created"
			data["Title"] = "Agent token created"
			data["CreatedToken"] = created
			s.render(w, r, 201, data)
			return nil
		case "settings":
			if len(parts) != 1 {
				return inventory.ErrNotFound
			}
			user, err := s.service.SetTheme(ctx, a, r.FormValue("theme"))
			if err != nil {
				return err
			}
			s.themeCookie(w, user.Theme)
			http.Redirect(w, r, "/settings", 303)
			return nil
		case "categories", "locations":
			if len(parts) == 2 && parts[0] == "locations" && parts[1] == "default" {
				id, err := optionalID(r.FormValue("default_location_id"))
				if err != nil {
					return err
				}
				if _, err = s.service.SetDefaultLocation(ctx, a, id); err != nil {
					return err
				}
				http.Redirect(w, r, "/locations", 303)
				return nil
			}
			if len(parts) == 3 && parts[2] == "delete" {
				id, err := idValue(parts[1])
				if err != nil {
					return err
				}
				if err = s.service.DeleteNode(ctx, a, parts[0], id); err != nil {
					return err
				}
				http.Redirect(w, r, "/"+parts[0], 303)
				return nil
			}
			if len(parts) != 1 {
				return inventory.ErrNotFound
			}
			var n inventory.Node
			n.Name = r.FormValue("name")
			if r.FormValue("id") != "" {
				n.ID, e = idValue(r.FormValue("id"))
				if e != nil {
					return e
				}
			}
			n.ParentID, e = optionalID(r.FormValue("parent_id"))
			if e != nil {
				return e
			}
			_, e = s.service.SaveNode(ctx, a, parts[0], n)
			if e != nil {
				return e
			}
			http.Redirect(w, r, "/"+parts[0], 303)
			return nil
		case "users":
			if len(parts) != 1 {
				return inventory.ErrNotFound
			}
			user := inventory.User{Username: r.FormValue("username"), Role: r.FormValue("role"), Disabled: r.FormValue("disabled") == "on"}
			if r.FormValue("id") != "" {
				user.ID, e = idValue(r.FormValue("id"))
				if e != nil {
					return e
				}
			}
			_, e = s.service.SaveUser(ctx, a, user, r.FormValue("password"))
			if e != nil {
				return e
			}
			http.Redirect(w, r, "/users", 303)
			return nil
		}
		return inventory.ErrNotFound
	}
	if r.Method != "GET" {
		return methodError()
	}
	if path == "" {
		http.Redirect(w, r, "/items", 303)
		return nil
	}
	settings, e := s.service.Settings(ctx, a)
	if e != nil {
		return e
	}
	data["DefaultLocationID"] = settings.DefaultLocationID
	switch parts[0] {
	case "tokens":
		if len(parts) != 1 {
			return inventory.ErrNotFound
		}
		data["View"] = "tokens"
		data["Title"] = "Agent/API tokens"
		data["Tokens"], e = s.service.Tokens(ctx, a)
		if e != nil {
			return e
		}
		data["Users"], e = s.service.Users(ctx, a)
		if e != nil {
			return e
		}
		data["Scopes"] = inventory.TokenScopes
	case "settings":
		if len(parts) != 1 {
			return inventory.ErrNotFound
		}
		data["View"] = "settings"
		data["Title"] = "Your settings"
	case "export.csv":
		if len(parts) != 1 {
			return inventory.ErrNotFound
		}
		return s.export(w, r, a)
	case "items":
		if len(parts) == 1 {
			f, err := searchParams(r)
			if err != nil {
				return err
			}
			f.Limit = 100
			data["Items"], e = s.service.Search(ctx, a, f)
			if e != nil {
				return e
			}
			data["Filter"] = f
			data["View"] = "list"
			data["Title"] = "Inventory"
			q := r.URL.Query()
			q.Set("offset", strconv.Itoa(f.Offset+100))
			data["NextURL"] = "/items?" + q.Encode()
			prev := f.Offset - 100
			if prev < 0 {
				prev = 0
			}
			q.Set("offset", strconv.Itoa(prev))
			data["PrevURL"] = "/items?" + q.Encode()
		} else if len(parts) == 2 && parts[1] == "new" {
			if u.Role == "viewer" {
				return inventory.ErrForbidden
			}
			data["Item"] = inventory.Item{Tags: []string{}, Attributes: map[string]string{}}
			data["View"] = "edit"
			data["Title"] = "New item"
			data["Action"] = "/items/new"
		} else if len(parts) == 2 || len(parts) == 3 {
			id, err := idValue(parts[1])
			if err != nil {
				return err
			}
			i, err := s.service.Item(ctx, a, id)
			if err != nil {
				return err
			}
			data["Item"] = i
			data["Title"] = i.Name
			if len(parts) == 3 {
				if parts[2] != "edit" {
					return inventory.ErrNotFound
				}
				if u.Role == "viewer" {
					return inventory.ErrForbidden
				}
				data["View"] = "edit"
				data["Action"] = itemURL(id)
			} else {
				data["View"] = "detail"
				data["Stock"], e = s.service.Stock(ctx, a, id)
				if e != nil {
					return e
				}
				data["Documents"], e = s.service.Documents(ctx, a, id)
				if e != nil {
					return e
				}
			}
		} else {
			return inventory.ErrNotFound
		}
		data["Categories"], e = s.service.Nodes(ctx, a, "categories")
		if e != nil {
			return e
		}
		data["Locations"], e = s.service.Nodes(ctx, a, "locations")
		if e != nil {
			return e
		}
	case "categories", "locations":
		if len(parts) != 1 {
			return inventory.ErrNotFound
		}
		data["View"] = "nodes"
		data["Kind"] = parts[0]
		data["Title"] = strings.ToUpper(parts[0][:1]) + parts[0][1:]
		data["Nodes"], e = s.service.Nodes(ctx, a, parts[0])
		if e != nil {
			return e
		}
	case "users":
		if len(parts) != 1 {
			return inventory.ErrNotFound
		}
		data["View"] = "users"
		data["Title"] = "Users"
		data["Users"], e = s.service.Users(ctx, a)
		if e != nil {
			return e
		}
	case "documents":
		if len(parts) != 3 || parts[2] != "content" {
			return inventory.ErrNotFound
		}
		id, e := idValue(parts[1])
		if e != nil {
			return e
		}
		return s.download(w, r, a, id)
	default:
		return inventory.ErrNotFound
	}
	s.render(w, r, 200, data)
	return nil
}
func optionalID(v string) (*int64, error) {
	if v == "" {
		return nil, nil
	}
	id, e := idValue(v)
	return &id, e
}
func quantityForm(r *http.Request) (float64, error) {
	q, e := strconv.ParseFloat(r.FormValue("quantity"), 64)
	if e != nil {
		return 0, &inventory.Error{Code: "validation", Message: "Quantity must be a number"}
	}
	return q, nil
}
func itemForm(r *http.Request, id int64) (inventory.Item, error) {
	i := inventory.Item{ID: id, Name: r.FormValue("name"), Description: r.FormValue("description"), Manufacturer: r.FormValue("manufacturer"), MPN: r.FormValue("mpn"), Condition: r.FormValue("condition"), Notes: r.FormValue("notes"), Tags: strings.Split(r.FormValue("tags"), ","), Archived: r.FormValue("archived") == "on"}
	var e error
	i.CategoryID, e = optionalID(r.FormValue("category_id"))
	if e != nil {
		return i, e
	}
	if raw := strings.TrimSpace(r.FormValue("attributes")); raw != "" {
		if e = json.Unmarshal([]byte(raw), &i.Attributes); e != nil {
			return i, &inventory.Error{Code: "validation", Message: fmt.Sprintf("Attributes must be a JSON object with string values: %v", e)}
		}
	}
	return i, nil
}
