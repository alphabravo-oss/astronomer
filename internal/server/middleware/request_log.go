package middleware

import (
	"bufio"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

type loggingResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (w *loggingResponseWriter) WriteHeader(statusCode int) {
	w.statusCode = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

// Unwrap exposes the underlying writer so upgrade paths can reach the real
// ResponseWriter interfaces.
func (w *loggingResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *loggingResponseWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *loggingResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("wrapped ResponseWriter does not implement http.Hijacker")
	}
	return hijacker.Hijack()
}

func (w *loggingResponseWriter) Push(target string, opts *http.PushOptions) error {
	pusher, ok := w.ResponseWriter.(http.Pusher)
	if !ok {
		return http.ErrNotSupported
	}
	return pusher.Push(target, opts)
}

func RequestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &loggingResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(rec, r)

		routeTemplate := "unknown"
		if ctx := chi.RouteContext(r.Context()); ctx != nil {
			if pattern := ctx.RoutePattern(); pattern != "" {
				routeTemplate = pattern
			}
		}

		observability.WithCorrelationID(
			observability.WithEvent(slog.Default(), "http_request"),
			GetCorrelationID(r.Context()),
		).Info("http request completed",
			"method", r.Method,
			"route_template", routeTemplate,
			"status_code", rec.statusCode,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	})
}
