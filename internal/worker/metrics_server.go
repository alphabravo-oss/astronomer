package worker

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

// HealthPinger reports whether the worker's Redis dependency is reachable.
// *asynq.Client and *asynq.Server both satisfy it (Ping() error), so callers
// can pass whichever handle they already hold.
type HealthPinger interface {
	Ping() error
}

// newWorkerMux builds the worker's HTTP surface: Prometheus /metrics plus a
// /healthz liveness probe (C-02). /healthz pings Redis through the asynq handle;
// a queue consumer that can't reach Redis can neither process nor enqueue work,
// so an unreachable Redis is a real liveness failure worth restarting on.
func newWorkerMux(pinger HealthPinger, log *slog.Logger) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		if pinger != nil {
			if err := pinger.Ping(); err != nil {
				// Log the full error (which may embed the Redis host:port)
				// server-side, but return a generic body to the unauthenticated
				// probe so the dependency's address is not leaked to callers.
				observability.WithEvent(log, "worker_healthz_redis_unreachable").
					Warn("worker healthz redis ping failed", "error", err)
				http.Error(w, "redis unreachable", http.StatusServiceUnavailable)
				return
			}
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	return mux
}

// StartMetricsServer exposes Prometheus metrics and a /healthz probe for the
// worker process on a dedicated listener so scrape/probe traffic stays off the
// task-processing path.
func StartMetricsServer(ctx context.Context, addr string, pinger HealthPinger, log *slog.Logger) error {
	if addr == "" {
		observability.WithEvent(log, "worker_metrics_listener_disabled").Info("worker metrics server disabled")
		return nil
	}

	srv := &http.Server{
		Addr:    addr,
		Handler: newWorkerMux(pinger, log),
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
