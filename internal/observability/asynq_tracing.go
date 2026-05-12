package observability

import (
	"context"
	"encoding/json"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

// FEATURES-051126 T15 — asynq traceparent propagation.
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

// asynqCarrier is a propagation.TextMapCarrier backed by a map. Only
// the two W3C fields above are ever set/read.
type asynqCarrier struct {
	m map[string]string
}

func (c asynqCarrier) Get(key string) string  { return c.m[key] }
func (c asynqCarrier) Set(key, value string)  { c.m[key] = value }
func (c asynqCarrier) Keys() []string {
	keys := make([]string, 0, len(c.m))
	for k := range c.m {
		keys = append(keys, k)
	}
	return keys
}

// WithTracingPayload injects the current span's W3C trace headers into
// the JSON payload so the worker dequeue can rejoin the trace. No-ops
// when ctx carries no active span (the propagator skips the inject call
// and the carrier map stays empty).
//
// Returns the original bytes unchanged when:
//   - payload isn't a JSON object (rare; same shape contract as
//     WithCorrelationPayload)
//   - no span is active (nothing to propagate)
func WithTracingPayload(ctx context.Context, payload []byte) []byte {
	carrier := asynqCarrier{m: map[string]string{}}
	otel.GetTextMapPropagator().Inject(ctx, propagation.MapCarrier(carrier.m))
	if len(carrier.m) == 0 {
		return payload
	}

	var obj map[string]json.RawMessage
	if err := json.Unmarshal(payload, &obj); err != nil {
		return payload
	}
	if obj == nil {
		obj = map[string]json.RawMessage{}
	}
	if tp, ok := carrier.m["traceparent"]; ok && tp != "" {
		v, _ := json.Marshal(tp)
		obj[asynqTraceparentField] = v
	}
	if ts, ok := carrier.m["tracestate"]; ok && ts != "" {
		v, _ := json.Marshal(ts)
		obj[asynqTracestateField] = v
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return payload
	}
	return out
}

// ContextWithAsynqTracing extracts _traceparent / _tracestate from a
// task payload and returns a context with the parent span context
// installed so the worker's own spans become children of the original
// trace. Safe to call on every dequeue; no-op when the payload doesn't
// carry the fields (e.g. tasks enqueued before T15 landed).
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
	carrier := propagation.MapCarrier{}
	carrier["traceparent"] = probe.Traceparent
	if probe.Tracestate != "" {
		carrier["tracestate"] = probe.Tracestate
	}
	return otel.GetTextMapPropagator().Extract(ctx, carrier)
}
