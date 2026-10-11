package inventory

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"golang.org/x/crypto/bcrypt"
	"strings"
	"time"
)

func tokenHash(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
func validateUser(username, password, role string, required bool) error {
	if len(clean(username)) < 1 || len(username) > 100 {
		return invalid("Username must be 1–100 characters")
	}
	if (required || password != "") && (len(password) < 12 || len(password) > 72) {
		return invalid("Password must be 12–72 bytes")
	}
	if role != "admin" && role != "editor" && role != "viewer" {
		return invalid("Role must be admin, editor or viewer")
	}
	return nil
}

// Bootstrap only works before any users exist. It cannot reset an existing account.
func (s *Service) Bootstrap(ctx context.Context, username, password string) error {
	if e := validateUser(username, password, "admin", true); e != nil {
		return e
	}
	hash, e := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if e != nil {
		return e
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var n int
	if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&n); e != nil {
		return e
	}
	if n != 0 {
		return invalid("Initial admin already exists")
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO users(username,password_hash,role,created_at) VALUES(?,?,'admin',?)`, clean(username), hash, now())
	if e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Service) HasUsers(ctx context.Context) (bool, error) {
	var n int
	e := s.db.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n > 0, e
}

var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("not-a-real-account-password"), bcrypt.DefaultCost)

func (s *Service) Login(ctx context.Context, username, password string) (string, error) {
	var id int64
	var hash []byte
	var disabled bool
	e := s.db.QueryRowContext(ctx, `SELECT id,password_hash,disabled FROM users WHERE username=?`, clean(username)).Scan(&id, &hash, &disabled)
	if e != nil && e != sql.ErrNoRows {
		return "", e
	}
	if e == sql.ErrNoRows {
		hash = dummyHash
	}
	check := bcrypt.CompareHashAndPassword(hash, []byte(password))
	if check != nil || id == 0 || disabled {
		return "", ErrAuth
	}
	token := randomToken()
	_, e = s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, time.Now().UTC().Format(time.RFC3339))
	if e != nil {
		return "", e
	}
	_, e = s.db.ExecContext(ctx, `INSERT INTO sessions(token_hash,user_id,expires_at) VALUES(?,?,?)`, tokenHash(token), id, time.Now().Add(24*time.Hour).UTC().Format(time.RFC3339))
	return token, e
}
func (s *Service) Authenticate(ctx context.Context, token string) (Actor, error) {
	if strings.HasPrefix(token, "inv_") {
		return s.AuthenticateToken(ctx, token)
	}
	var a Actor
	e := s.db.QueryRowContext(ctx, `SELECT s.user_id FROM sessions s JOIN users u ON u.id=s.user_id WHERE token_hash=? AND expires_at>? AND u.disabled=0`, tokenHash(token), time.Now().UTC().Format(time.RFC3339)).Scan(&a.UserID)
	if e == sql.ErrNoRows {
		return a, ErrAuth
	}
	return a, e
}
func (s *Service) Logout(ctx context.Context, token string) error {
	_, e := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash=?`, tokenHash(token))
	return e
}
func (s *Service) Users(ctx context.Context, a Actor) ([]User, error) {
	if e := s.authorize(ctx, a, "admin"); e != nil {
		return nil, e
	}
	rows, e := s.db.QueryContext(ctx, `SELECT id,username,role,disabled,theme FROM users ORDER BY username`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		var u User
		if e = rows.Scan(&u.ID, &u.Username, &u.Role, &u.Disabled, &u.Theme); e != nil {
			return nil, e
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
func (s *Service) SaveUser(ctx context.Context, a Actor, u User, password string) (User, error) {
	if e := s.authorize(ctx, a, "admin"); e != nil {
		return u, e
	}
	u.Username = clean(u.Username)
	if e := validateUser(u.Username, password, u.Role, u.ID == 0); e != nil {
		return u, e
	}
	var hash []byte
	var e error
	if password != "" {
		hash, e = bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if e != nil {
			return u, e
		}
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return u, e
	}
	defer tx.Rollback()
	if u.ID == 0 {
		r, err := tx.ExecContext(ctx, `INSERT INTO users(username,password_hash,role,disabled,created_at) VALUES(?,?,?,?,?)`, u.Username, hash, u.Role, u.Disabled, now())
		e = err
		if e == nil {
			u.ID, e = r.LastInsertId()
		}
	} else {
		var oldRole string
		var oldDisabled bool
		e = tx.QueryRowContext(ctx, `SELECT role,disabled FROM users WHERE id=?`, u.ID).Scan(&oldRole, &oldDisabled)
		if e == sql.ErrNoRows {
			return u, ErrNotFound
		}
		if e != nil {
			return u, e
		}
		if oldRole == "admin" && !oldDisabled && (u.Role != "admin" || u.Disabled) {
			var n int
			if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE role='admin' AND disabled=0`).Scan(&n); e != nil {
				return u, e
			}
			if n <= 1 {
				return u, invalid("Cannot disable or demote the last active admin")
			}
		}
		_, e = tx.ExecContext(ctx, `UPDATE users SET username=?,role=?,disabled=? WHERE id=?`, u.Username, u.Role, u.Disabled, u.ID)
		if e == nil && password != "" {
			_, e = tx.ExecContext(ctx, `UPDATE users SET password_hash=? WHERE id=?`, hash, u.ID)
		}
		if e == nil {
			_, e = tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=?`, u.ID)
		}
	}
	if e != nil {
		if strings.Contains(e.Error(), "UNIQUE") {
			return u, invalid("Username already exists")
		}
		return u, e
	}
	if e = audit(ctx, tx, a, "save", "user", u.ID); e != nil {
		return u, e
	}
	if e = tx.QueryRowContext(ctx, `SELECT theme FROM users WHERE id=?`, u.ID).Scan(&u.Theme); e != nil {
		return u, e
	}
	return u, tx.Commit()
}
