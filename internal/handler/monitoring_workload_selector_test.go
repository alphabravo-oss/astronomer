package handler

import (
	"regexp"
	"strconv"
	"testing"
)

func TestWorkloadPodRegexIsValidPromQLStringAndMatchesExactPods(t *testing.T) {
	// Pod names commonly contain hyphens; StatefulSet names can contain dots.
	// PromQL string parsing must happen before regexp parsing, so both layers
	// need their own escaping. A raw \- or \. is not a valid quoted string.
	names := []map[string]any{{"name": "observability-demo-78cd6b55b5-xvmzc"}, {"name": "api.worker-0"}}
	literal := `"` + escapePromLabel(podRegex(names)) + `"`
	decoded, err := strconv.Unquote(literal)
	if err != nil {
		t.Fatalf("invalid PromQL string %s: %v", literal, err)
	}
	pattern, err := regexp.Compile(decoded)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"observability-demo-78cd6b55b5-xvmzc", "api.worker-0"} {
		if !pattern.MatchString(name) {
			t.Fatalf("did not select workload pod %s", name)
		}
	}
	for _, name := range []string{"apiXworker-0", "other-observability-demo-78cd6b55b5-xvmzc", "api.worker-01"} {
		if pattern.MatchString(name) {
			t.Fatalf("selected unrelated pod %s", name)
		}
	}
}
