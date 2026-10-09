package httpclient

import (
	"strings"
	"testing"
)

func TestDestinationPolicyRejectsAmbiguousAuthority(t *testing.T) {
	for _, tc := range []struct{ name, raw string }{
		{"duplicate restriction", `{"restricted":true,"restricted":false,"origins":[]}`},
		{"duplicate origin", `{"restricted":true,"origins":[{"url":"https://untrusted.test","url":"https://registry.corp","cidrs":["10.0.0.0/8"]}]}`},
		{"unknown property", `{"restricted":false,"secret_source_field":"synthetic-private"}`},
		{"deep unknown property", `{"future":` + strings.Repeat("[", 65) + `0` + strings.Repeat("]", 65) + `}`},
		{"trailing document", `{} {}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseDestinationPolicy(tc.raw); err == nil {
				t.Fatal("ambiguous/unsupported authority accepted")
			} else if strings.Contains(err.Error(), "synthetic-private") || strings.Contains(err.Error(), "secret_source_field") {
				t.Fatal("policy error exposed input content")
			}
		})
	}
}

func TestDestinationPolicyExactByteBoundAndUnrestrictedDefaults(t *testing.T) {
	for _, raw := range []string{"", `{}`, `{"restricted":false,"origins":[]}`, `{}` + strings.Repeat(" ", (64<<10)-2)} {
		policy, err := ParseDestinationPolicy(raw)
		if err != nil || policy.Restricted || len(policy.Origins) != 0 {
			t.Fatalf("supported unrestricted policy rejected: error=%v", err)
		}
	}
	if _, err := ParseDestinationPolicy(`{}` + strings.Repeat(" ", (64<<10)-1)); err == nil || err.Error() != "destination policy exceeds size limit" {
		t.Fatalf("exact oversized category changed: %v", err)
	}
}
