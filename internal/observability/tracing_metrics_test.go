package observability

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestExporterMetricsDistinguishActualWireSuccessAndFailure(t *testing.T) {
	metrics := newTracingMetricsCollector()
	metrics.enabled.Store(true)
	registry := prometheus.NewRegistry()
	registry.MustRegister(metrics)
	for _, code := range []int{http.StatusOK, http.StatusServiceUnavailable} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/traces" {
				t.Error("wrong OTLP path")
			}
			_, _ = io.Copy(io.Discard, r.Body)
			w.WriteHeader(code)
		}))
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		exporter, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(server.URL+"/v1/traces"), otlptracehttp.WithInsecure(), otlptracehttp.WithRetry(otlptracehttp.RetryConfig{Enabled: false}))
		if err != nil {
			cancel()
			server.Close()
			t.Fatal(err)
		}
		wrapped := tracingMetricsExporter{exporter, metrics}
		span := tracetest.SpanStub{Name: "safe-operation", StartTime: time.Now(), EndTime: time.Now(), Resource: resource.Empty()}.Snapshot()
		err = wrapped.ExportSpans(ctx, []sdktrace.ReadOnlySpan{span})
		if (err == nil) != (code == http.StatusOK) {
			t.Errorf("export status%d error=%v", code, err)
		}
		_ = wrapped.Shutdown(ctx)
		cancel()
		server.Close()
	}
	if metrics.success.Load() != 1 || metrics.failure.Load() != 1 || metrics.lastSuccess.Load() == 0 || !metrics.enabled.Load() {
		t.Fatal("export failure must remain distinguishable from disabled tracing and prior success")
	}
	want := `# HELP astronomer_tracing_exports_total Completed SDK export calls, including failure; not internal retries or queue drops.
# TYPE astronomer_tracing_exports_total counter
astronomer_tracing_exports_total{astronomer_instance_id="` + InstanceID() + `",result="failure"} 1
astronomer_tracing_exports_total{astronomer_instance_id="` + InstanceID() + `",result="success"} 1
`
	if err := testutil.GatherAndCompare(registry, strings.NewReader(want), "astronomer_tracing_exports_total"); err != nil {
		t.Fatal(err)
	}
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		for _, metric := range family.Metric {
			for _, label := range metric.Label {
				if label.GetName() != instanceIDLabel && label.GetName() != "result" {
					t.Fatal("unbounded exporter label")
				}
			}
		}
	}
}
