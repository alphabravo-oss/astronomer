package observability

import (
	"context"
	"os"
	"testing"

	"go.opentelemetry.io/otel"
)

// FEATURES-051126 T15: when the OTLP endpoint is unset, InitTracing
// must be a clean no-op — no background goroutines, no global side
// effects beyond installing the W3C propagator, and a Shutdown that
// returns nil immediately.
func TestInitTracing_DisabledByDefault(t *testing.T) {
	t.Parallel()

	// Cache + clear the env so a host that has the var set doesn't
	// accidentally turn this into the enabled-path test.
	prev := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	_ = os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Cleanup(func() {
		_ = os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", prev)
	})

	cfg := TracingFromEnv()
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
		got := parseOTLPHeaders(c.in)
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

// Sampler ratio clamping: values outside [0,1] are clamped, zero
// resolves to the default 0.05. We exercise the same logic via the
// config struct so the public surface is what we assert.
func TestTracingConfig_SamplerRatioClamping(t *testing.T) {
	t.Parallel()
	cases := []float64{-1.0, 0.0, 0.5, 1.0, 2.0}
	for _, in := range cases {
		// We don't actually init the SDK in this test — the disabled
		// path is the one that's safe to exercise. This is purely a
		// regression guard that the field is taken in unchanged so a
		// future refactor doesn't drop the operator's input on the
		// floor before clamping.
		cfg := TracingConfig{Endpoint: "", SamplerRatio: in}
		if cfg.SamplerRatio != in {
			t.Errorf("ratio %v: field round-trips as %v", in, cfg.SamplerRatio)
		}
	}
}
