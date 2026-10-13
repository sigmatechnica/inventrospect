package inventory

import (
	"context"
	"database/sql"
	"log"
	"os"
	"path/filepath"
)

type DeleteInput struct {
	IDs    []int64 `json:"ids"`
	DryRun bool    `json:"dry_run,omitempty"`
}
type DeleteEntry struct {
	ID        int64  `json:"id"`
	Status    string `json:"status"`
	Documents int    `json:"documents"`
	Movements int    `json:"stock_movements"`
}
type DeleteResult struct {
	DryRun         bool          `json:"dry_run"`
	Items          int           `json:"items"`
	Documents      int           `json:"documents"`
	Movements      int           `json:"stock_movements"`
	Results        []DeleteEntry `json:"results"`
	CleanupPending bool          `json:"cleanup_pending"`
}

func (s *Service) DeleteItems(ctx context.Context, a Actor, in DeleteInput) (DeleteResult, error) {
	if e := s.authorize(ctx, a, "inventory:delete"); e != nil {
		return DeleteResult{}, e
	}
	if e := s.checkBulk(len(in.IDs)); e != nil {
		return DeleteResult{}, e
	}
	seen := map[int64]bool{}
	for _, id := range in.IDs {
		if id <= 0 || seen[id] {
			return DeleteResult{}, invalid("IDs must be positive and distinct")
		}
		seen[id] = true
	}
	out, e := mutate(s, ctx, a, "delete_items", "", in, func(tx *sql.Tx) (DeleteResult, error) {
		out := DeleteResult{DryRun: in.DryRun, Results: []DeleteEntry{}}
		for _, id := range in.IDs {
			r := DeleteEntry{ID: id, Status: "deleted"}
			var n int
			if e := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM items WHERE id=?`, id).Scan(&n); e != nil {
				return out, e
			}
			if n == 0 {
				if e := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM deleted_items WHERE id=?`, id).Scan(&n); e != nil {
					return out, e
				}
				if n == 0 {
					return out, ErrNotFound
				}
				r.Status = "already_deleted"
			} else {
				if in.DryRun {
					r.Status = "would_delete"
				}
				if e := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM documents WHERE item_id=?`, id).Scan(&r.Documents); e != nil {
					return out, e
				}
				if e := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM stock_movements WHERE item_id=?`, id).Scan(&r.Movements); e != nil {
					return out, e
				}
				out.Items++
				out.Documents += r.Documents
				out.Movements += r.Movements
			}
			out.Results = append(out.Results, r)
		}
		if in.DryRun {
			return out, nil
		}
		for _, r := range out.Results {
			if r.Status == "already_deleted" {
				continue
			}
			for _, statement := range []string{
				`INSERT INTO document_cleanup(storage_key,queued_at) SELECT storage_key,strftime('%Y-%m-%dT%H:%M:%SZ','now') FROM documents WHERE item_id=? ON CONFLICT DO NOTHING`,
				`DELETE FROM documents WHERE item_id=?`,
				`INSERT INTO item_deletion_guard(item_id) VALUES(?)`,
				`DELETE FROM stock_movements WHERE item_id=?`,
				`DELETE FROM items WHERE id=?`,
				`DELETE FROM item_deletion_guard WHERE item_id=?`,
			} {
				if _, e := tx.ExecContext(ctx, statement, r.ID); e != nil {
					return out, e
				}
			}
			if _, e := tx.ExecContext(ctx, `INSERT INTO deleted_items(id,deleted_at,actor_id,token_id) VALUES(?,?,?,?)`, r.ID, now(), a.UserID, tokenID(a)); e != nil {
				return out, e
			}
			if e := audit(ctx, tx, a, "permanent_delete", "item", r.ID); e != nil {
				return out, e
			}
		}
		return out, nil
	})
	if e == nil && !in.DryRun {
		if err := s.cleanupFiles(context.WithoutCancel(ctx)); err != nil {
			out.CleanupPending = true
			log.Printf("Document cleanup pending: %v", err)
		}
	}
	if e == nil && !in.DryRun {
		var pending int
		if err := s.db.QueryRowContext(context.WithoutCancel(ctx), `SELECT COUNT(*) FROM document_cleanup`).Scan(&pending); err != nil || pending > 0 {
			out.CleanupPending = true
		}
	}
	return out, e
}

// Metadata is removed before bytes. Failures leave a durable queue, retried at
// startup and on subsequent deletion calls. Never unlink a referenced key.
func (s *Service) cleanupFiles(ctx context.Context) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	rows, e := tx.QueryContext(ctx, `SELECT storage_key FROM document_cleanup LIMIT 1000`)
	if e != nil {
		return e
	}
	keys := []string{}
	for rows.Next() {
		var k string
		if e = rows.Scan(&k); e != nil {
			rows.Close()
			return e
		}
		keys = append(keys, k)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, k := range keys {
		var refs int
		if e = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM documents WHERE storage_key=?`, k).Scan(&refs); e != nil {
			return e
		}
		if refs == 0 {
			if k == "" || k == "." || k == ".." || filepath.Base(k) != k {
				return invalid("Invalid queued storage key")
			}
			if e = os.Remove(filepath.Join(s.dataDir, "documents", k)); e != nil && !os.IsNotExist(e) {
				return e
			}
		}
		if _, e = tx.ExecContext(ctx, `DELETE FROM document_cleanup WHERE storage_key=?`, k); e != nil {
			return e
		}
	}
	return tx.Commit()
}
