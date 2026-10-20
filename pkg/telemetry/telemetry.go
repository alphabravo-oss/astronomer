// Package telemetry is the single OpenTelemetry initialisation point for
// every Astronomer binary (Technology Selection Guide ch.05).
//
// Init installs, in order: the W3C TraceContext+Baggage propagator, the
// tracer provider (OTLP gRPC by default, http/protobuf opt-in; a no-op
// provider when Endpoint is empty) and a meter provider whose Prometheus
// exporter registers on the process registry, so OTel instruments and the
// legacy client_golang collectors are served by one /metrics handler.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace/noop"
)

// EnvironmentEnv is consulted when Config.Environment is empty.
const EnvironmentEnv = "ASTRONOMER_ENVIRONMENT"

const defaultEnvironment = "development"

// Config is the typed telemetry configuration. Environment resolution of
// the OTEL_* variables happens in internal/config (or the agent config).
type Config struct {
	// ServiceName, ServiceVersion and Environment populate service.name,
	// service.version and deployment.environment. ServiceName defaults to
	// "astronomer-go"; Environment to $ASTRONOMER_ENVIRONMENT, then
	// "development".
	ServiceName    string
	ServiceVersion string
	Environment    string
	// ServiceNamespace and ServiceInstanceID distinguish otherwise identical
	// server/worker/agent resources.
	ServiceNamespace  string
	ServiceInstanceID string

	// Endpoint is the OTLP collector, e.g. "http://tempo:4317". Empty
	// installs a no-op tracer provider; metrics are still served.
	Endpoint string
	// Protocol is "grpc" (default) or "http/protobuf".
	Protocol string
	// Insecure forces plaintext; implied by an http:// Endpoint.
	Insecure bool
	// Headers are sent with every OTLP export (auth for hosted backends).
	Headers map[string]string
	// SampleRate is the parent-based head sampling ratio in [0,1]; values
	// outside are clamped and an explicit 0 never samples.
	SampleRate float64

	// Registerer/Gatherer are the Prometheus registry shared with the legacy
	// client_golang collectors; nil selects the process default registry.
	Registerer prometheus.Registerer
	Gatherer   prometheus.Gatherer

	// Logger receives initialisation and SDK diagnostics.
	Logger zerolog.Logger
}

// Shutdown flushes and stops the tracer and meter providers.
type Shutdown func(context.Context) error

func noopShutdown(context.Context) error { return nil }

// Init wires tracing and metrics and returns the bounded shutdown plus the
// /metrics handler for the dedicated admin listener. On failure every
// partially initialised provider is torn down and globals are reset.
func Init(ctx context.Context, cfg Config) (Shutdown, http.Handler, error) {
	if err := ctx.Err(); err != nil {
		return noopShutdown, nil, err
	}
	cfg = cfg.withDefaults()
	res, err := newResource(cfg)
	if err != nil {
		return noopShutdown, nil, err
	}
	tracerShutdown, err := initTracing(ctx, cfg, res)
	if err != nil {
		return noopShutdown, nil, err
	}
	meterProvider, handler, err := initMetrics(cfg, res)
	if err != nil {
		teardownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), tracingExportTimeout)
		defer cancel()
		_ = tracerShutdown(teardownCtx)
		otel.SetTracerProvider(noop.NewTracerProvider())
		tracingMetrics.enabled.Store(false)
		return noopShutdown, nil, fmt.Errorf("otel meter provider: %w", err)
	}
	otel.SetMeterProvider(meterProvider)
	shutdown := func(ctx context.Context) error {
		return errors.Join(meterProvider.Shutdown(ctx), tracerShutdown(ctx))
	}
	return shutdown, handler, nil
}

func (cfg Config) withDefaults() Config {
	if strings.TrimSpace(cfg.ServiceName) == "" {
		cfg.ServiceName = "astronomer-go"
	}
	if strings.TrimSpace(cfg.Environment) == "" {
		cfg.Environment = strings.TrimSpace(os.Getenv(EnvironmentEnv))
	}
	if cfg.Environment == "" {
		cfg.Environment = defaultEnvironment
	}
	if cfg.Registerer == nil {
		cfg.Registerer = prometheus.DefaultRegisterer
	}
	if cfg.Gatherer == nil {
		cfg.Gatherer = prometheus.DefaultGatherer
	}
	return cfg
}

func newResource(cfg Config) (*resource.Resource, error) {
	attributes := []attribute.KeyValue{
		semconv.ServiceName(cfg.ServiceName),
		semconv.ServiceVersionKey.String(cfg.ServiceVersion),
		semconv.DeploymentEnvironment(cfg.Environment),
	}
	if cfg.ServiceNamespace != "" {
		attributes = append(attributes, semconv.ServiceNamespace(cfg.ServiceNamespace))
	}
	if cfg.ServiceInstanceID != "" {
		attributes = append(attributes, semconv.ServiceInstanceID(cfg.ServiceInstanceID))
	}
	res, err := resource.Merge(
		resource.Default(),
		// The SDK's default resource can carry a newer semantic-convention
		// schema than this module. Keep these stable identity keys schemaless so
		// merging them cannot fail solely because dependency versions differ.
		resource.NewSchemaless(attributes...),
	)
	if err != nil {
		return nil, fmt.Errorf("otel resource: %w", err)
	}
	return res, nil
}

// initMetrics registers the OTel Prometheus exporter on the shared registry
// and returns the scrape handler for that registry.
func initMetrics(cfg Config, res *resource.Resource) (*sdkmetric.MeterProvider, http.Handler, error) {
	exporter, err := otelprom.New(otelprom.WithRegisterer(cfg.Registerer))
	if err != nil {
		return nil, nil, err
	}
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithResource(res), sdkmetric.WithReader(exporter))
	handler := promhttp.InstrumentMetricHandler(cfg.Registerer, promhttp.HandlerFor(cfg.Gatherer, promhttp.HandlerOpts{}))
	return provider, handler, nil
}
