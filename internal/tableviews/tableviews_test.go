package tableviews

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateTableKey(t *testing.T) {
	for _, ok := range []string{"pods", "cluster:nodes", "a.b/c-d_e"} {
		if err := ValidateTableKey(ok); err != nil {
			t.Errorf("%q rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "Pods", " pods", "-x", strings.Repeat("a", 129)} {
		if err := ValidateTableKey(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestNormalizeName(t *testing.T) {
	got, err := NormalizeName("  Failing pods ")
	if err != nil || got != "Failing pods" {
		t.Fatalf("got %q, %v", got, err)
	}
	for _, bad := range []string{"", "   ", "a\nb", strings.Repeat("é", 65)} {
		if _, err := NormalizeName(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if _, err := NormalizeName(strings.Repeat("é", 64)); err != nil {
		t.Errorf("64 runes rejected: %v", err)
	}
}

func TestNormalizeState(t *testing.T) {
	if _, err := NormalizeState(json.RawMessage(`{"v":1,"sort":[{"id":"name","desc":false}]}`)); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{``, `[]`, `"x"`, `null`, `{"evil":1}`, `{"v":`, `{"search":"` + strings.Repeat("a", MaxStateBytes) + `"}`} {
		if _, err := NormalizeState(json.RawMessage(bad)); err == nil {
			t.Errorf("%.30q accepted", bad)
		}
	}
}
