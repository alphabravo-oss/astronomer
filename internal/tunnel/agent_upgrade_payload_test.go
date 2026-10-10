package tunnel

import (
	"encoding/json"
	"testing"
)

func TestAgentUpgradeConfigurationPreservesOptionalPayload(t *testing.T) {
	for _, item := range []struct{ name, spec, want, digest string }{
		{"empty", "", "", ""},
		{"invalid", "{{{", "", ""},
		{"no-plan", `{"request":{}}`, "", ""},
		{"canonical-payload", `{"plan":{"agent_overrides":{"values":{"feature":true}},"configuration_digest":"  digest-fixture  "}}`, `{"values":{"feature":true}}`, "digest-fixture"},
	} {
		t.Run(item.name, func(t *testing.T) {
			payload, digest := agentUpgradeConfiguration(json.RawMessage(item.spec))
			if string(payload) != item.want || digest != item.digest {
				t.Fatal("optional upgrade configuration or trimmed identity changed")
			}
		})
	}
}

func TestAgentUpgradeTargetPullPolicyPreservesOptionalPayload(t *testing.T) {
	for _, item := range []struct{ name, spec, want string }{
		{"empty", "", ""}, {"invalid", "{{{", ""}, {"no-plan", `{"request":{}}`, ""},
		{"trimmed-value", `{"plan":{"target_pull_policy":"  Never  "}}`, "Never"},
	} {
		t.Run(item.name, func(t *testing.T) {
			if got := agentUpgradeTargetPullPolicy(json.RawMessage(item.spec)); got != item.want {
				t.Fatal("optional upgrade pull policy changed")
			}
		})
	}
}
