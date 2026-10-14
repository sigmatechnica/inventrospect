package inventory

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"
)

var TokenScopes = []string{"inventory:read", "inventory:write", "categories:write", "locations:write", "inventory:delete", "stock:read", "stock:write", "documents:read", "documents:write"}

type TokenLimits struct {
	WriteUnitsPerMinute int `json:"write_units_per_minute"`
	BulkRecordLimit     int `json:"bulk_record_limit"`
}

func defaultTokenLimits() TokenLimits { return TokenLimits{600, 100} }
func (l TokenLimits) validate() error {
	if l.WriteUnitsPerMinute < 0 || l.WriteUnitsPerMinute > 1000000 || l.BulkRecordLimit < 1 || l.BulkRecordLimit > 100 {
		return invalid("Write units must be 0–1000000 (0 pauses writes); bulk limit must be 1–100")
	}
	return nil
}

type APIToken struct {
	TokenLimits
	ID         int64    `json:"id"`
	Name       string   `json:"name"`
	Prefix     string   `json:"prefix"`
	UserID     int64    `json:"user_id"`
	Scopes     []string `json:"scopes"`
	CreatedAt  string   `json:"created_at"`
	ExpiresAt  *string  `json:"expires_at"`
	RevokedAt  *string  `json:"revoked_at"`
	LastUsedAt *string  `json:"last_used_at"`
}
type TokenInput struct {
	Limits    *TokenLimits `json:"limits,omitempty"`
	Name      string       `json:"name"`
	UserID    int64        `json:"user_id"`
	Scopes    []string     `json:"scopes"`
	ExpiresAt *string      `json:"expires_at,omitempty"`
}
type TokenCreated struct {
	Token  APIToken `json:"token"`
	Secret string   `json:"secret"`
}

