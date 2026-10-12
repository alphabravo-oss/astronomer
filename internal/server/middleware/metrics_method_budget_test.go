package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

func TestMetricsBoundsUnrecognizedRequestMethodsWithoutChangingRequests(t *testing.T) {
	previousInstance := observability.InstanceID()
	observability.SetInstanceID("test-http-method-budget")
	t.Cleanup(func() { observability.SetInstanceID(previousInstance) })
	var received []string
	handler := Metrics(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = append(received, r.Method)
		w.WriteHeader(http.StatusNoContent)
	}))
	methods := []string{"SYNTHETIC_PROBE_ONE", "SYNTHETIC_PROBE_TWO", "SYNTHETIC_PROBE_THREE"}
	for _, method := range methods {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(method, "/unmatched/synthetic-resource", nil))
		if rec.Code != http.StatusNoContent {
			t.Fatalf("method %q changed response status", method)
		}
	}
	for i, method := range methods {
		if received[i] != method {
			t.Fatal("metrics changed the request method delivered to the handler")
		}
	}
	if got := metricValue(t, httpRequestsTotal.WithLabelValues(observability.MetricValues("OTHER", "unmatched", "2xx")...)); got != float64(len(methods)) {
		t.Fatalf("unrecognized-method bucket = %v, want %d", got, len(methods))
	}
}
