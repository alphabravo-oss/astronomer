package observability

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// When the OTLP endpoint is unset, InitTracing
// must be a clean no-op — no background goroutines, no global side
// effects beyond installing the W3C propagator, and a Shutdown that
// returns nil immediately.
func TestInitTracing_DisabledByDefault(t *testing.T) {
	t.Parallel()

	cfg := TracingConfig{}
	if cfg.Endpoint != "" {
		t.Fatalf("expected empty Endpoint when env unset, got %q", cfg.Endpoint)
	}

	shutdown, err := InitTracing(context.Background(), nil, cfg)
	if err != nil {
		t.Fatalf("InitTracing returned error in disabled mode: %v", err)
	}
	if shutdown == nil {
		t.Fatal("Shutdown is nil; caller expects a non-nil callable even in no-op mode")
	}
	// Disabled-mode shutdown must complete instantly and return nil.
	if err := shutdown(context.Background()); err != nil {
		t.Errorf("no-op Shutdown returned error: %v", err)
	}

	// Propagator must still be installed so an upstream traceparent
	// header isn't dropped — when tracing is later enabled, no redeploy
	// is needed for connected traces.
	if got := otel.GetTextMapPropagator(); got == nil {
		t.Error("propagator not set; W3C traceparent would be dropped")
	}
}

// Verify the env parser handles edge cases that operators trip over.
func TestParseOTLPHeaders(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		in    string
		want  map[string]string
		count int
	}{
		{"empty", "", map[string]string{}, 0},
		{"single", "Authorization=Bearer xyz", map[string]string{"Authorization": "Bearer xyz"}, 1},
		{"multi", "a=1,b=2", map[string]string{"a": "1", "b": "2"}, 2},
		{"whitespace", " a = 1 , b = 2 ", map[string]string{"a": "1", "b": "2"}, 2},
		{"malformed_skipped", "good=1,broken,also=2", map[string]string{"good": "1", "also": "2"}, 2},
	}
	for _, c := range cases {
		got := ParseOTLPHeaders(c.in)
		if len(got) != c.count {
			t.Errorf("%s: count=%d, want %d (got %v)", c.name, len(got), c.count, got)
			continue
		}
		for k, v := range c.want {
			if got[k] != v {
				t.Errorf("%s: key %q = %q, want %q", c.name, k, got[k], v)
			}
		}
	}
}

func TestTracingSamplerHonorsExplicitZero(t *testing.T) {
	parent := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2},
		TraceFlags: trace.FlagsSampled, Remote: true,
	})
	params := sdktrace.SamplingParameters{
		ParentContext: trace.ContextWithRemoteSpanContext(context.Background(), parent),
		TraceID:       parent.TraceID(),
		Name:          "agent.operation",
	}
	if got := samplerForRatio(normalizedSamplerRatio(0)).ShouldSample(params).Decision; got != sdktrace.Drop {
		t.Fatalf("explicit zero decision = %v, want Drop even for sampled remote parent", got)
	}
	if got := normalizedSamplerRatio(-1); got != 0 {
		t.Fatalf("normalized -1 = %v, want 0", got)
	}
	if got := normalizedSamplerRatio(2); got != 1 {
		t.Fatalf("normalized 2 = %v, want 1", got)
	}
}

func TestTracingResourceCarriesDeploymentIdentity(t *testing.T) {
	res, err := tracingResource(TracingConfig{
		ServiceVersion: "1.2.3", Environment: "staging",
		ServiceNamespace: "astronomer", ServiceInstanceID: "pod-1",
	}, "astronomer-worker")
	if err != nil {
		t.Fatal(err)
	}
	want := map[attribute.Key]string{
		"service.name": "astronomer-worker", "service.version": "1.2.3",
		"deployment.environment": "staging", "service.namespace": "astronomer",
		"service.instance.id": "pod-1",
	}
	for key, value := range want {
		got, ok := res.Set().Value(key)
		if !ok || got.AsString() != value {
			t.Errorf("resource %s = %q, %t; want %q", key, got.AsString(), ok, value)
		}
	}
}
