package inventory

import (
	"context"
	"database/sql"
	"sort"
	"strings"
)

func nodeKind(kind string) bool { return kind == "categories" || kind == "locations" }
func (s *Service) Nodes(ctx context.Context, a Actor, kind string) ([]Node, error) {
	if e := s.authorize(ctx, a, "read"); e != nil {
		return nil, e
	}
	if !nodeKind(kind) {
		return nil, invalid("Unknown hierarchy")
	}
	rows, e := s.db.QueryContext(ctx, "SELECT id,name,parent_id FROM "+kind)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Node{}
	names := map[int64]Node{}
	for rows.Next() {
		var n Node
		if e = rows.Scan(&n.ID, &n.Name, &n.ParentID); e != nil {
			return nil, e
		}
		out = append(out, n)
		names[n.ID] = n
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	for i, n := range out {
		parts := []string{n.Name}
		for n.ParentID != nil {
			n = names[*n.ParentID]
			parts = append([]string{n.Name}, parts...)
		}
		out[i].Path = strings.Join(parts, " / ")
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}
func (s *Service) SaveNode(ctx context.Context, a Actor, kind string, n Node) (Node, error) {
	if e := s.authorize(ctx, a, "write"); e != nil {
		return n, e
	}
	if !nodeKind(kind) {
		return n, invalid("Unknown hierarchy")
	}
	n.Name = clean(n.Name)
	if n.Name == "" || len(n.Name) > 200 {
		return n, invalid("Name must be 1–200 characters")
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return n, e
	}
	defer tx.Rollback()
	if n.ID != 0 {
		if e = exists(ctx, tx, kind, n.ID); e != nil {
			return n, e
		}
	}
	if n.ParentID != nil {
		p := n.ParentID
		seen := map[int64]bool{}
		for p != nil {
			if *p == n.ID || seen[*p] {
				return n, invalid("Hierarchy cannot contain a cycle")
			}
			seen[*p] = true
			var next *int64
			e = tx.QueryRowContext(ctx, "SELECT parent_id FROM "+kind+" WHERE id=?", *p).Scan(&next)
			if e == sql.ErrNoRows {
				return n, invalid("Unknown parent ID")
			}
			if e != nil {
				return n, e
			}
			p = next
		}
	}
	if n.ID == 0 {
		r, err := tx.ExecContext(ctx, "INSERT INTO "+kind+"(name,parent_id) VALUES(?,?)", n.Name, n.ParentID)
		e = err
		if e == nil {
			n.ID, e = r.LastInsertId()
		}
	} else {
		_, e = tx.ExecContext(ctx, "UPDATE "+kind+" SET name=?,parent_id=? WHERE id=?", n.Name, n.ParentID, n.ID)
	}
	if e != nil {
		return n, e
	}
	if e = audit(ctx, tx, a, "save", kind, n.ID); e != nil {
		return n, e
	}
	return n, tx.Commit()
}

// DeleteNode removes only unreferenced leaves, preserving items and stock history.
func (s *Service) DeleteNode(ctx context.Context, a Actor, kind string, id int64) error {
	if e := s.authorize(ctx, a, "write"); e != nil {
		return e
	}
	if !nodeKind(kind) {
		return invalid("Unknown hierarchy")
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var count int
	e = tx.QueryRowContext(ctx, "SELECT count(*) FROM "+kind+" WHERE id=?", id).Scan(&count)
	if e != nil {
		return e
	}
	if count == 0 {
		return ErrNotFound
	}
	if e = tx.QueryRowContext(ctx, "SELECT count(*) FROM "+kind+" WHERE parent_id=?", id).Scan(&count); e != nil {
		return e
	}
	if count > 0 {
		return invalid("Move or delete child entries before deleting this entry")
	}
	if kind == "categories" {
		e = tx.QueryRowContext(ctx, `SELECT count(*) FROM items WHERE category_id=?`, id).Scan(&count)
		if e != nil {
			return e
		}
		if count > 0 {
			return invalid("Reassign all items (including archived items) before deleting this category")
		}
	} else {
		e = tx.QueryRowContext(ctx, `SELECT count(*) FROM stock_movements WHERE location_id=?`, id).Scan(&count)
		if e != nil {
			return e
		}
		if count > 0 {
			return invalid("This location has stock history and cannot be deleted, even if its balance is zero")
		}
	}
	if _, e = tx.ExecContext(ctx, "DELETE FROM "+kind+" WHERE id=?", id); e != nil {
		return e
	}
	if e = audit(ctx, tx, a, "delete", kind, id); e != nil {
		return e
	}
	return tx.Commit()
}
