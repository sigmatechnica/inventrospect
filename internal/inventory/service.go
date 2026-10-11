package inventory

import (
	"context"
	"crypto/rand"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schema string

//go:embed migration2.sql
var migration2 string

//go:embed migration3.sql
var migration3 string

type Service struct {
	db      *sql.DB
	dataDir string
}

func Open(dataDir string) (*Service, error) {
	abs, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Join(abs, "documents"), 0700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(abs, "inventory.db"))
	if err != nil {
		return nil, err
	}
	// One connection serializes writes and guarantees connection-local SQLite settings.
	db.SetMaxOpenConns(1)
	s := &Service{db: db, dataDir: abs}
	if _, err = db.Exec(`PRAGMA foreign_keys=ON; PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000; CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY);`); err != nil {
		db.Close()
		return nil, err
	}
	var version int
	if err = db.QueryRow(`SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&version); err != nil {
		db.Close()
		return nil, err
	}
	migrations := []string{schema, migration2, migration3}
	if version > len(migrations) {
		db.Close()
		return nil, errors.New("database schema is newer than this executable")
	}
	for next := version + 1; next <= len(migrations); next++ {
		tx, e := db.Begin()
		if e != nil {
			db.Close()
			return nil, e
		}
		if _, e = tx.Exec(migrations[next-1]); e == nil {
			_, e = tx.Exec(`INSERT INTO schema_migrations(version) VALUES(?)`, next)
		}
		if e != nil {
			tx.Rollback()
			db.Close()
			return nil, e
		}
		if e = tx.Commit(); e != nil {
			db.Close()
			return nil, e
		}
	}
	if err = os.Chmod(filepath.Join(abs, "inventory.db"), 0600); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}
func (s *Service) Close() error { return s.db.Close() }
func now() string               { return time.Now().UTC().Format(time.RFC3339Nano) }
func randomToken() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func (s *Service) authorize(ctx context.Context, a Actor, scope string) error {
	var role string
	var disabled bool
	err := s.db.QueryRowContext(ctx, `SELECT role,disabled FROM users WHERE id=?`, a.UserID).Scan(&role, &disabled)
	if err == sql.ErrNoRows || disabled {
		return ErrAuth
	}
	if err != nil {
		return err
	}
	if scope == "admin" && role != "admin" {
		return ErrForbidden
	}
	if (scope == "write" || strings.HasSuffix(scope, ":write")) && role == "viewer" {
		return ErrForbidden
	}
	if a.TokenID != 0 {
		var raw string
		e := s.db.QueryRowContext(ctx, `SELECT scopes FROM api_tokens WHERE id=? AND user_id=? AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at>?)`, a.TokenID, a.UserID, time.Now().UTC().Format(time.RFC3339)).Scan(&raw)
		if e == sql.ErrNoRows {
			return ErrAuth
		}
		if e != nil {
			return e
		}
		if e = json.Unmarshal([]byte(raw), &a.Scopes); e != nil {
			return e
		}
		if len(a.Scopes) == 0 {
			return ErrForbidden
		}
	}
	if a.Scopes != nil {
		for _, v := range a.Scopes {
			if v == scope || (a.TokenID == 0 && ((v == "read" && strings.HasSuffix(scope, ":read")) || (v == "write" && strings.HasSuffix(scope, ":write")))) {
				return nil
			}
		}
		return ErrForbidden
	}
	return nil
}
func (s *Service) CurrentUser(ctx context.Context, a Actor) (User, error) {
	if e := s.authorize(ctx, a, "read"); e != nil {
		return User{}, e
	}
	var u User
	e := s.db.QueryRowContext(ctx, `SELECT id,username,role,disabled,theme FROM users WHERE id=?`, a.UserID).Scan(&u.ID, &u.Username, &u.Role, &u.Disabled, &u.Theme)
	return u, e
}
func audit(ctx context.Context, tx *sql.Tx, a Actor, action, resource string, id int64) error {
	_, e := tx.ExecContext(ctx, `INSERT INTO audit_events(actor_id,action,resource,resource_id,created_at,token_id) VALUES(?,?,?,?,?,?)`, a.UserID, action, resource, id, now(), tokenID(a))
	return e
}
func clean(s string) string { return strings.TrimSpace(s) }
func exists(ctx context.Context, tx *sql.Tx, table string, id int64) error {
	var n int
	e := tx.QueryRowContext(ctx, "SELECT 1 FROM "+table+" WHERE id=?", id).Scan(&n)
	if e == sql.ErrNoRows {
		return invalid("Unknown %s ID", table)
	}
	return e
}

func (s *Service) itemExists(ctx context.Context, id int64) error {
	var found int
	e := s.db.QueryRowContext(ctx, `SELECT 1 FROM items WHERE id=?`, id).Scan(&found)
	if e == sql.ErrNoRows {
		return ErrNotFound
	}
	return e
}
func (s *Service) itemRecord(ctx context.Context, id int64) (Item, error) {
	return scanItem(s.db.QueryRowContext(ctx, "SELECT "+itemColumns+" FROM items i WHERE i.id=?", id))
}
