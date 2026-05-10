package worker

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

// StartMetricsServer exposes Prometheus metrics for the worker process on a
// dedicated listener so scrape traffic stays off the task-processing path.
func StartMetricsServer(ctx context.Context, addr string, log *slog.Logger) error {
	if addr == "" {
		observability.WithEvent(log, "worker_metrics_listener_disabled").Info("worker metrics server disabled")
		return nil
	}

	srv := &http.Server{
		Addr:    addr,
		Handler: promhttp.Handler(),
	}

	go func() {
		<-ctx.Done()
		if err := srv.Shutdown(context.Background()); err != nil && !errors.Is(err, http.ErrServerClosed) {
			observability.WithEvent(log, "worker_metrics_listener_shutdown_error").Warn("worker metrics server shutdown failed", "error", err)
		}
	}()

	observability.WithEvent(log, "worker_metrics_listener_started").Info("starting worker metrics server", "addr", addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
