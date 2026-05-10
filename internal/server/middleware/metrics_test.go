package middleware

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	dto "github.com/prometheus/client_model/go"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

type hijackableRecorder struct {
	*httptest.ResponseRecorder
}

func (w *hijackableRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	serverConn, clientConn := net.Pipe()
	rw := bufio.NewReadWriter(bufio.NewReader(serverConn), bufio.NewWriter(serverConn))
	return clientConn, rw, nil
}

func (w *hijackableRecorder) Flush() {}

func TestMetricsPreservesHijacker(t *testing.T) {
	handler := Metrics(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Fatalf("wrapped response writer does not implement http.Hijacker")
		}
		conn, _, err := hijacker.Hijack()
		if err != nil {
			t.Fatalf("Hijack failed: %v", err)
		}
		_ = conn.Close()
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/ws/agent/tunnel/test/", nil)
	rec := &hijackableRecorder{ResponseRecorder: httptest.NewRecorder()}
	handler.ServeHTTP(rec, req)
}

func TestStatusClass(t *testing.T) {
	tests := map[int]string{
		101: "1xx",
		204: "2xx",
		302: "3xx",
		404: "4xx",
		503: "5xx",
		0:   "unknown",
	}
	for code, want := range tests {
		if got := statusClass(code); got != want {
			t.Fatalf("statusClass(%d) = %q, want %q", code, got, want)
		}
	}
}

func TestMetricsUsesRouteTemplateAndStatusClass(t *testing.T) {
	httpRequestsTotal.Reset()
	httpRequestDuration.Reset()
	httpInflightRequests.Reset()

	router := chi.NewRouter()
	router.Use(Metrics)
	router.Get("/api/v1/items/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/items/123", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if got := metricValue(t, httpRequestsTotal.WithLabelValues(observability.MetricValues(http.MethodGet, "/api/v1/items/{id}", "2xx")...)); got != 1 {
		t.Fatalf("requests_total = %v, want 1", got)
	}
	if got := metricValue(t, httpInflightRequests.WithLabelValues(observability.MetricValues(http.MethodGet, "/api/v1/items/{id}")...)); got != 0 {
		t.Fatalf("inflight gauge after request = %v, want 0", got)
	}
}

func metricValue(t *testing.T, collector interface{ Write(*dto.Metric) error }) float64 {
	t.Helper()
	m := &dto.Metric{}
	if err := collector.Write(m); err != nil {
		t.Fatalf("collector.Write(): %v", err)
	}
	switch {
	case m.Counter != nil:
		return m.Counter.GetValue()
	case m.Gauge != nil:
		return m.Gauge.GetValue()
	default:
		t.Fatal("unsupported metric type")
		return 0
	}
}
