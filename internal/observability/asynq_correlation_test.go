package observability

import (
	"encoding/json"
	"testing"
)

// Round-trip: wrap a payload with a correlation ID, extract it back,
// must match. FEATURES-051126 T22.
func TestAsynqCorrelationRoundtrip(t *testing.T) {
	payload := []byte(`{"cluster_id":"abc","op":"reconcile"}`)
	wrapped := WithCorrelationPayload(payload, "req-12345")

	if got := ExtractAsynqCorrelationID(wrapped); got != "req-12345" {
		t.Errorf("extract = %q, want %q", got, "req-12345")
	}

	// Wrapped payload must still parse as the original shape (with the
	// extra `_correlation_id` field).
	var roundtrip struct {
		ClusterID     string `json:"cluster_id"`
		Op            string `json:"op"`
		CorrelationID string `json:"_correlation_id"`
	}
	if err := json.Unmarshal(wrapped, &roundtrip); err != nil {
		t.Fatalf("wrapped payload doesn't parse: %v", err)
	}
	if roundtrip.ClusterID != "abc" || roundtrip.Op != "reconcile" {
		t.Errorf("original fields lost: cluster_id=%q op=%q", roundtrip.ClusterID, roundtrip.Op)
	}
}

// Empty correlation ID is a no-op — the payload comes back unchanged.
func TestAsynqCorrelationEmpty(t *testing.T) {
	payload := []byte(`{"a":1}`)
	if got := WithCorrelationPayload(payload, ""); string(got) != string(payload) {
		t.Errorf("empty correlation should not change payload, got %q", string(got))
	}
}

// Non-JSON payloads are returned unchanged. Best-effort wrapping; we
// don't want a malformed payload to lose its contents.
func TestAsynqCorrelationNonJSON(t *testing.T) {
	payload := []byte(`not json`)
	got := WithCorrelationPayload(payload, "req-1")
	if string(got) != string(payload) {
		t.Errorf("non-JSON payload should be unchanged, got %q", string(got))
	}
}

// Extract on a payload with no correlation field returns empty.
func TestAsynqCorrelationExtractAbsent(t *testing.T) {
	if got := ExtractAsynqCorrelationID([]byte(`{"a":1}`)); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
	if got := ExtractAsynqCorrelationID(nil); got != "" {
		t.Errorf("nil payload should extract empty, got %q", got)
	}
	if got := ExtractAsynqCorrelationID([]byte(`not json`)); got != "" {
		t.Errorf("non-JSON payload should extract empty, got %q", got)
	}
}
