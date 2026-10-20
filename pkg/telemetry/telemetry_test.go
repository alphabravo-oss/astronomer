package telemetry

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace/noop"
)

func scrape(t *testing.T, handler http.Handler) string {
	t.Helper()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("scrape status %d: %s", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

func TestInitServesOTelAndLegacyMetricsOnOneHandler(t *testing.T) {
	preserveTracingGlobals(t)
	registry := prometheus.NewRegistry()
	legacy := prometheus.NewCounter(prometheus.CounterOpts{Name: "legacy_fixture_total", Help: "client_golang fixture"})
	registry.MustRegister(legacy)
	legacy.Inc()

	shutdown, handler, err := Init(context.Background(), Config{
		ServiceName: "telemetry-fixture", ServiceVersion: "1.2.3", Environment: "qualification",
		Registerer: registry, Gatherer: registry, Logger: zerolog.Nop(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = shutdown(context.Background()) })
	counter, err := otel.Meter("telemetry-fixture").Int64Counter("astronomer.fixture.events")
	if err != nil {
		t.Fatal(err)
	}
	counter.Add(context.Background(), 2)

	body := scrape(t, handler)
	for _, want := range []string{
		"legacy_fixture_total 1",
		`astronomer_fixture_events_total{otel_scope_name="telemetry-fixture"`,
		`target_info{`,
		`service_name="telemetry-fixture"`,
		`service_version="1.2.3"`,
		`deployment_environment="qualification"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape missing %q:\n%s", want, body)
		}
	}
	if _, ok := otel.GetTracerProvider().(noop.TracerProvider); !ok {
		t.Errorf("empty endpoint must install the no-op tracer provider, got %T", otel.GetTracerProvider())
	}
}

type failingRegisterer struct{ prometheus.Registerer }

func (failingRegisterer) Register(prometheus.Collector) error { return errors.New("registry refused") }

func TestInitTearsDownTracerWhenMeterInitFails(t *testing.T) {
	preserveTracingGlobals(t)
	requests := make(chan struct{}, 8)
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		requests <- struct{}{}
		w.Header().Set("Content-Type", "application/x-protobuf")
	}))
	defer collector.Close()

	registry := prometheus.NewRegistry()
	_, handler, err := Init(context.Background(), Config{
		Endpoint: collector.URL, Protocol: ProtocolHTTPProtobuf, SampleRate: 1,
		Registerer: failingRegisterer{registry}, Gatherer: registry, Logger: zerolog.Nop(),
	})
	if err == nil || !strings.Contains(err.Error(), "meter provider") {
		t.Fatalf("Init error = %v, want meter provider failure", err)
	}
	if handler != nil {
		t.Fatal("failed Init must not return a metrics handler")
	}
	if _, ok := otel.GetTracerProvider().(noop.TracerProvider); !ok {
		t.Fatalf("partially initialised tracer left installed: %T", otel.GetTracerProvider())
	}
	if tracingMetrics.enabled.Load() {
		t.Fatal("tracing still reported enabled after teardown")
	}
	_, span := otel.Tracer("teardown").Start(context.Background(), "after-teardown")
	span.End()
	select {
	case <-requests:
		t.Fatal("torn-down tracer exported a span")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestEnvironmentDefaultsFromEnvThenDevelopment(t *testing.T) {
	t.Setenv(EnvironmentEnv, "")
	if got := (Config{}).withDefaults().Environment; got != "development" {
		t.Fatalf("default environment = %q, want development", got)
	}
	t.Setenv(EnvironmentEnv, "staging")
	if got := (Config{}).withDefaults().Environment; got != "staging" {
		t.Fatalf("env environment = %q, want staging", got)
	}
	if got := (Config{Environment: "production"}).withDefaults().Environment; got != "production" {
		t.Fatalf("explicit environment = %q, want production", got)
	}
}
