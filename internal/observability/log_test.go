package observability

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"
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
