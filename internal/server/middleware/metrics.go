package middleware

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

const metricsNamespace = "astronomer"

var (
	registerMetricsOnce sync.Once

	httpRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Name:      "http_requests_total",
			Help:      "Total number of HTTP requests handled by the Astronomer control plane.",
		},
		observability.MetricLabels("method", "route_template", "status_class"),
	)
	httpRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: metricsNamespace,
			Name:      "http_request_duration_seconds",
			Help:      "Latency of HTTP requests handled by the Astronomer control plane.",
			Buckets:   prometheus.DefBuckets,
		},
		observability.MetricLabels("method", "route_template", "status_class"),
	)
	httpInflightRequests = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: metricsNamespace,
			Name:      "http_in_flight_requests",
			Help:      "Current number of in-flight HTTP requests handled by the Astronomer control plane.",
		},
		observability.MetricLabels("method", "route_template"),
	)
)

func registerMetrics() {
	registerMetricsOnce.Do(func() {
		prometheus.MustRegister(
			httpRequestsTotal,
			httpRequestDuration,
			httpInflightRequests,
		)
	})
}

// MetricsHandler exposes the process and application Prometheus scrape
// endpoint. Global collectors are registered once to keep repeated test/router
// construction safe.
func MetricsHandler() http.Handler {
	registerMetrics()
	return promhttp.Handler()
}

// Metrics instruments HTTP requests with bounded-cardinality labels derived
// from the method, chi route pattern, and response status code.
func Metrics(next http.Handler) http.Handler {
	registerMetrics()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route := routePattern(r)
		httpInflightRequests.WithLabelValues(observability.MetricValues(r.Method, route)...).Inc()
		defer httpInflightRequests.WithLabelValues(observability.MetricValues(r.Method, route)...).Dec()

		rec := &metricsResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(rec, r)

		route = routePattern(r)
		statusClass := statusClass(rec.statusCode)
		httpRequestsTotal.WithLabelValues(observability.MetricValues(r.Method, route, statusClass)...).Inc()
		httpRequestDuration.WithLabelValues(observability.MetricValues(r.Method, route, statusClass)...).Observe(time.Since(start).Seconds())
	})
}

func statusClass(statusCode int) string {
	switch {
	case statusCode >= 100 && statusCode < 200:
		return "1xx"
	case statusCode >= 200 && statusCode < 300:
		return "2xx"
	case statusCode >= 300 && statusCode < 400:
		return "3xx"
	case statusCode >= 400 && statusCode < 500:
		return "4xx"
	case statusCode >= 500 && statusCode < 600:
		return "5xx"
	default:
		return "unknown"
	}
}

func routePattern(r *http.Request) string {
	if r == nil {
		return "unknown"
	}
	if ctx := chi.RouteContext(r.Context()); ctx != nil {
		if pattern := ctx.RoutePattern(); pattern != "" {
			return pattern
		}
	}
	if r.URL != nil && r.URL.Path != "" {
		return r.URL.Path
	}
	return "unknown"
}

type metricsResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (w *metricsResponseWriter) WriteHeader(statusCode int) {
	w.statusCode = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

// Unwrap exposes the underlying ResponseWriter so helpers like
// http.ResponseController and upgrade paths can reach the original writer.
func (w *metricsResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *metricsResponseWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *metricsResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("wrapped ResponseWriter does not implement http.Hijacker")
	}
	return hijacker.Hijack()
}

func (w *metricsResponseWriter) Push(target string, opts *http.PushOptions) error {
	pusher, ok := w.ResponseWriter.(http.Pusher)
	if !ok {
		return http.ErrNotSupported
	}
	return pusher.Push(target, opts)
}
