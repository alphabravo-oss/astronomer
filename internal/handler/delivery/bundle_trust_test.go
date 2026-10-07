package delivery

import (
	"encoding/json"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

func TestBundleDeliveryRequiresCompletedResolutionAndSourceTrust(t *testing.T) {
	for _, test := range []struct {
		name, state, verification, source string
		allowed                           bool
	}{
		{"verified", "ready", "verified", `{}`, true},
		{"explicit unsigned policy", "ready", "unsigned", `{"trust_policy":{"allow_unsigned":true}}`, true},
		{"signature required", "ready", "unsigned", `{"trust_policy":{"allow_unsigned":false,"provider":"cosign_key","key_ref":"release-key"}}`, false},
		{"absent policy", "ready", "unsigned", `{}`, false},
		{"null policy", "ready", "unsigned", `{"trust_policy":null}`, false},
		{"invalid policy", "ready", "unsigned", `{"trust_policy":{"allow_unsigned":true,"provider":"cosign_key"}}`, false},
		{"malformed snapshot", "ready", "unsigned", `{`, false},
		{"wrong policy type", "ready", "unsigned", `{"trust_policy":{"allow_unsigned":"true"}}`, false},
		{"failed verification", "ready", "failed", `{"trust_policy":{"allow_unsigned":true}}`, false},
		{"pending verification", "ready", "pending", `{"trust_policy":{"allow_unsigned":true}}`, false},
		{"unfinished resolution", "resolving", "verified", `{}`, false},
		{"failed resolution", "failed", "unsigned", `{"trust_policy":{"allow_unsigned":true}}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			row := sqlc.ComponentBundleVersion{State: test.state, VerificationStatus: test.verification, SourceSpec: json.RawMessage(test.source)}
			if got := bundleReadyForDelivery(row); got != test.allowed {
				t.Fatalf("allowed=%v, want %v", got, test.allowed)
			}
		})
	}
}
