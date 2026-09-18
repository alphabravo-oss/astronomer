package pagination

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestCanonicalEnvelope(t *testing.T) {
	for _, tc := range []struct {
		name     string
		items    []string
		metadata Metadata
		total    bool
	}{
		{"nil exact", nil, Exact(int64(0), 50, 0, 0), true},
		{"unknown full", []string{"a"}, FromPage(1, 4, 1), false},
		{"unknown final", []string{"a"}, Uncounted(5, 4, 1, false), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			Write(recorder, tc.items, tc.metadata)
			var envelope map[string]json.RawMessage
			if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if len(envelope) != 2 || envelope["pagination"] == nil || string(envelope["data"]) == "null" {
				t.Fatalf("invalid canonical envelope: %s", recorder.Body.String())
			}
			var metadata map[string]json.RawMessage
			if err := json.Unmarshal(envelope["pagination"], &metadata); err != nil {
				t.Fatal(err)
			}
			if (metadata["total"] != nil) != tc.total {
				t.Fatalf("total presence = %s", envelope["pagination"])
			}
			for _, key := range []string{"count", "next", "previous", "total_known"} {
				if metadata[key] != nil || envelope[key] != nil {
					t.Fatalf("retired key %s", key)
				}
			}
		})
	}
}

func TestEmptyPageNeverLoops(t *testing.T) {
	metadata := Exact(int64(100), 20, 10, 0)
	if metadata.HasMore || metadata.NextOffset != nil {
		t.Fatalf("empty page must not yield a nonadvancing continuation: %+v", metadata)
	}
}

func TestLargeExactTotalPreserved(t *testing.T) {
	const total = int64(1) << 40
	metadata := Exact(total, 20, 10, 2)
	if metadata.Total == nil || *metadata.Total != total || !metadata.HasMore || *metadata.NextOffset != 12 {
		t.Fatalf("metadata = %+v", metadata)
	}
}
