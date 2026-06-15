package events

import (
	"encoding/json"
	"testing"
)

func TestRawJSON(t *testing.T) {
	if got := RawJSON(nil); got != nil {
		t.Fatalf("RawJSON(nil) = %s, want nil", got)
	}

	got := RawJSON(map[string]any{"name": "cluster-a"})
	var decoded map[string]string
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatalf("RawJSON returned invalid JSON: %v", err)
	}
	if decoded["name"] != "cluster-a" {
		t.Fatalf("decoded name = %q, want cluster-a", decoded["name"])
	}

	if got := RawJSON(func() {}); got != nil {
		t.Fatalf("unmarshalable value should return nil, got %s", got)
	}
}

func TestDecodeFilterGlobs(t *testing.T) {
	got, ok := DecodeFilterGlobs(nil)
	if !ok {
		t.Fatal("DecodeFilterGlobs(nil) ok = false, want true")
	}
	if got != nil {
		t.Fatalf("DecodeFilterGlobs(nil) = %#v, want nil", got)
	}

	got, ok = DecodeFilterGlobs([]byte(`["audit.*","cluster.created"]`))
	if !ok {
		t.Fatal("DecodeFilterGlobs(valid) ok = false, want true")
	}
	if len(got) != 2 || got[0] != "audit.*" || got[1] != "cluster.created" {
		t.Fatalf("DecodeFilterGlobs(valid) = %#v", got)
	}

	got, ok = DecodeFilterGlobs([]byte(`not-json`))
	if ok {
		t.Fatal("DecodeFilterGlobs(invalid) ok = true, want false")
	}
	if got != nil {
		t.Fatalf("DecodeFilterGlobs(invalid) = %#v, want nil", got)
	}
}
