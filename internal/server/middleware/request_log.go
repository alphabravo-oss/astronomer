package middleware

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
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

type requestLogFieldsKey struct{}

type requestLogFields struct {
	actorID         string
	actorAuthMethod string
}

func setRequestLogActor(ctx context.Context, user *AuthenticatedUser) {
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

func RequestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		fields := &requestLogFields{}
		r = r.WithContext(context.WithValue(r.Context(), requestLogFieldsKey{}, fields))
		rec := &loggingResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(rec, r)

		routeTemplate := "unknown"
		if ctx := chi.RouteContext(r.Context()); ctx != nil {
			if pattern := ctx.RoutePattern(); pattern != "" {
				routeTemplate = pattern
			}
		}

		requestID := GetRequestID(r.Context())
		log := observability.WithTraceID(
			observability.WithRequestID(
				observability.WithCorrelationID(
					observability.WithEvent(slog.Default(), "http_request"),
					GetCorrelationID(r.Context()),
				),
				requestID,
			),
			r.Context(),
		)
		log = observability.WithActorAuthMethod(observability.WithActorID(log, fields.actorID), fields.actorAuthMethod)
		log = observability.WithOperationID(observability.WithClusterID(log, requestLogClusterID(r, routeTemplate)), requestLogOperationID(r, routeTemplate))
		log.Info("http request completed",
			"method", r.Method,
			"route_template", routeTemplate,
			"status_code", rec.statusCode,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	})
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
		routeTemplateHas(routeTemplate, "/fleet-operations/{id}") ||
		routeTemplateHas(routeTemplate, "/deferred-operations/{id}") {
		return strings.TrimSpace(chi.URLParam(r, "id"))
	}
	return ""
}

func routeTemplateHas(routeTemplate, segment string) bool {
	return strings.Contains(routeTemplate, segment)
}
