package web

import (
	"io"
	"net/http"
	"strconv"

	"inventrospect/internal/inventory"
)

func (s *Server) createItemForm(r *http.Request, a inventory.Actor, i inventory.Item) (inventory.Item, error) {
	initial := inventory.Adjustment{}
	if raw := r.FormValue("initial_quantity"); raw != "" {
		q, e := strconv.ParseFloat(raw, 64)
		if e != nil {
			return i, &inventory.Error{Code: "validation", Message: "Initial quantity must be a nonnegative whole number"}
		}
		initial.Quantity = q
	}
	if raw := r.FormValue("initial_location_id"); raw != "" {
		id, e := idValue(raw)
		if e != nil {
			return i, e
		}
		initial.LocationID = id
	}
	uploads := []inventory.DocumentUpload{}
	files := []io.Closer{}
	defer func() {
		for _, f := range files {
			f.Close()
		}
	}()
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
		for _, field := range []struct{ name, kind string }{{"datasheets", "datasheet"}, {"files", "other"}} {
			for _, header := range r.MultipartForm.File[field.name] {
				if len(uploads) >= inventory.MaxInitialDocuments {
					return i, &inventory.Error{Code: "validation", Message: "Attach at most 10 files when creating an item"}
				}
				f, e := header.Open()
				if e != nil {
					return i, e
				}
				files = append(files, f)
				uploads = append(uploads, inventory.DocumentUpload{Document: inventory.Document{Filename: header.Filename, Type: field.kind}, Content: f})
			}
		}
	}
	return s.service.CreateItem(r.Context(), a, i, initial, uploads)
}
