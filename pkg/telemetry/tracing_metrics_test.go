package telemetry

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
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
astronomer_tracing_exports_total{astronomer_instance_id="` + observability.InstanceID() + `",result="failure"} 1
astronomer_tracing_exports_total{astronomer_instance_id="` + observability.InstanceID() + `",result="success"} 1
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
				if label.GetName() != "astronomer_instance_id" && label.GetName() != "result" {
					t.Fatal("unbounded exporter label")
				}
			}
		}
	}
}

func TestTracingEnabledWithZeroSamplingDoesNotImplyExports(t *testing.T) {
	preserveTracingGlobals(t)
	var requests atomic.Uint64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
	}))
	defer server.Close()
	beforeSuccess, beforeFailure := tracingMetrics.success.Load(), tracingMetrics.failure.Load()
	shutdown, err := initForTest(t, zerolog.Nop(), Config{Endpoint: server.URL, Protocol: ProtocolHTTPProtobuf, SampleRate: 0})
	if err != nil {
		t.Fatal(err)
	}
	for _, ctx := range []context.Context{
		context.Background(),
		trace.ContextWithRemoteSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
			TraceID: trace.TraceID{1}, SpanID: trace.SpanID{1}, TraceFlags: trace.FlagsSampled, Remote: true,
		})),
	} {
		_, span := otel.Tracer("sampling-zero-fixture").Start(ctx, "safe-operation")
		if span.IsRecording() {
			t.Error("explicit zero sampled a local or remote-parent span")
		}
		span.End()
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if !tracingMetrics.enabled.Load() {
		t.Fatal("configured sampling zero must remain enabled")
	}
	if requests.Load() != 0 || tracingMetrics.success.Load() != beforeSuccess || tracingMetrics.failure.Load() != beforeFailure {
		t.Fatal("sampling zero produced an HTTP export or invented exporter progress")
	}
	shutdown, err = initForTest(t, zerolog.Nop(), Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if tracingMetrics.enabled.Load() {
		t.Fatal("empty endpoint retained the enabled gauge")
	}
}
