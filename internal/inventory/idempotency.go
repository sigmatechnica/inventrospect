package inventory

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const IdempotencyRetention = 7 * 24 * time.Hour

func requestKey(keys []string) string {
	if len(keys) > 0 {
		return keys[0]
	}
	return ""
}

// Mutation and replay response share the same SQLite transaction. The timestamp
// in the key makes expired retries fail closed even after their rows are purged.
func mutate[T any](s *Service, ctx context.Context, a Actor, operation, key string, input any, fn func(*sql.Tx) (T, error)) (T, error) {
	var zero T
	principal := fmt.Sprintf("user:%d/token:%d", a.UserID, a.TokenID)
	var hash string
	var storedBytes int64
	cutoff := time.Now().Add(-IdempotencyRetention).Unix()
	if key != "" {
		parts := strings.SplitN(key, ":", 2)
		if len(parts) != 2 || len(parts[1]) < 8 || len(key) > 128 {
			return zero, invalid("client_request_id must be unix-seconds:unique-suffix (8+ suffix characters, 128 max total)")
		}
		stamp, e := strconv.ParseInt(parts[0], 10, 64)
		if e != nil {
			return zero, invalid("Invalid client_request_id timestamp")
		}
		if stamp <= cutoff {
			return zero, &Error{Code: "idempotency_expired", Message: "Request key expired (7 days); inspect state before issuing a new key"}
		}
		if stamp > time.Now().Add(5*time.Minute).Unix() {
			return zero, invalid("Request key timestamp is in the future")
		}
		raw, e := json.Marshal(input)
		if e != nil {
			return zero, invalid("Invalid mutation arguments")
		}
		sum := sha256.Sum256(raw)
		hash = hex.EncodeToString(sum[:])
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return zero, e
	}
	defer tx.Rollback()
	if key != "" {
		var savedHash, result string
		e = tx.QueryRowContext(ctx, `SELECT request_hash,result FROM idempotency WHERE principal=? AND operation=? AND request_id=?`, principal, operation, key).Scan(&savedHash, &result)
		if e == nil {
			if hash != savedHash {
				return zero, &Error{Code: "idempotency_conflict", Message: "Request key was already used with different arguments"}
			}
			decoder := json.NewDecoder(strings.NewReader(result))
			decoder.UseNumber() // Keep IDs in bulk interface values exact on replay.
			e = decoder.Decode(&zero)
			return zero, e
		}
		if e != sql.ErrNoRows {
			return zero, e
		}
		if _, e = tx.ExecContext(ctx, `DELETE FROM idempotency WHERE created_at<=?`, cutoff); e != nil {
			return zero, e
		}
		var count int64
		if e = tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(length(CAST(result AS BLOB))),0) FROM idempotency`).Scan(&count, &storedBytes); e != nil {
			return zero, e
		}
		if count >= 10000 || storedBytes >= 64*1024*1024 {
			return zero, &Error{Code: "rate_limited", Message: "Idempotency storage capacity reached; retry later with the same key"}
		}
	}
	out, e := fn(tx)
	if e != nil {
		return zero, e
	}
	if key != "" {
		raw, e := json.Marshal(out)
		if e != nil {
			return zero, e
		}
		if len(raw) > 1024*1024 {
			return zero, invalid("Idempotent result exceeds 1 MiB; use a smaller batch")
		}
		if storedBytes+int64(len(raw)) > 64*1024*1024 {
			return zero, &Error{Code: "rate_limited", Message: "Idempotency storage capacity reached; retry later with the same key"}
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO idempotency(principal,operation,request_id,request_hash,result,created_at) VALUES(?,?,?,?,?,?)`, principal, operation, key, hash, string(raw), time.Now().Unix()); e != nil {
			return zero, e
		}
	}
	if e = tx.Commit(); e != nil {
		return zero, e
	}
	return out, nil
}
