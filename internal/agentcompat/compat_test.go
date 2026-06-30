package agentcompat

import "testing"

func TestEvaluate(t *testing.T) {
	cases := map[string]struct {
		status  string
		blocked bool
	}{
		"v1.2.3": {"supported", false},
		"1.2.3":  {"supported", false},
		"v0.2.0": {"supported", false}, // the current release line
		"v0.9.0": {"supported", false}, // 0.x is all supported now
		"v0.1.0": {"supported", false}, // exactly the floor
		"v0.0.9": {"blocked", true},    // below the v0.1.0 floor
		"":       {"unknown", false},
		"latest": {"unknown", false},
		"dev":    {"unknown", false},
	}
	for version, want := range cases {
		got := Evaluate(version)
		if got.Status != want.status || got.Blocked != want.blocked {
			t.Fatalf("Evaluate(%q) = status:%q blocked:%v, want status:%q blocked:%v", version, got.Status, got.Blocked, want.status, want.blocked)
		}
	}
}
