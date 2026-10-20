package telemetry

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace/noop"
	"golang.org/x/net/http/httpguts"
)

// HTTP, queue, pgx and tunnel instrumentation share W3C propagation. This
// initializer is called once at process startup by server, worker and agent.
// Empty endpoints install a no-op provider; live reconfiguration is not a
// supported lifecycle. Callers own one bounded Shutdown before process exit.
const tracingExportTimeout = 5 * time.Second

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

// initTracing installs the propagator, the SDK error handler and the tracer
// provider. An empty endpoint installs a no-op provider (propagation still
// works so an inbound traceparent keeps flowing through this process).
func initTracing(ctx context.Context, cfg Config, res *resource.Resource) (Shutdown, error) {
	log := cfg.Logger
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
		log.Warn().Str("event", "otel.sdk.error").Msg("OpenTelemetry SDK reported an error")
	}))
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	if endpoint == "" {
		otel.SetTracerProvider(noop.NewTracerProvider())
		tracingMetrics.enabled.Store(false)
		log.Debug().Msg("otel tracing disabled (OTEL_EXPORTER_OTLP_ENDPOINT unset)")
		return noopShutdown, nil
	}
	exporter, err := newTraceExporter(ctx, cfg, endpoint)
	if err != nil {
		return noopShutdown, fmt.Errorf("otel exporter: %w", err)
	}
	// Head-based ratio sampler with parent-respect; explicit zero is stronger:
	// NeverSample also rejects a sampled remote parent.
	ratio := normalizedSamplerRatio(cfg.SampleRate)
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithSampler(samplerForRatio(ratio)),
		sdktrace.WithBatcher(tracingMetricsExporter{diagnosticExporter{exporter}, tracingMetrics},
			sdktrace.WithMaxQueueSize(2048),
			sdktrace.WithMaxExportBatchSize(512),
			sdktrace.WithBatchTimeout(5*time.Second),
			sdktrace.WithExportTimeout(tracingExportTimeout),
		),
	)
	otel.SetTracerProvider(tp)
	tracingMetrics.enabled.Store(true)
	log.Info().Float64("sampler_ratio", ratio).Str("protocol", normalizedProtocol(cfg.Protocol)).
		Str("service_name", cfg.ServiceName).Msg("otel tracing initialized")
	return tp.Shutdown, nil
}

// Retain explicitly supported legacy host:port endpoints, but reject malformed
// URLs rather than letting the SDK silently fall back to its default address.
// Credentials belong in typed Headers, never userinfo or URL query strings.
func tracingEndpoint(cfg Config) (string, error) {
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

func newTraceExporter(ctx context.Context, cfg Config, endpoint string) (sdktrace.SpanExporter, error) {
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