func (s *Service) CreateToken(ctx context.Context, a Actor, in TokenInput) (TokenCreated, error) {
	if a.TokenID != 0 {
		return TokenCreated{}, ErrForbidden
	}
	if e := s.authorize(ctx, a, "admin"); e != nil {
		return TokenCreated{}, e
	}
	limits := defaultTokenLimits()
	if in.Limits != nil {
		limits = *in.Limits
	}
	if e := limits.validate(); e != nil {
		return TokenCreated{}, e
	}
	in.Name = clean(in.Name)
	if in.Name == "" || len(in.Name) > 100 {
		return TokenCreated{}, invalid("Token name must be 1–100 characters")
	}
	seen := map[string]bool{}
	scopes := []string{}
	for _, scope := range in.Scopes {
		valid := false
		for _, allowed := range TokenScopes {
			if scope == allowed {
				valid = true
			}
		}
		if !valid {
			return TokenCreated{}, invalid("Unknown token scope: %s", scope)
		}
		if !seen[scope] {
			scopes = append(scopes, scope)
			seen[scope] = true
		}
	}
	if len(scopes) == 0 {
		return TokenCreated{}, invalid("Select at least one scope")
	}
	if in.ExpiresAt != nil {
		t, e := time.Parse(time.RFC3339, *in.ExpiresAt)
		if e != nil || !t.After(time.Now()) {
			return TokenCreated{}, invalid("Expiry must be a future RFC3339 timestamp")
		}
		v := t.UTC().Format(time.RFC3339)
		in.ExpiresAt = &v
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return TokenCreated{}, e
	}
	defer tx.Rollback()
	var role string
	var disabled bool
	e = tx.QueryRowContext(ctx, `SELECT role,disabled FROM users WHERE id=?`, in.UserID).Scan(&role, &disabled)
	if e == sql.ErrNoRows || disabled {
		return TokenCreated{}, invalid("Choose an active token owner")
	}
	if e != nil {
		return TokenCreated{}, e
	}
	for _, scope := range scopes {
		if scope == "inventory:delete" && role != "admin" {
			return TokenCreated{}, invalid("Permanent deletion requires an admin token owner")
		}
		if role == "viewer" && (strings.HasSuffix(scope, ":write") || scope == "inventory:delete") {
			return TokenCreated{}, invalid("Viewer-owned tokens cannot have write scopes")
		}
	}
	secret := "inv_" + randomToken()
	t := APIToken{TokenLimits: limits, Name: in.Name, UserID: in.UserID, Prefix: secret[:12], Scopes: scopes, CreatedAt: now(), ExpiresAt: in.ExpiresAt}
	raw, _ := json.Marshal(scopes)
	r, e := tx.ExecContext(ctx, `INSERT INTO api_tokens(name,prefix,token_hash,user_id,scopes,created_at,expires_at,write_units_per_minute,bulk_record_limit) VALUES(?,?,?,?,?,?,?,?,?)`, t.Name, t.Prefix, tokenHash(secret), t.UserID, string(raw), t.CreatedAt, t.ExpiresAt, limits.WriteUnitsPerMinute, limits.BulkRecordLimit)
	if e != nil {
		return TokenCreated{}, e
	}
	t.ID, e = r.LastInsertId()
	if e != nil {
		return TokenCreated{}, e
	}
	if e = audit(ctx, tx, a, "create", "api_token", t.ID); e != nil {
		return TokenCreated{}, e
	}
	if e = tx.Commit(); e != nil {
		return TokenCreated{}, e
	}
	return TokenCreated{Token: t, Secret: secret}, nil
}
func (s *Service) Tokens(ctx context.Context, a Actor) ([]APIToken, error) {
	if a.TokenID != 0 {
		return nil, ErrForbidden
	}
	if e := s.authorize(ctx, a, "admin"); e != nil {
		return nil, e
	}
	rows, e := s.db.QueryContext(ctx, `SELECT id,name,prefix,user_id,scopes,created_at,expires_at,revoked_at,last_used_at,write_units_per_minute,bulk_record_limit FROM api_tokens ORDER BY id DESC`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []APIToken{}
	for rows.Next() {
		var t APIToken
		var raw string
		if e = rows.Scan(&t.ID, &t.Name, &t.Prefix, &t.UserID, &raw, &t.CreatedAt, &t.ExpiresAt, &t.RevokedAt, &t.LastUsedAt, &t.WriteUnitsPerMinute, &t.BulkRecordLimit); e != nil {
			return nil, e
		}
		if e = json.Unmarshal([]byte(raw), &t.Scopes); e != nil {
			return nil, e
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
func (s *Service) RevokeToken(ctx context.Context, a Actor, id int64) error {
	if a.TokenID != 0 {
		return ErrForbidden
	}
	if e := s.authorize(ctx, a, "admin"); e != nil {
		return e
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	r, e := tx.ExecContext(ctx, `UPDATE api_tokens SET revoked_at=COALESCE(revoked_at,?) WHERE id=?`, now(), id)
	if e != nil {
		return e
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	if e = audit(ctx, tx, a, "revoke", "api_token", id); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Service) AuthenticateToken(ctx context.Context, secret string) (Actor, error) {
	var a Actor
	var raw string
	if !strings.HasPrefix(secret, "inv_") || len(secret) != 68 {
		return a, ErrAuth
	}
	e := s.db.QueryRowContext(ctx, `SELECT t.id,t.user_id,t.scopes FROM api_tokens t JOIN users u ON u.id=t.user_id WHERE token_hash=? AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at>?) AND u.disabled=0`, tokenHash(secret), time.Now().UTC().Format(time.RFC3339)).Scan(&a.TokenID, &a.UserID, &raw)
	if e == sql.ErrNoRows {
		return a, ErrAuth
	}
	if e != nil {
		return a, e
	}
	if e = json.Unmarshal([]byte(raw), &a.Scopes); e != nil {
		return a, e
	}
	_, e = s.db.ExecContext(ctx, `UPDATE api_tokens SET last_used_at=? WHERE id=?`, now(), a.TokenID)
	return a, e
}
func tokenID(a Actor) any {
	if a.TokenID == 0 {
		return nil
	}
	return a.TokenID
}

func (s *Service) UpdateTokenLimits(ctx context.Context, a Actor, id int64, limits TokenLimits) error {
	if a.TokenID != 0 {
		return ErrForbidden
	}
	if e := s.authorize(ctx, a, "admin"); e != nil {
		return e
	}
	if e := limits.validate(); e != nil {
		return e
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	r, e := tx.ExecContext(ctx, `UPDATE api_tokens SET write_units_per_minute=?,bulk_record_limit=? WHERE id=?`, limits.WriteUnitsPerMinute, limits.BulkRecordLimit, id)
	if e != nil {
		return e
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	if e = audit(ctx, tx, a, "limits", "api_token", id); e != nil {
		return e
	}
	return tx.Commit()
}
