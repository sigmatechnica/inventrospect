package inventory

import (
	"context"
	"log"
	"time"
)

func (s *Service) pruneIdempotency(ctx context.Context) error {
	// created_at is never earlier than an accepted key by more than five minutes.
	// Retain that allowance so pruning cannot open a replay gap for future-stamped keys.
	_, err := s.db.ExecContext(ctx, `DELETE FROM idempotency WHERE created_at<=?`, time.Now().Add(-IdempotencyRetention-5*time.Minute).Unix())
	return err
}
func (s *Service) startMaintenance() {
	ctx, cancel := context.WithCancel(context.Background())
	s.maintenanceCancel = cancel
	s.maintenanceDone = make(chan struct{})
	if err := s.pruneIdempotency(ctx); err != nil {
		log.Printf("Idempotency cleanup: %v", err)
	}
	go func() {
		defer close(s.maintenanceDone)
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := s.cleanupFiles(ctx); err != nil && ctx.Err() == nil {
					log.Printf("Document cleanup pending: %v", err)
				}
				if err := s.pruneIdempotency(ctx); err != nil && ctx.Err() == nil {
					log.Printf("Idempotency cleanup: %v", err)
				}
			}
		}
	}()
}
