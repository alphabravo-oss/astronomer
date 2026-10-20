package observability

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace/noop"
	"golang.org/x/net/http/httpguts"
)

// HTTP, queue, pgx and tunnel instrumentation share W3C propagation. This
// initializer is called once at process startup by server, worker and agent.
// Empty endpoints install a no-op provider; live reconfiguration is not a
// supported lifecycle. Callers own one bounded Shutdown before process exit.
const tracingExportTimeout = 5 * time.Second

// TracingConfig captures the operator-facing knobs the otel SDK reads
// at init time. All fields are optional; an empty Endpoint disables
// tracing entirely (no-op provider).
type TracingConfig struct {
	// Endpoint is the OTLP collector, e.g. "http://otel-collector:4317"
	// (gRPC, the default) or "http://otel-collector:4318" with
	// Protocol "http/protobuf". When empty, tracing is disabled.
	Endpoint string

	// Protocol selects the OTLP transport: "grpc" (default, guide ch.05)
	// or "http/protobuf" for collectors that only expose the HTTP receiver.
	Protocol string

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

	// Environment, ServiceNamespace, and ServiceInstanceID make otherwise
	// identical server/worker/agent resources distinguishable in one backend.
	Environment       string
	ServiceNamespace  string
	ServiceInstanceID string

	// SamplerRatio is the head sampler probability in [0.0, 1.0].
	// Zero → no traces; 1.0 → all traces. Values outside that range
	// are clamped. Configuration loaders own defaults so an explicit zero
	// remains distinguishable from an unset value.
	SamplerRatio float64
}

// ParseOTLPHeaders parses the standard comma-separated OTLP header syntax.
// Environment resolution happens in internal/config; observability receives
// an explicit typed configuration.
func ParseOTLPHeaders(raw string) map[string]string {
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

	if err := ctx.Err(); err != nil {
		return noopShutdown, err
	}
	endpoint, err := tracingEndpoint(cfg)
	if err != nil {
		return noopShutdown, err
	}
	for key, value := range cfg.Headers {
		if !httpguts.ValidHeaderFieldName(key) || !httpguts.ValidHeaderFieldValue(value) {
			return noopShutdown, fmt.Errorf("otel exporter headers are malformed")
		}
	}
	// The SDK otherwise logs full exporter errors directly to stderr, including
	// collector-controlled HTTP error bodies. Keep one safe event; returned
	// export/shutdown errors and the exporter counters retain their semantics.
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(error) {
		log.Warn("OpenTelemetry SDK reported an error", "event", "otel.sdk.error")
	}))

	// Propagator is always set, even when the exporter is off. That
	// way an incoming traceparent header from a load balancer still
	// flows through this process. Enabling export requires process restart.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	if endpoint == "" {
		otel.SetTracerProvider(noop.NewTracerProvider())
		tracingMetrics.enabled.Store(false)
		log.Debug("otel tracing disabled (OTEL_EXPORTER_OTLP_ENDPOINT unset)")
		return noopShutdown, nil
	}

	serviceName := cfg.ServiceName
	if serviceName == "" {
		serviceName = "astronomer-go"
	}

	res, err := tracingResource(cfg, serviceName)
	if err != nil {
		return noopShutdown, err
	}

	exporter, err := newTraceExporter(ctx, cfg, endpoint)
	if err != nil {
		return noopShutdown, fmt.Errorf("otel exporter: %w", err)
	}

	// Sampling. Head-based ratio sampler with parent-respect — a
	// caller that already started a trace (e.g. external client) gets
	// its sampling decision propagated rather than re-rolled. Explicit zero is
	// stronger: NeverSample also rejects a sampled remote parent, guaranteeing
	// that a zero-configured process exports no sampled traces.
	ratio := normalizedSamplerRatio(cfg.SamplerRatio)
	sampler := samplerForRatio(ratio)

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler),
		// Batcher buffers + ships in the background. 5s flush window
		// keeps lag bounded under steady load; the explicit Shutdown
		// flushes the rest on graceful exit.
		sdktrace.WithBatcher(tracingMetricsExporter{diagnosticExporter{exporter}, tracingMetrics},
			sdktrace.WithMaxQueueSize(2048),
			sdktrace.WithMaxExportBatchSize(512),
			sdktrace.WithBatchTimeout(5*time.Second),
			sdktrace.WithExportTimeout(tracingExportTimeout),
		),
	)
	otel.SetTracerProvider(tp)
	tracingMetrics.enabled.Store(true)

	log.Info("otel tracing initialized",
		"sampler_ratio", ratio,
		"protocol", normalizedProtocol(cfg.Protocol),
		"service_name", serviceName)

	return tp.Shutdown, nil
}

