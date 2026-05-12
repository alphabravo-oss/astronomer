package observability

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// FEATURES-051126 T15 — traceparent must round-trip through an asynq
// task payload so the worker handler can rejoin the original trace.
//
// Strategy: install a real TracerProvider locally (no exporter — spans
// just live in memory), start a span, inject into a payload, then
// extract on the "worker" side and confirm the resulting context has
// the same TraceID as the originating span.
func TestAsynqTracingPayload_RoundTrip(t *testing.T) {
	// Local TracerProvider so the propagator has something to extract
	// against. Global state is restored at the end so we don't leak
	// into other tests.
	prev := otel.GetTracerProvider()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		otel.SetTracerProvider(prev)
	})

	tracer := tp.Tracer("test")
	parentCtx, parentSpan := tracer.Start(context.Background(), "enqueue")
	defer parentSpan.End()
	originTraceID := parentSpan.SpanContext().TraceID()

	// Payload is a normal JSON object — the field we inject lives
	// alongside the task-specific fields.
	body, _ := json.Marshal(map[string]any{"cluster_id": "abc-123"})
	wrapped := WithTracingPayload(parentCtx, body)

	// Sanity: the wrapped payload should contain `_traceparent`.
	if !strings.Contains(string(wrapped), "_traceparent") {
		t.Fatalf("wrapped payload missing _traceparent: %s", wrapped)
	}

	// Worker side: extract traceparent into a fresh context.
	workerCtx := ContextWithAsynqTracing(context.Background(), wrapped)
	// Use SpanContextFromContext to inspect the remote parent that
	// the propagator stamped onto the worker ctx.
	parent := trace.SpanContextFromContext(workerCtx)
	if !parent.IsValid() {
		t.Fatalf("worker ctx has no propagated span context after extract")
	}
	if parent.TraceID() != originTraceID {
		t.Fatalf("worker TraceID %s != enqueuer TraceID %s",
			parent.TraceID(), originTraceID)
	}
}

// When ctx has no active span, WithTracingPayload must return payload
// unchanged so it stays cheap on the cold path.
func TestWithTracingPayload_NoActiveSpan_PassThrough(t *testing.T) {
	otel.SetTextMapPropagator(propagation.TraceContext{})
	body := []byte(`{"foo":1}`)
	got := WithTracingPayload(context.Background(), body)
	if string(got) != string(body) {
		t.Fatalf("payload mutated despite no active span: got %s want %s", got, body)
	}
}

// Extract must no-op when the field is absent (older tasks enqueued
// before T15 land).
func TestContextWithAsynqTracing_AbsentFields(t *testing.T) {
	body := []byte(`{"foo":1}`)
	ctx := ContextWithAsynqTracing(context.Background(), body)
	if trace.SpanContextFromContext(ctx).IsValid() {
		t.Fatal("ctx unexpectedly carries a span after extract on a payload with no traceparent")
	}
}
