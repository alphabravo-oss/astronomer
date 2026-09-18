package observability

import (
	"context"
	"encoding/json"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

// Asynq traceparent propagation.
//
// asynq.Task has no headers — payloads are raw bytes — so we ride the
// same JSON-payload-injection convention as asynq_correlation.go. The
// W3C traceparent / tracestate values that the otel TextMapPropagator
// produces are mixed into the payload under reserved fields:
//   _traceparent  — the W3C traceparent header value
//   _tracestate   — the W3C tracestate header value (when present)
//
// Naming mirrors the leading-underscore convention to signal "framework
// metadata, not part of the task contract".
//
// Callers:
//   - Enqueuer: payload = WithTracingPayload(ctx, payload)
//   - Handler:  ctx = ContextWithAsynqTracing(ctx, task.Payload())

const (
	asynqTraceparentField = "_traceparent"
	asynqTracestateField  = "_tracestate"
)

// WithTracingPayload injects the current span's W3C trace headers into
// the JSON payload so the worker dequeue can rejoin the trace. No-ops
// when ctx carries no active span (the propagator skips the inject call
// and the carrier map stays empty), and when payload isn't a JSON
// object.
func WithTracingPayload(ctx context.Context, payload []byte) []byte {
	return mergeReservedFields(payload, tracingFieldsFromContext(ctx))
}

// EnrichTaskPayload returns a copy of payload with correlation ID +
// W3C trace headers merged in via a single JSON round-trip. Preferred
// over chained WithCorrelationPayload + WithTracingPayload calls
// because the chained form would unmarshal + remarshal twice per
// enqueue. Empty correlationID + no active span returns payload
// unchanged.
func EnrichTaskPayload(ctx context.Context, payload []byte, correlationID string) []byte {
	fields := tracingFieldsFromContext(ctx)
	if correlationID != "" {
		if fields == nil {
			fields = map[string]string{}
		}
		fields[asynqCorrelationField] = correlationID
	}
	return mergeReservedFields(payload, fields)
}

// tracingFieldsFromContext returns the W3C traceparent/tracestate keys
// that should be injected, or nil when no span is active.
func tracingFieldsFromContext(ctx context.Context) map[string]string {
	traceparent, tracestate := TraceContextFromContext(ctx)
	if traceparent == "" {
		return nil
	}
	fields := map[string]string{asynqTraceparentField: traceparent}
	if tracestate != "" {
		fields[asynqTracestateField] = tracestate
	}
	return fields
}

// TraceContextFromContext serializes only W3C trace routing metadata. Baggage
// is deliberately excluded from tunnel/task envelopes to avoid propagating
// arbitrary application or secret values.
func TraceContextFromContext(ctx context.Context) (traceparent, tracestate string) {
	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	return carrier["traceparent"], carrier["tracestate"]
}

// ContextWithAsynqTracing extracts _traceparent / _tracestate from a
// task payload and returns a context with the parent span context
// installed so the worker's own spans become children of the original
// trace. Safe to call on every dequeue; no-op when the payload doesn't
// carry the fields (e.g. tasks enqueued before tracing landed).
func ContextWithAsynqTracing(ctx context.Context, payload []byte) context.Context {
	if len(payload) == 0 {
		return ctx
	}
	var probe struct {
		Traceparent string `json:"_traceparent"`
		Tracestate  string `json:"_tracestate"`
	}
	if err := json.Unmarshal(payload, &probe); err != nil {
		return ctx
	}
	if probe.Traceparent == "" {
		return ctx
	}
	return ContextWithTraceContext(ctx, probe.Traceparent, probe.Tracestate)
}

// ContextWithTraceContext installs a remote W3C parent. Invalid or absent
// metadata safely leaves ctx without a valid remote span context.
func ContextWithTraceContext(ctx context.Context, traceparent, tracestate string) context.Context {
	if traceparent == "" {
		return ctx
	}
	carrier := propagation.MapCarrier{"traceparent": traceparent}
	if tracestate != "" {
		carrier["tracestate"] = tracestate
	}
	return otel.GetTextMapPropagator().Extract(ctx, carrier)
}
