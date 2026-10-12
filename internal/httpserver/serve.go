// Package httpserver owns finite HTTP serving and shutdown for internal probes.
package httpserver

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"
)

// Serve joins its shutdown owner before returning. A failed graceful drain
// closes remaining connections; a serving failure releases the owner without
// waiting for a cancellation that may never arrive. Callers bind the listener
// before invoking Serve so a bind failure cannot create a shutdown waiter.
func Serve(ctx context.Context, srv *http.Server, listener net.Listener, timeout time.Duration, onShutdownError func(error)) error {
	serveDone := make(chan struct{})
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			if err := srv.Shutdown(shutdownCtx); err != nil {
				if onShutdownError != nil {
					onShutdownError(err)
				}
				_ = srv.Close()
			}
		case <-serveDone:
		}
	}()
	err := srv.Serve(listener)
	close(serveDone)
	<-shutdownDone
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
