package inventory

import (
	"context"
	"database/sql"
	"time"
)

type writeWindow struct {
	Started time.Time
	Count   int
}
type prepaidKey struct{}
type prepaidBudget struct{ used int }
type queryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// Process settings are optional ceilings; individual token settings are stored in SQLite.
func (s *Service) ConfigureLimits(bulk, perMinute int) error {
	if bulk < 1 || bulk > 100 || perMinute < 0 {
		return invalid("bulk limit must be 1–100; agent write ceiling must be nonnegative")
	}
	s.limitsMu.Lock()
	defer s.limitsMu.Unlock()
	s.maxBulk = bulk
	s.writesPerMinute = perMinute
	return nil
}
func (s *Service) checkBulk(ctx context.Context, a Actor, n int) error {
	s.limitsMu.Lock()
	limit := s.maxBulk
	s.limitsMu.Unlock()
	if a.TokenID != 0 {
		var tokenLimit int
		if err := s.db.QueryRowContext(ctx, `SELECT bulk_record_limit FROM api_tokens WHERE id=?`, a.TokenID).Scan(&tokenLimit); err != nil {
			return err
		}
		if tokenLimit < limit {
			limit = tokenLimit
		}
	}
	if n < 1 || n > limit {
		return invalid("A bulk request must contain 1–%d records", limit)
	}
	return nil
}

// Reserve before commits. release refunds validation failures/replays; a batch
// reserves its entire potential cost so exhaustion never splits a batch.
func (s *Service) reserveWrites(ctx context.Context, q queryRower, a Actor, units int) (func(int), error) {
	noop := func(int) {}
	if a.TokenID == 0 || units == 0 || ctx.Value(prepaidKey{}) != nil {
		return noop, nil
	}
	var limit int
	if err := q.QueryRowContext(ctx, `SELECT write_units_per_minute FROM api_tokens WHERE id=?`, a.TokenID).Scan(&limit); err != nil {
		return nil, err
	}
	s.limitsMu.Lock()
	defer s.limitsMu.Unlock()
	if s.writesPerMinute > 0 && s.writesPerMinute < limit {
		limit = s.writesPerMinute
	}
	now := time.Now()
	for id, w := range s.writeWindows {
		if now.Sub(w.Started) >= time.Minute {
			delete(s.writeWindows, id)
		}
	}
	w := s.writeWindows[a.TokenID]
	if w.Started.IsZero() {
		w.Started = now
	}
	if limit == 0 || units > limit-w.Count {
		return nil, &Error{Code: "rate_limited", Message: "Token write budget exceeded or writes paused", Details: map[string]any{"retry_after_seconds": int(time.Until(w.Started.Add(time.Minute)).Seconds()) + 1}}
	}
	w.Count += units
	s.writeWindows[a.TokenID] = w
	return func(used int) {
		s.limitsMu.Lock()
		defer s.limitsMu.Unlock()
		current := s.writeWindows[a.TokenID]
		if current.Started == w.Started {
			current.Count -= units - used
			s.writeWindows[a.TokenID] = current
		}
	}, nil
}
