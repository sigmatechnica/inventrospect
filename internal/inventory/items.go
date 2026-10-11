package inventory

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
)

const itemColumns = `i.id,i.name,i.description,i.category_id,i.manufacturer,i.mpn,i.condition,i.notes,i.tags,i.attributes,i.archived,i.created_at,i.updated_at,i.created_by,i.updated_by,COALESCE((SELECT SUM(quantity)/1000000.0 FROM stock_movements WHERE item_id=i.id),0)`

type scanner interface{ Scan(...any) error }

func scanItem(row scanner) (Item, error) {
	var i Item
	var tags, attrs string
	e := row.Scan(&i.ID, &i.Name, &i.Description, &i.CategoryID, &i.Manufacturer, &i.MPN, &i.Condition, &i.Notes, &tags, &attrs, &i.Archived, &i.CreatedAt, &i.UpdatedAt, &i.CreatedBy, &i.UpdatedBy, &i.Quantity)
	if e == sql.ErrNoRows {
		return i, ErrNotFound
	}
	if e != nil {
		return i, e
	}
	if e = json.Unmarshal([]byte(tags), &i.Tags); e != nil {
		return i, e
	}
	e = json.Unmarshal([]byte(attrs), &i.Attributes)
	return i, e
}
func (s *Service) Item(ctx context.Context, a Actor, id int64) (Item, error) {
	if e := s.authorize(ctx, a, "inventory:read"); e != nil {
		return Item{}, e
	}
	return scanItem(s.db.QueryRowContext(ctx, "SELECT "+itemColumns+" FROM items i WHERE i.id=?", id))
}
func (s *Service) Search(ctx context.Context, a Actor, f Search) ([]Item, error) {
	if e := s.authorize(ctx, a, "inventory:read"); e != nil {
		return nil, e
	}
	if f.Limit == 0 {
		f.Limit = 100
	}
	if f.Limit < 1 || f.Limit > 1000 || f.Offset < 0 {
		return nil, invalid("limit must be 1–1000 and offset must be nonnegative")
	}
	query := "SELECT " + itemColumns + " FROM items i WHERE 1=1"
	args := []any{}
	if f.Query != "" {
		query += ` AND (i.name LIKE ? ESCAPE '\' OR i.description LIKE ? ESCAPE '\' OR i.manufacturer LIKE ? ESCAPE '\' OR i.mpn LIKE ? ESCAPE '\' OR i.notes LIKE ? ESCAPE '\' OR EXISTS(SELECT 1 FROM json_each(i.tags) WHERE value LIKE ? ESCAPE '\'))`
		q := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(f.Query) + "%"
		for j := 0; j < 6; j++ {
			args = append(args, q)
		}
	}
	if f.CategoryID > 0 {
		query += ` AND i.category_id IN (WITH RECURSIVE tree(id) AS (SELECT id FROM categories WHERE id=? UNION ALL SELECT c.id FROM categories c JOIN tree t ON c.parent_id=t.id) SELECT id FROM tree)`
		args = append(args, f.CategoryID)
	}
	balance := `COALESCE((SELECT SUM(s.quantity) FROM stock_movements s WHERE s.item_id=i.id),0)`
	if f.LocationID > 0 {
		loc := `s.location_id IN (WITH RECURSIVE tree(id) AS (SELECT id FROM locations WHERE id=? UNION ALL SELECT l.id FROM locations l JOIN tree t ON l.parent_id=t.id) SELECT id FROM tree)`
		balance = `COALESCE((SELECT SUM(s.quantity) FROM stock_movements s WHERE s.item_id=i.id AND ` + loc + `),0)`
		if f.Stock == "" {
			query += ` AND ` + balance + `>0`
			args = append(args, f.LocationID)
		}
	}
	if f.Stock != "" {
		if f.Stock != "in" && f.Stock != "out" {
			return nil, invalid("stock must be in or out")
		}
		query += " AND " + balance
		if f.Stock == "in" {
			query += ">0"
		} else {
			query += "<=0"
		}
		if f.LocationID > 0 {
			args = append(args, f.LocationID)
		}
	}
	switch f.Archived {
	case "", "false":
		query += " AND i.archived=0"
	case "true":
		query += " AND i.archived=1"
	case "all":
	default:
		return nil, invalid("archived must be false, true or all")
	}
	query += " ORDER BY i.name COLLATE NOCASE,i.id LIMIT ? OFFSET ?"
	args = append(args, f.Limit, f.Offset)
	rows, e := s.db.QueryContext(ctx, query, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Item{}
	for rows.Next() {
		i, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}
func (s *Service) SaveItem(ctx context.Context, a Actor, i Item) (Item, error) {
	if e := s.authorize(ctx, a, "inventory:write"); e != nil {
		return i, e
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return i, e
	}
	defer tx.Rollback()
	i, e = saveItem(ctx, tx, a, i)
	if e != nil {
		return i, e
	}
	if e = tx.Commit(); e != nil {
		return i, e
	}
	return s.itemRecord(ctx, i.ID)
}

// Shared by ordinary edits and atomic creation with initial stock/documents.
func saveItem(ctx context.Context, tx *sql.Tx, a Actor, i Item) (Item, error) {
	i.Name = clean(i.Name)
	if i.Name == "" || len(i.Name) > 300 {
		return i, invalid("Item name must be 1–300 characters")
	}
	if len(i.Description)+len(i.Notes) > 100000 {
		return i, invalid("Description and notes are too long")
	}
	if len(i.Tags) > 100 || len(i.Attributes) > 200 {
		return i, invalid("Too many tags or attributes")
	}
	tags := []string{}
	seen := map[string]bool{}
	for _, t := range i.Tags {
		t = clean(t)
		if len(t) > 100 {
			return i, invalid("Tags must be at most 100 characters")
		}
		if t != "" && !seen[t] {
			tags = append(tags, t)
			seen[t] = true
		}
	}
	i.Tags = tags
	if i.Attributes == nil {
		i.Attributes = map[string]string{}
	}
	for k, v := range i.Attributes {
		if clean(k) == "" || len(k) > 100 || len(v) > 2000 {
			return i, invalid("Attribute names must be 1–100 characters and values at most 2000 characters")
		}
	}
	tagJSON, _ := json.Marshal(i.Tags)
	attrJSON, _ := json.Marshal(i.Attributes)
	var e error
	if i.CategoryID != nil {
		if e = exists(ctx, tx, "categories", *i.CategoryID); e != nil {
			return i, e
		}
	}
	i.UpdatedAt = now()
	i.UpdatedBy = a.UserID
	if i.ID == 0 {
		i.CreatedAt = i.UpdatedAt
		i.CreatedBy = a.UserID
		r, err := tx.ExecContext(ctx, `INSERT INTO items(name,description,category_id,manufacturer,mpn,condition,notes,tags,attributes,archived,created_at,updated_at,created_by,updated_by) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, i.Name, i.Description, i.CategoryID, i.Manufacturer, i.MPN, i.Condition, i.Notes, string(tagJSON), string(attrJSON), i.Archived, i.CreatedAt, i.UpdatedAt, i.CreatedBy, i.UpdatedBy)
		e = err
		if e == nil {
			i.ID, e = r.LastInsertId()
		}
	} else {
		e = tx.QueryRowContext(ctx, `SELECT created_at,created_by FROM items WHERE id=?`, i.ID).Scan(&i.CreatedAt, &i.CreatedBy)
		if e == sql.ErrNoRows {
			return i, ErrNotFound
		}
		if e != nil {
			return i, e
		}
		_, e = tx.ExecContext(ctx, `UPDATE items SET name=?,description=?,category_id=?,manufacturer=?,mpn=?,condition=?,notes=?,tags=?,attributes=?,archived=?,updated_at=?,updated_by=? WHERE id=?`, i.Name, i.Description, i.CategoryID, i.Manufacturer, i.MPN, i.Condition, i.Notes, string(tagJSON), string(attrJSON), i.Archived, i.UpdatedAt, i.UpdatedBy, i.ID)
	}
	if e != nil {
		return i, e
	}
	if e = audit(ctx, tx, a, "save", "item", i.ID); e != nil {
		return i, e
	}
	return i, nil
}
func (s *Service) ArchiveItem(ctx context.Context, a Actor, id int64, archived bool) (Item, error) {
	if e := s.authorize(ctx, a, "inventory:write"); e != nil {
		return Item{}, e
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return Item{}, e
	}
	defer tx.Rollback()
	r, e := tx.ExecContext(ctx, `UPDATE items SET archived=?,updated_at=?,updated_by=? WHERE id=?`, archived, now(), a.UserID, id)
	if e != nil {
		return Item{}, e
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return Item{}, ErrNotFound
	}
	if e = audit(ctx, tx, a, "archive", "item", id); e != nil {
		return Item{}, e
	}
	if e = tx.Commit(); e != nil {
		return Item{}, e
	}
	return s.itemRecord(ctx, id)
}
