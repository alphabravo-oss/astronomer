package tunnel

import (
	"encoding/json"
	"strings"
)

func agentUpgradeConfiguration(spec json.RawMessage) (json.RawMessage, string) {
	if len(spec) == 0 {
		return nil, ""
	}
	var envelope struct {
		Plan struct {
			AgentOverrides      json.RawMessage `json:"agent_overrides"`
			ConfigurationDigest string          `json:"configuration_digest"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(spec, &envelope); err != nil {
		return nil, ""
	}
	return envelope.Plan.AgentOverrides, strings.TrimSpace(envelope.Plan.ConfigurationDigest)
}

// agentUpgradeRollbackImage pulls the rollback image out of the operation spec
// persisted at queue time (internal/handler/cluster_agents.go writes the whole
// upgrade plan under "plan"). It is best-effort: a spec that is absent, invalid
// or from an older schema yields "", and the agent falls back to the image it
// is currently running.
func agentUpgradeRollbackImage(spec json.RawMessage) string {
	if len(spec) == 0 {
		return ""
	}
	var envelope struct {
		Plan struct {
			RollbackImage string `json:"rollback_image"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(spec, &envelope); err != nil {
		return ""
	}
	return strings.TrimSpace(envelope.Plan.RollbackImage)
}

func agentUpgradeTargetPullPolicy(spec json.RawMessage) string {
	if len(spec) == 0 {
		return ""
	}
	var envelope struct {
		Plan struct {
			TargetPullPolicy string `json:"target_pull_policy"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(spec, &envelope); err != nil {
		return ""
	}
	return strings.TrimSpace(envelope.Plan.TargetPullPolicy)
}
