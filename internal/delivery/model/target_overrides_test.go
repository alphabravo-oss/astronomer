package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTargetOverridesCanonicalDigestAndMerge(t *testing.T) {
	t.Parallel()
	left := TargetOverrides{HelmValues: json.RawMessage(`{"image":{"tag":"v1","pullPolicy":"Always"},"replicas":1}`)}
	right := TargetOverrides{HelmValues: json.RawMessage(`{"replicas":3,"image":{"tag":"v2"}}`)}
	first, err := right.CanonicalDigest()
	if err != nil {
		t.Fatal(err)
	}
	var reordered TargetOverrides
	if err := json.Unmarshal([]byte(`{"helm_values":{"image":{"tag":"v2"},"replicas":3}}`), &reordered); err != nil {
		t.Fatal(err)
	}
	second, err := reordered.CanonicalDigest()
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("semantic digest changed: %s != %s", first, second)
	}
	merged, err := MergeHelmValues(left.HelmValues, right.HelmValues)
	if err != nil {
		t.Fatal(err)
	}
	if string(merged) != `{"image":{"pullPolicy":"Always","tag":"v2"},"replicas":3}` {
		t.Fatalf("merged values = %s", merged)
	}
}

func TestTargetOverridesRejectUnsafeOrUnboundedInput(t *testing.T) {
	t.Parallel()
	cases := []string{
		`null`,
		`{"unknown":true}`,
		`{"helm_values":[]}`,
		`{"patches":[""]}`,
		`{"patches":["- not-an-object"]}`,
		`{"patches":["` + strings.Repeat("x", MaxTargetPatchBytes+1) + `"]}`,
	}
	for _, raw := range cases {
		var overrides TargetOverrides
		if err := json.Unmarshal([]byte(raw), &overrides); err == nil {
			t.Errorf("accepted invalid overrides %q", raw[:min(len(raw), 80)])
		}
	}
}
