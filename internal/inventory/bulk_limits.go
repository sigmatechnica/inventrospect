package inventory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Only an exact, unexpired stored request may bypass a bulk reservation.
func (s *Service) isReplay(ctx context.Context, a Actor, op, key string, input any) bool {
	parts := strings.SplitN(key, ":", 2)
	if len(parts) != 2 {
		return false
	}
	stamp, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || stamp <= time.Now().Add(-IdempotencyRetention).Unix() {
		return false
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return false
	}
	sum := sha256.Sum256(raw)
	var hash string
	err = s.db.QueryRowContext(ctx, `SELECT request_hash FROM idempotency WHERE principal=? AND operation=? AND request_id=?`, fmt.Sprintf("user:%d/token:%d", a.UserID, a.TokenID), op, key).Scan(&hash)
	return err == nil && hash == hex.EncodeToString(sum[:])
}
func (s *Service) limitedBulk(ctx context.Context, a Actor, n int, replays []bool, fn func(context.Context, int) (any, error)) (BulkResponse, error) {
	units := n
	for _, r := range replays {
		if r {
			units--
		}
	}
	release, err := s.reserveWrites(ctx, s.db, a, units)
	if err != nil {
		return BulkResponse{}, err
	}
	budget := &prepaidBudget{}
	defer func() { release(budget.used) }()
	prepaid := context.WithValue(ctx, prepaidKey{}, budget)
	return runBulk(n, func(j int) (any, error) {
		out, err := fn(prepaid, j)
		return out, err
	})
}

// A lower configured cap must not prevent returning an already committed result.
func (s *Service) checkReplayBulk(ctx context.Context, a Actor, n int, operation, key string, input any) error {
	if n >= 1 && n <= 100 && key != "" && s.isReplay(ctx, a, operation, key, input) {
		return nil
	}
	return s.checkBulk(ctx, a, n)
}
