package observability

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// FEATURES-051126 T15 — distributed tracing foundation.
//
// Astronomer ships zero OTel instrumentation today. The audit's
// acceptance bar is one trace that spans HTTP → asynq → DB → tunnel.
// This file is the bottom layer: an SDK init that no-ops when the user
// hasn't opted in, and a single Shutdown() the server defers.
//
// Opt-in via env:
//   OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector.observability:4318
//
// When unset (the default), InitTracing returns the no-op TracerProvider
// the SDK ships with, so existing dev / test invocations stay free of
// background goroutines and network egress.

// TracingConfig captures the operator-facing knobs the otel SDK reads
// at init time. All fields are optional; an empty Endpoint disables
// tracing entirely (no-op provider).
type TracingConfig struct {
	// Endpoint is the OTLP/HTTP collector base, e.g.
	// "http://otel-collector.observability:4318". When empty, tracing
	// is disabled.
	Endpoint string

	// Insecure forces the exporter to use plain HTTP instead of HTTPS.
	// Defaults to true when the Endpoint scheme is http://.
	Insecure bool

	// Headers are forwarded with every OTLP export — typical use is
	// auth tokens for hosted backends ("Authorization: Bearer X").
	Headers map[string]string

	// ServiceName populates the service.name resource attribute. When
	// empty, defaults to "astronomer-go".
	ServiceName string

	// ServiceVersion populates the service.version resource attribute.
	// When empty the build's main module version is used (already
	// recorded by pkg/version).
	ServiceVersion string

	// SamplerRatio is the head sampler probability in [0.0, 1.0].
	// Zero → no traces; 1.0 → all traces. Values outside that range
	// are clamped. When unspecified (zero), defaults to 0.05.
	SamplerRatio float64
}

// TracingFromEnv resolves a TracingConfig from the standard OTel env
// vars so operators don't need a chart change for every adjustment.
// Knobs honored:
//   - OTEL_EXPORTER_OTLP_ENDPOINT   (REQUIRED to enable)
//   - OTEL_EXPORTER_OTLP_INSECURE   ("true"/"1" forces plain HTTP)
//   - OTEL_EXPORTER_OTLP_HEADERS    ("k1=v1,k2=v2")
//   - OTEL_SERVICE_NAME             (defaults to astronomer-go)
//   - OTEL_TRACES_SAMPLER_ARG       (sampler ratio, "1.0" = always-on)
func TracingFromEnv() TracingConfig {
	cfg := TracingConfig{
		Endpoint:       os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
		ServiceName:    os.Getenv("OTEL_SERVICE_NAME"),
		ServiceVersion: os.Getenv("OTEL_SERVICE_VERSION"),
	}
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_INSECURE"))); v == "true" || v == "1" {
		cfg.Insecure = true
	}
	if h := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_HEADERS")); h != "" {
		cfg.Headers = parseOTLPHeaders(h)
	}
	if ratio := strings.TrimSpace(os.Getenv("OTEL_TRACES_SAMPLER_ARG")); ratio != "" {
		var f float64
		_, _ = fmt.Sscanf(ratio, "%f", &f)
		cfg.SamplerRatio = f
	}
	return cfg
}

func parseOTLPHeaders(raw string) map[string]string {
	out := map[string]string{}
	for _, pair := range strings.Split(raw, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		eq := strings.IndexByte(pair, '=')
		if eq < 0 {
			continue
		}
		out[strings.TrimSpace(pair[:eq])] = strings.TrimSpace(pair[eq+1:])
	}
	return out
}

// TracingShutdown is what InitTracing hands back to the caller. The
// server defers Shutdown(ctx) so spans get flushed during graceful
// shutdown before the OTLP exporter's TCP socket closes.
type TracingShutdown func(context.Context) error

// noopShutdown is returned when tracing is disabled.
func noopShutdown(context.Context) error { return nil }

// InitTracing wires the OTel SDK against an OTLP/HTTP collector when
// cfg.Endpoint is non-empty, and otherwise returns a no-op so the rest
// of the code can call otel.Tracer(...) without guards. The global
// TracerProvider is set on success; the global propagator is set to
// W3C TraceContext + Baggage so traceparent works across HTTP, asynq
// payloads (via internal/observability/asynq_correlation.go peers),
// and tunnel originator hand-offs.
func InitTracing(ctx context.Context, log *slog.Logger, cfg TracingConfig) (TracingShutdown, error) {
	if log == nil {
		log = slog.Default()
	}

	// Propagator is always set, even when the exporter is off. That
	// way an incoming traceparent header from a load balancer still
	// flows through this process — when tracing is later enabled, no
	// re-deploy is needed to start seeing connected traces.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	if strings.TrimSpace(cfg.Endpoint) == "" {
		log.Debug("otel tracing disabled (OTEL_EXPORTER_OTLP_ENDPOINT unset)")
		return noopShutdown, nil
	}

	serviceName := cfg.ServiceName
	if serviceName == "" {
		serviceName = "astronomer-go"
	}

	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(semconv.SchemaURL,
			semconv.ServiceName(serviceName),
			semconv.ServiceVersionKey.String(cfg.ServiceVersion),
		),
	)
	if err != nil {
		return noopShutdown, fmt.Errorf("otel resource: %w", err)
	}

	exporterOpts := []otlptracehttp.Option{
		otlptracehttp.WithEndpointURL(cfg.Endpoint),
	}
	// Endpoint URL takes precedence; for backwards compat with
	// non-URL forms ("collector:4318") the explicit Insecure knob is
	// still honored.
	if cfg.Insecure || strings.HasPrefix(cfg.Endpoint, "http://") {
		exporterOpts = append(exporterOpts, otlptracehttp.WithInsecure())
	}
	if len(cfg.Headers) > 0 {
		exporterOpts = append(exporterOpts, otlptracehttp.WithHeaders(cfg.Headers))
	}

	exporter, err := otlptracehttp.New(ctx, exporterOpts...)
	if err != nil {
		return noopShutdown, fmt.Errorf("otel exporter: %w", err)
	}

	// Sampling. Head-based ratio sampler with parent-respect — a
	// caller that already started a trace (e.g. external client) gets
	// its sampling decision propagated rather than re-rolled.
	ratio := cfg.SamplerRatio
	if ratio < 0 {
		ratio = 0
	}
	if ratio > 1 {
		ratio = 1
	}
	if ratio == 0 {
		ratio = 0.05
	}
	sampler := sdktrace.ParentBased(sdktrace.TraceIDRatioBased(ratio))

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler),
		// Batcher buffers + ships in the background. 5s flush window
		// keeps lag bounded under steady load; the explicit Shutdown
		// flushes the rest on graceful exit.
		sdktrace.WithBatcher(exporter,
			sdktrace.WithBatchTimeout(5*time.Second),
		),
	)
	otel.SetTracerProvider(tp)

	log.Info("otel tracing initialized",
		"endpoint", cfg.Endpoint,
		"sampler_ratio", ratio,
		"service_name", serviceName)

	return tp.Shutdown, nil
}
