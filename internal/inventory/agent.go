package inventory

import (
	"context"
	"errors"
	"log"
)

// ItemInput is the editable item contract shared by REST and bulk operations.
// PUT is a full replacement of these fields, not a merge.
type ItemInput struct {
	Name         string            `json:"name" jsonschema:"Item name (required)"`
	Description  string            `json:"description,omitempty" jsonschema:"Human-readable item description"`
	CategoryID   *int64            `json:"category_id,omitempty" jsonschema:"Existing category ID, or null for uncategorized"`
	Manufacturer string            `json:"manufacturer,omitempty"`
	MPN          string            `json:"mpn,omitempty"`
	Condition    string            `json:"condition,omitempty"`
	Notes        string            `json:"notes,omitempty"`
	Tags         []string          `json:"tags,omitempty" jsonschema:"Free-form tags; replacement replaces the entire list"`
	Attributes   map[string]string `json:"attributes,omitempty" jsonschema:"Free-form attribute names mapped to string values including units"`
	Archived     bool              `json:"archived,omitempty"`
}

func (i ItemInput) Item(id int64) Item {
	return Item{ID: id, Name: i.Name, Description: i.Description, CategoryID: i.CategoryID, Manufacturer: i.Manufacturer, MPN: i.MPN, Condition: i.Condition, Notes: i.Notes, Tags: i.Tags, Attributes: i.Attributes, Archived: i.Archived}
}

type CreateItemInput struct {
	Item         ItemInput  `json:"item" jsonschema:"Editable item fields"`
	InitialStock Adjustment `json:"initial_stock,omitempty" jsonschema:"Optional initial stock; positive quantity requires stock:write and a location or configured default"`
}
type UpdateItemInput struct {
	ID   int64     `json:"id" jsonschema:"Explicit positive ID of an existing item"`
	Item ItemInput `json:"item" jsonschema:"Complete replacement of editable fields; omitted fields reset"`
}
type AdjustStockInput struct {
	ItemID     int64      `json:"item_id" jsonschema:"Explicit positive inventory item ID"`
	Adjustment Adjustment `json:"adjustment" jsonschema:"Signed whole-number change at an explicit location"`
}
type BulkResult struct {
	Index int    `json:"index"`
	Data  any    `json:"data,omitempty"`
	Error *Error `json:"error,omitempty"`
}
type BulkResponse struct {
	Results   []BulkResult `json:"results"`
	Succeeded int          `json:"succeeded"`
	Failed    int          `json:"failed"`
	Semantics string       `json:"semantics"`
}

func bulkResult(index int, data any, err error) BulkResult {
	if err == nil {
		return BulkResult{Index: index, Data: data}
	}
	var known *Error
	if !errors.As(err, &known) {
		log.Printf("bulk record %d: %v", index, err)
		known = &Error{"internal", "An internal error occurred"}
	}
	return BulkResult{Index: index, Error: known}
}
func runBulk(n int, fn func(int) (any, error)) (BulkResponse, error) {
	out := BulkResponse{Results: []BulkResult{}, Semantics: "Each record commits independently in input order; failed records are rolled back. Successful records are not rolled back. Do not blindly retry successful stock adjustments."}
	if n < 1 || n > 100 {
		return out, invalid("A bulk request must contain 1–100 records")
	}
	for i := 0; i < n; i++ {
		data, e := fn(i)
		out.Results = append(out.Results, bulkResult(i, data, e))
		if e == nil {
			out.Succeeded++
		} else {
			out.Failed++
		}
	}
	return out, nil
}
func (s *Service) BulkCreateItems(ctx context.Context, a Actor, records []CreateItemInput) (BulkResponse, error) {
	if e := s.authorize(ctx, a, "inventory:write"); e != nil {
		return BulkResponse{}, e
	}
	return runBulk(len(records), func(i int) (any, error) {
		return s.CreateItem(ctx, a, records[i].Item.Item(0), records[i].InitialStock, nil)
	})
}
func (s *Service) BulkUpdateItems(ctx context.Context, a Actor, records []UpdateItemInput) (BulkResponse, error) {
	if e := s.authorize(ctx, a, "inventory:write"); e != nil {
		return BulkResponse{}, e
	}
	return runBulk(len(records), func(i int) (any, error) {
		if records[i].ID <= 0 {
			return nil, invalid("Item ID must be positive")
		}
		return s.SaveItem(ctx, a, records[i].Item.Item(records[i].ID))
	})
}
func (s *Service) BulkAdjustStock(ctx context.Context, a Actor, records []AdjustStockInput) (BulkResponse, error) {
	if e := s.authorize(ctx, a, "stock:write"); e != nil {
		return BulkResponse{}, e
	}
	return runBulk(len(records), func(i int) (any, error) { return s.AdjustStock(ctx, a, records[i].ItemID, records[i].Adjustment) })
}

type HistoryPage struct {
	Entries      []Movement `json:"entries"`
	NextBeforeID *int64     `json:"next_before_id"`
}

func (s *Service) StockHistory(ctx context.Context, a Actor, id, before int64, limit int) (HistoryPage, error) {
	out := HistoryPage{Entries: []Movement{}}
	if e := s.authorize(ctx, a, "stock:read"); e != nil {
		return out, e
	}
	if e := s.itemExists(ctx, id); e != nil {
		return out, e
	}
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 200 || before < 0 {
		return out, invalid("limit must be 1–200 and before_id nonnegative")
	}
	rows, e := s.db.QueryContext(ctx, `SELECT id,item_id,location_id,quantity/1000000.0,reason,transfer_id,actor_id,created_at,token_id FROM stock_movements WHERE item_id=? AND (?=0 OR id<?) ORDER BY id DESC LIMIT ?`, id, before, before, limit+1)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		var m Movement
		if e = rows.Scan(&m.ID, &m.ItemID, &m.LocationID, &m.Quantity, &m.Reason, &m.TransferID, &m.ActorID, &m.CreatedAt, &m.TokenID); e != nil {
			return out, e
		}
		out.Entries = append(out.Entries, m)
	}
	if len(out.Entries) > limit {
		out.Entries = out.Entries[:limit]
		last := out.Entries[limit-1].ID
		out.NextBeforeID = &last
	}
	return out, rows.Err()
}
