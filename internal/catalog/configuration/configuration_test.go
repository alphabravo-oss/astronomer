package configuration

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestExactVersionIdentityAndAvailability(t *testing.T) {
	raw := json.RawMessage(`{"repository":"grafana","chart":"loki","version":"7.3.0","versions":[{"version":"7.3.0","available":true,"artifact":{"repository":"grafana","name":"loki","version":"7.3.0","digest":"sha256:` + strings.Repeat("a", 64) + `"}}]}`)
	artifact, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if artifact.Find("7.3.1") != nil {
		t.Fatal("applied configuration to a different version")
	}
	profile := artifact.Find("7.3.0")
	if profile.Validate("sha256:"+strings.Repeat("b", 64)) == nil {
		t.Fatal("accepted a changed archive")
	}
	if profile.Validate(profile.Artifact.Digest) != nil {
		t.Fatal("rejected exact identity")
	}
	profile.Revoked = true
	if profile.Validate(profile.Artifact.Digest) == nil {
		t.Fatal("accepted revoked version")
	}
	for _, changed := range []string{strings.Replace(string(raw), `"name":"loki"`, `"name":"lookalike"`, 1), strings.Replace(string(raw), `"version":"7.3.0","digest"`, `"version":"7.3.1","digest"`, 1)} {
		if _, err := Parse([]byte(changed)); err == nil {
			t.Fatal("accepted mismatched catalog identity")
		}
	}
}

func TestCompatibilityAndUpgradeConstraints(t *testing.T) {
	profile := Version{Available: true, UpgradeFrom: ">=1.0.0 <2.0.0"}
	profile.Compatibility.Kubernetes = ">=1.31.0 <1.36.0"
	if !profile.Eligible("v1.34.0").Available {
		t.Fatal("eligible cluster disabled")
	}
	for _, version := range []string{"v1.30.0", "unknown", "v1.36.0"} {
		result := profile.Eligible(version)
		if result.Available || result.UnavailableReason == "" {
			t.Fatal("incompatible cluster not explained")
		}
	}
	if !profile.Available {
		t.Fatal("eligibility mutated the source record")
	}
	if profile.CheckUpgrade("1.5.0") != nil || profile.CheckUpgrade("2.1.0") == nil {
		t.Fatal("upgrade constraint ignored")
	}
}
