package inventory

import (
	"context"
	"database/sql"
	"io"
)

const MaxInitialDocuments = 10
const MaxInitialUploadSize int64 = 100 * 1024 * 1024

type DocumentUpload struct {
	Document Document
	Content  io.Reader
}

// CreateItem saves the item, initial ledger entry and document metadata atomically.
// On failure, newly prepared files are removed; no partial item is left behind.
func (s *Service) CreateItem(ctx context.Context, a Actor, i Item, initial Adjustment, uploads []DocumentUpload, keys ...string) (Item, error) {
	if e := s.authorize(ctx, a, "inventory:write"); e != nil {
		return Item{}, e
	}
	if initial.Quantity != 0 {
		if e := s.authorize(ctx, a, "stock:write"); e != nil {
			return Item{}, e
		}
	}
	if len(uploads) > 0 {
		if e := s.authorize(ctx, a, "documents:write"); e != nil {
			return Item{}, e
		}
	}
	if i.ID != 0 {
		return Item{}, invalid("A new item must not have an ID")
	}
	if initial.Quantity != 0 {
		if _, ok := quantityUnits(initial.Quantity); !ok || initial.Quantity < 0 {
			return Item{}, invalid("Initial quantity must be a nonnegative whole number (maximum 1e9)")
		}
	}
	if i.Archived && initial.Quantity > 0 {
		return Item{}, invalid("An archived item cannot have initial stock")
	}
	if len(uploads) > MaxInitialDocuments {
		return Item{}, invalid("Attach at most 10 files when creating an item")
	}
	prepared := []Document{}
	committed := false
	defer func() {
		if !committed {
			for _, d := range prepared {
				s.removeDocumentFile(d)
			}
		}
	}()
	var total int64
	for _, u := range uploads {
		d, e := s.prepareDocument(a, u.Document, u.Content)
		if e != nil {
			return Item{}, e
		}
		prepared = append(prepared, d)
		total += d.Size
		if total > MaxInitialUploadSize {
			return Item{}, invalid("Initial uploads must total at most 100 MiB")
		}
	}
	fingerprints := append([]Document(nil), prepared...)
	for j := range fingerprints {
		fingerprints[j].UploadedAt = ""
		fingerprints[j].UploadedBy = 0
	}
	usedPrepared := false
	out, e := mutate(s, ctx, a, "create_item", requestKey(keys), []any{i, initial, fingerprints}, func(tx *sql.Tx) (Item, error) {
		usedPrepared = true
		return createItemTx(ctx, tx, a, i, initial, prepared)
	})
	// A replay has no references to the newly prepared duplicate files.
	committed = e == nil && usedPrepared
	return out, e
}
func createItemTx(ctx context.Context, tx *sql.Tx, a Actor, i Item, initial Adjustment, prepared []Document) (Item, error) {
	if i.ID != 0 {
		return Item{}, invalid("A new item must not have an ID")
	}
	if initial.Quantity != 0 {
		if _, ok := quantityUnits(initial.Quantity); !ok || initial.Quantity < 0 {
			return Item{}, invalid("Initial quantity must be a nonnegative whole number")
		}
	}
	if i.Archived && initial.Quantity > 0 {
		return Item{}, invalid("An archived item cannot have initial stock")
	}
	i, e := saveItem(ctx, tx, a, i)
	if e != nil {
		return Item{}, e
	}
	if initial.Quantity > 0 {
		if initial.LocationID == 0 {
			var location *int64
			if e = tx.QueryRowContext(ctx, `SELECT default_location_id FROM inventory_settings WHERE id=1`).Scan(&location); e != nil {
				return Item{}, e
			}
			if location != nil {
				initial.LocationID = *location
			}
		}
		if initial.LocationID == 0 {
			return Item{}, invalid("Choose a location for initial stock, or set a default location")
		}
		if e = exists(ctx, tx, "locations", initial.LocationID); e != nil {
			return Item{}, e
		}
		reason := clean(initial.Reason)
		if reason == "" {
			reason = "Initial stock"
		}
		if len(reason) > 2000 {
			return Item{}, invalid("Reason must be at most 2000 characters")
		}
		if _, e = movement(ctx, tx, a, i.ID, initial.LocationID, initial.Quantity, reason, nil, now()); e != nil {
			return Item{}, e
		}
	}
	for _, d := range prepared {
		if _, e = insertDocument(ctx, tx, a, i.ID, d); e != nil {
			return Item{}, e
		}
	}
	return scanItem(tx.QueryRowContext(ctx, "SELECT "+itemColumns+" FROM items i WHERE i.id=?", i.ID))
}
