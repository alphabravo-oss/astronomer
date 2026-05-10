package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
	appmiddleware "github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

// StartMetricsServer exposes Prometheus metrics on a dedicated listener so
// scrape traffic is isolated from the public API surface.
func StartMetricsServer(ctx context.Context, addr string, log *slog.Logger) error {
	if addr == "" {
		observability.WithEvent(log, "server_metrics_listener_disabled").Info("server metrics listener disabled")
		return nil
	}

	srv := &http.Server{
		Addr:    addr,
		Handler: appmiddleware.MetricsHandler(),
	}

	go func() {
		<-ctx.Done()
		if err := srv.Shutdown(context.Background()); err != nil && !errors.Is(err, http.ErrServerClosed) {
			observability.WithEvent(log, "server_metrics_listener_shutdown_error").Warn("server metrics listener shutdown failed", "error", err)
		}
	}()

	observability.WithEvent(log, "server_metrics_listener_started").Info("starting server metrics listener", "addr", addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
