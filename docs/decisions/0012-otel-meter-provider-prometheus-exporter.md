# 0012 — pkg/telemetry: one Init for tracer and meter providers, one /metrics

- Status: accepted
- Date: 2026-10-20
- Guide: Technology Selection Guide ch.05 (telemetry initialisation in a single package, OTLP gRPC, Prometheus exporter on the admin port)

## Context

Tracing was initialised by `internal/observability.InitTracing`; metrics were
~30 client_golang collectors on the default registry, served by `promhttp`
from the server/worker admin listeners and the agent health port. There was
no OTel meter provider, so OTel-native instruments (otelhttp, SDK metrics)
had nowhere to go.

## Decision

- `pkg/telemetry.Init(ctx, cfg) (Shutdown, http.Handler, error)` is the only
  entrypoint (`InitTracing` is deleted). `Config` carries ServiceName,
  ServiceVersion, Environment (default `$ASTRONOMER_ENVIRONMENT`, then
  `development`), ServiceNamespace, ServiceInstanceID, Endpoint, Protocol
  (gRPC default, `http/protobuf` opt-in), Insecure, Headers, SampleRate, the
  Prometheus Registerer/Gatherer (default: the process registry) and Logger.
- Resource: `service.name`, `service.version`, `deployment.environment`
  (+ namespace/instance id). Tracing keeps the parent-based ratio sampler,
  the redacting span exporter and the export counters; an empty Endpoint
  installs a no-op tracer provider but metrics are still served.
- The meter provider uses `go.opentelemetry.io/otel/exporters/prometheus`
  with `WithRegisterer(<process registry>)`, so legacy collectors
  (`service_build_info`, `astronomer_*`) and OTel instruments
  (`http_server_request_duration_seconds` from otelhttp, `target_info`) are
  one scrape on `SERVER_METRICS_ADDR` / `WORKER_METRICS_ADDR` / the agent
  health port. If meter initialisation fails, the already-built tracer
  provider is shut down and the globals reset to no-op.
- `Shutdown` flushes both providers (`errors.Join`).
- Server, worker and agent call `telemetry.Init` before any instrumented
  component is constructed and pass the returned handler to their admin
  listener (`server.StartMetricsServer`, `worker.StartMetricsServer`,
  `HealthReporter.SetMetricsHandler`).

## Consequences

- OTel histograms appear next to the hand-rolled HTTP metrics; dashboards can
  move to the semantic-convention names incrementally.
- The dev Grafana Tempo datasource links traces to Loki with a line filter
  on the trace id, which matches both `trace_id` and a traceparent-adopted
  `correlation_id`.
