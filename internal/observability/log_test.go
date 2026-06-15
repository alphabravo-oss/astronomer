package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

func TestWithEventAndCorrelationID(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))

	WithCorrelationID(WithEvent(log, "http_request"), "corr-123").Info("done")

	var payload map[string]any
	if err := json.Unmarshal(buf.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal log: %v", err)
	}
	if payload["event"] != "http_request" {
		t.Fatalf("event = %v, want http_request", payload["event"])
	}
	if payload["correlation_id"] != "corr-123" {
		t.Fatalf("correlation_id = %v, want corr-123", payload["correlation_id"])
	}
}

func TestWithRequestAndTraceID(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	traceID := trace.TraceID{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10}
	spanID := trace.SpanID{0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18}
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID,
		SpanID:  spanID,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), spanContext)

	WithTraceID(WithRequestID(log, "req-123"), ctx).Info("done")

	var payload map[string]any
	if err := json.Unmarshal(buf.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal log: %v", err)
	}
	if payload["request_id"] != "req-123" {
		t.Fatalf("request_id = %v, want req-123", payload["request_id"])
	}
	if payload["trace_id"] != traceID.String() {
		t.Fatalf("trace_id = %v, want %s", payload["trace_id"], traceID.String())
	}
}

func TestLogIdentifiers(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))

	payload := []byte(`{"actor_user_id":"user-1","cluster_id":"cluster-1","operation_id":"op-1","user_id":"target-user"}`)
	WithLogIdentifiers(log, ExtractLogIdentifiers(payload)).Info("done")

	var out map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal log: %v", err)
	}
	if out["actor_id"] != "user-1" {
		t.Fatalf("actor_id = %v, want user-1", out["actor_id"])
	}
	if out["cluster_id"] != "cluster-1" {
		t.Fatalf("cluster_id = %v, want cluster-1", out["cluster_id"])
	}
	if out["operation_id"] != "op-1" {
		t.Fatalf("operation_id = %v, want op-1", out["operation_id"])
	}
	if _, ok := out["user_id"]; ok {
		t.Fatal("generic user_id should not be copied into structured log fields")
	}
}
