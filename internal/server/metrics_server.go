package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/rs/zerolog"
)

// StartMetricsServer serves the telemetry.Init /metrics handler (legacy
// client_golang collectors and OTel instruments on one registry) on a
// dedicated listener so scrape traffic is isolated from the public API.
func StartMetricsServer(ctx context.Context, addr string, metrics http.Handler, log zerolog.Logger) error {
	if addr == "" {
		log.Info().Str("event", "server_metrics_listener_disabled").Msg("server metrics listener disabled")
		return nil
	}

	srv := &http.Server{
		Addr:              addr,
		Handler:           metrics,
		ReadHeaderTimeout: 15 * time.Second,
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	log.Info().Str("event", "server_metrics_listener_started").Str("addr", addr).Msg("starting server metrics listener")
	result := make(chan error, 1)
	go func() { result <- srv.Serve(ln) }()
	select {
	case err := <-result:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		if err := srv.Shutdown(context.Background()); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Warn().Str("event", "server_metrics_listener_shutdown_error").Err(err).Msg("server metrics listener shutdown failed")
		}
		err := <-result
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}
