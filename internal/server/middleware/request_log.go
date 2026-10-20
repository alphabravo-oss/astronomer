package middleware

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
	"github.com/alphabravocompany/astronomer-go/pkg/logging"
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

type requestLogFieldsKey struct{}

type requestLogFields struct {
	actorID         string
	actorAuthMethod string
}

func setRequestLogActor(ctx context.Context, user *reqctx.User) {
	fields, ok := ctx.Value(requestLogFieldsKey{}).(*requestLogFields)
	if !ok || fields == nil || user == nil {
		return
	}
	fields.actorID = strings.TrimSpace(user.ID)
	fields.actorAuthMethod = strings.TrimSpace(user.AuthMethod)
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

// RequestLogger emits exactly one request-summary line per request, at
// info, after the response: route (chi pattern), method, status,
// duration_ms, correlation_id, trace_id and principal (the authenticated
// user id). Paths, query strings, headers and bodies are never logged.
func RequestLogger(base zerolog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			fields := &requestLogFields{}
			r = r.WithContext(context.WithValue(r.Context(), requestLogFieldsKey{}, fields))
			rec := &loggingResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
			next.ServeHTTP(rec, r)

			route := "unknown"
			if ctx := chi.RouteContext(r.Context()); ctx != nil {
				if pattern := ctx.RoutePattern(); pattern != "" {
					route = pattern
				}
			}
			event := logging.Ctx(r.Context(), base).Info().
				Str("event", "http_request").
				Str("route", route).
				Str("method", r.Method).
				Int("status", rec.statusCode).
				Int64("duration_ms", time.Since(start).Milliseconds())
			if fields.actorID != "" {
				event = event.Str("principal", fields.actorID).Str("auth_method", fields.actorAuthMethod)
			}
			if clusterID := requestLogClusterID(r, route); clusterID != "" {
				event = event.Str("cluster_id", clusterID)
			}
			if operationID := requestLogOperationID(r, route); operationID != "" {
				event = event.Str("operation_id", operationID)
			}
			event.Msg("http request completed")
		})
	}
}

func requestLogClusterID(r *http.Request, routeTemplate string) string {
	if r == nil {
		return ""
	}
	if clusterID := strings.TrimSpace(chi.URLParam(r, "cluster_id")); clusterID != "" {
		return clusterID
	}
	if routeTemplateHas(routeTemplate, "/clusters/{id}") || routeTemplateHas(routeTemplate, "/dashboards/clusters/{id}") {
		return strings.TrimSpace(chi.URLParam(r, "id"))
	}
	return ""
}

func requestLogOperationID(r *http.Request, routeTemplate string) string {
	if r == nil {
		return ""
	}
	if operationID := strings.TrimSpace(chi.URLParam(r, "operation_id")); operationID != "" {
		return operationID
	}
	if routeTemplateHas(routeTemplate, "/operations/{id}") ||
		routeTemplateHas(routeTemplate, "/deferred-operations/{id}") {
		return strings.TrimSpace(chi.URLParam(r, "id"))
	}
	return ""
}

func routeTemplateHas(routeTemplate, segment string) bool {
	return strings.Contains(routeTemplate, segment)
}
