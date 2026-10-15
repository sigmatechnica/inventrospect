// Package serverlife owns HTTP draining so callers close services only afterwards.
package serverlife

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"
)

// Run waits for Shutdown, not merely ListenAndServe. On timeout Close cancels
// active connections; the caller should terminate the process with a failure.
func Run(ctx context.Context, srv *http.Server, timeout time.Duration, serve func() error, draining ...func()) error {
	if timeout <= 0 {
		return errors.New("shutdown timeout must be positive")
	}
	served := make(chan error, 1)
	go func() { served <- serve() }()
	select {
	case err := <-served:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		log.Print("shutdown draining HTTP requests")
		for _, fn := range draining {
			fn()
		}
		drain, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		if err := srv.Shutdown(drain); err != nil {
			srv.Close()
			return fmt.Errorf("HTTP shutdown failed: %w", err)
		}
		err := <-served
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
