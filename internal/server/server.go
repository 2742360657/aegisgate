// Package server owns the listener lifecycle, including bounded request draining.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// Serve returns only after graceful draining completes or its deadline expires.
// It does not cancel request contexts when the process shutdown signal arrives.
func Serve(ctx context.Context, listener net.Listener, srv *http.Server, grace time.Duration, logger *slog.Logger) error {
	result := make(chan error, 1)
	go func() { result <- srv.Serve(listener) }()
	select {
	case err := <-result:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		logger.Info("shutdown started", "grace", grace.String())
		drainCtx, cancel := context.WithTimeout(context.Background(), grace)
		defer cancel()
		if err := srv.Shutdown(drainCtx); err != nil {
			_ = srv.Close()
			<-result
			return fmt.Errorf("graceful shutdown: %w", err)
		}
		err := <-result
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		logger.Info("shutdown complete")
		return nil
	}
}
