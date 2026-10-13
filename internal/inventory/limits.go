package inventory

import (
	"time"
)

type writeWindow struct {
	Started time.Time
	Count   int
}

// ConfigureLimits is intended for startup configuration. Bulk sizes can only be
// reduced below the hard ceiling of 100. Zero disables the per-token rate cap.
func (s *Service) ConfigureLimits(bulk, perMinute int) error {
	if bulk < 1 || bulk > 100 || perMinute < 0 {
		return invalid("bulk limit must be 1–100; agent writes/minute must be nonnegative")
	}
	s.limitsMu.Lock()
	defer s.limitsMu.Unlock()
	s.maxBulk = bulk
	s.writesPerMinute = perMinute
	return nil
}
func (s *Service) checkBulk(n int) error {
	s.limitsMu.Lock()
	limit := s.maxBulk
	s.limitsMu.Unlock()
	if n < 1 || n > limit {
		return invalid("A bulk request must contain 1–%d records", limit)
	}
	return nil
}

// AllowAgentWrite is called by the shared authenticated HTTP boundary, so stdio
// MCP, Streamable HTTP MCP and direct REST share one token's budget.
func (s *Service) AllowAgentWrite(a Actor) error {
	if a.TokenID == 0 {
		return nil
	}
	s.limitsMu.Lock()
	defer s.limitsMu.Unlock()
	if s.writesPerMinute == 0 {
		return nil
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
	if w.Count >= s.writesPerMinute {
		return &Error{Code: "rate_limited", Message: "Agent write rate limit exceeded; retry after the current minute using the same idempotency key", Details: map[string]any{"retry_after_seconds": int(time.Until(w.Started.Add(time.Minute)).Seconds()) + 1}}
	}
	w.Count++
	s.writeWindows[a.TokenID] = w
	return nil
}
