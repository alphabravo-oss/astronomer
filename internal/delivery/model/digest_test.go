package model

import (
	"strings"
	"testing"
)

func TestCanonicalDigestIgnoresMapOrder(t *testing.T) {
	t.Parallel()

	a, err := CanonicalDigest(map[string]any{"z": 1, "nested": map[string]any{"b": true, "a": "value"}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := CanonicalDigest(map[string]any{"nested": map[string]any{"a": "value", "b": true}, "z": 1})
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("canonical digests differ: %s != %s", a, b)
	}
}

func TestParseDigestRejectsNonCanonicalForms(t *testing.T) {
	t.Parallel()

	valid := "sha256:" + strings.Repeat("a", 64)
	if got, err := ParseDigest(valid); err != nil || got.String() != valid {
		t.Fatalf("ParseDigest(valid) = %q, %v", got, err)
	}
	for _, input := range []string{
		strings.Repeat("a", 64),
		"SHA256:" + strings.Repeat("a", 64),
		"sha256:" + strings.Repeat("A", 64),
		"sha256:short",
	} {
		if _, err := ParseDigest(input); err == nil {
			t.Errorf("ParseDigest(%q) unexpectedly succeeded", input)
		}
	}
}
