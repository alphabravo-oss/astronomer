// Package observability — asynq correlation ID propagation.
//
// FEATURES-051126 T22: the request-side audit + log pipelines already
// propagate X-Correlation-ID through the handler chain; the worker
// side didn't see it. A user clicking "decommission cluster" produced
// an HTTP log line with correlation_id=abc-123 and then a worker log
// line for cluster.decommission with no correlation tied back to the
// originating request.
//
// asynq.Task has no header concept on the wire — payloads are raw
// bytes. We carry the correlation ID by mixing a single `_correlation_id`
// field into the JSON payload object at enqueue time and stripping it
// back out at dequeue time. The convention is reserved (leading
// underscore) so it can't collide with task-specific fields.
//
// Callers:
//   - Enqueuer: AsynqEnqueueOptions(ctx) returns the slice of asynq.Option
//     to pass to client.Enqueue, OR use WithCorrelationPayload to wrap
//     the payload bytes inline. The two helpers cover both shapes of
//     enqueue site we have today.
//   - Handler: ExtractAsynqCorrelationID(task.Payload()) returns the
//     correlation_id (or empty); pass to slog via
//     observability.WithCorrelationID.
package observability

import (
	"encoding/json"
)

// asynqCorrelationField is the reserved JSON field name. Underscore-led
// to signal "framework metadata, not part of the task contract".
const asynqCorrelationField = "_correlation_id"

// mergeReservedFields decodes payload as a JSON object, merges the
// supplied string fields into it, and re-encodes. Used by both the
// correlation-id and tracing helpers in this package; centralizes the
// "payload isn't an object → return unchanged" contract that both
// callers rely on.
//
// Empty fields map returns the payload unchanged.
func mergeReservedFields(payload []byte, fields map[string]string) []byte {
	if len(fields) == 0 {
		return payload
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(payload, &obj); err != nil {
		return payload
	}
	if obj == nil {
		obj = map[string]json.RawMessage{}
	}
	for k, v := range fields {
		enc, _ := json.Marshal(v)
		obj[k] = enc
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return payload
	}
	return out
}

// WithCorrelationPayload returns a copy of payload with the supplied
// correlation ID merged in as `_correlation_id`. Empty correlationID or
// non-object payload returns the original bytes unchanged.
func WithCorrelationPayload(payload []byte, correlationID string) []byte {
	if correlationID == "" {
		return payload
	}
	return mergeReservedFields(payload, map[string]string{asynqCorrelationField: correlationID})
}

// ExtractAsynqCorrelationID pulls `_correlation_id` from a task payload.
// Returns empty when the payload isn't a JSON object or the field is
// absent. Safe to call on every dequeue; cost is one unmarshal of one
// small field.
func ExtractAsynqCorrelationID(payload []byte) string {
	if len(payload) == 0 {
		return ""
	}
	var probe struct {
		CorrelationID string `json:"_correlation_id"`
	}
	if err := json.Unmarshal(payload, &probe); err != nil {
		return ""
	}
	return probe.CorrelationID
}
