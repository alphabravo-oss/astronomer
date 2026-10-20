package observability

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// requestErrorsTotal is the guide ch.05 "Errors" golden signal:
// request_errors_total{route,reason} with a bounded reason vocabulary derived
// from the HTTP status class, never from free-form error strings.
var requestErrorsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Namespace: "astronomer",
	Name:      "request_errors_total",
	Help:      "Error responses written by the API, by chi route pattern and bounded reason.",
}, []string{"route", "reason"})

// RecordRequestError increments request_errors_total for a non-2xx response.
// route should be the chi route pattern ("" is recorded as "unknown").
func RecordRequestError(route string, status int) {
	if status < http.StatusBadRequest {
		return
	}
	if route == "" {
		route = "unknown"
	}
	requestErrorsTotal.WithLabelValues(route, ErrorReason(status)).Inc()
}

// ErrorReason maps an HTTP status to the bounded reason vocabulary.
func ErrorReason(status int) string {
	switch status {
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return "validation"
	case http.StatusUnauthorized:
		return "unauthenticated"
	case http.StatusForbidden:
		return "permission_denied"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusConflict:
		return "conflict"
	case http.StatusTooManyRequests:
		return "rate_limited"
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return "timeout"
	case http.StatusBadGateway, http.StatusServiceUnavailable:
		return "upstream_unavailable"
	}
	if status >= http.StatusInternalServerError {
		return "internal"
	}
	return "client_error"
}

// RoutePattern returns the chi route pattern for r, or "unmatched" so raw
// URLs never become label values.
func RoutePattern(r *http.Request) string {
	if r == nil {
		return "unknown"
	}
	if ctx := chi.RouteContext(r.Context()); ctx != nil {
		if pattern := ctx.RoutePattern(); pattern != "" {
			return pattern
		}
	}
	return "unmatched"
}