// Retain explicitly supported legacy host:port endpoints, but reject malformed
// URLs rather than letting the SDK silently fall back to its default address.
// Credentials belong in typed Headers, never userinfo or URL query strings.
func tracingEndpoint(cfg TracingConfig) (string, error) {
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		return "", nil
	}
	if !strings.Contains(endpoint, "://") {
		host, _, err := net.SplitHostPort(endpoint)
		if err != nil || host == "" {
			return "", fmt.Errorf("otel exporter endpoint must be an HTTP(S) URL or host:port")
		}
		scheme := "https://"
		if cfg.Insecure {
			scheme = "http://"
		}
		endpoint = scheme + endpoint
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return "", fmt.Errorf("otel exporter endpoint must be HTTP(S) without credentials, query or fragment")
	}
	// SDK1.47 WithEndpointURL treats an absent path as '/', unlike its old
	// default trace path. Our typed endpoint accepts a collector base URL.
	if parsed.Path == "" || parsed.Path == "/" {
		parsed.Path = "/v1/traces"
	}
	return parsed.String(), nil
}

func tracingResource(cfg TracingConfig, serviceName string) (*resource.Resource, error) {
	attributes := []attribute.KeyValue{
		semconv.ServiceName(serviceName),
		semconv.ServiceVersionKey.String(cfg.ServiceVersion),
	}
	if cfg.Environment != "" {
		attributes = append(attributes, semconv.DeploymentEnvironment(cfg.Environment))
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

func normalizedSamplerRatio(ratio float64) float64 {
	if ratio < 0 {
		return 0
	}
	if ratio > 1 {
		return 1
	}
	return ratio
}

func samplerForRatio(ratio float64) sdktrace.Sampler {
	if ratio == 0 {
		return sdktrace.NeverSample()
	}
	return sdktrace.ParentBased(sdktrace.TraceIDRatioBased(ratio))
}

// OTLP transport selection. gRPC is the canonical exporter (guide ch.05);
// "http/protobuf" remains available for collectors without a gRPC receiver.
const (
	ProtocolGRPC         = "grpc"
	ProtocolHTTPProtobuf = "http/protobuf"
)

func normalizedProtocol(protocol string) string {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case ProtocolHTTPProtobuf, "http":
		return ProtocolHTTPProtobuf
	default:
		return ProtocolGRPC
	}
}

func newTraceExporter(ctx context.Context, cfg TracingConfig, endpoint string) (sdktrace.SpanExporter, error) {
	insecure := cfg.Insecure || strings.HasPrefix(endpoint, "http://")
	if normalizedProtocol(cfg.Protocol) == ProtocolHTTPProtobuf {
		opts := []otlptracehttp.Option{
			otlptracehttp.WithEndpointURL(endpoint),
			otlptracehttp.WithTimeout(tracingExportTimeout),
			otlptracehttp.WithHeaders(cfg.Headers),
		}
		if insecure {
			opts = append(opts, otlptracehttp.WithInsecure())
		}
		return otlptracehttp.New(ctx, opts...)
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	opts := []otlptracegrpc.Option{
		otlptracegrpc.WithEndpoint(parsed.Host),
		otlptracegrpc.WithTimeout(tracingExportTimeout),
		otlptracegrpc.WithHeaders(cfg.Headers),
	}
	if insecure {
		opts = append(opts, otlptracegrpc.WithInsecure())
	}
	return otlptracegrpc.New(ctx, opts...)
}
