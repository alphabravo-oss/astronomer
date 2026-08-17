package charlie

import "testing"

func TestModeStateUsesCharliesSignedIntegrationRevision(t *testing.T) {
	state := modeStateFromBridge(AdminBridgeStatus{
		CentralMode: "read_only", ProductModeCeiling: "read_only", EffectiveMode: "read_only",
		ProductEnabled: true, DeploymentEnabled: true, EffectiveEnabled: true, IntegrationRevision: "16", DisclosureDigest: "digest-a",
	}, 2)
	if state.Revision != 16 || state.Verified != ModeReadOnly || state.DisclosureDigest != "digest-a" {
		t.Fatalf("central authority revision was not preserved: %+v", state)
	}
}

func TestModeStatePreservesReviewedAuthorityBelowDisabledProductCeiling(t *testing.T) {
	state := modeStateFromBridge(AdminBridgeStatus{
		CentralMode: "read_only", ProductModeCeiling: "disabled", EffectiveMode: "disabled",
		ProductEnabled: true, DeploymentEnabled: true, EffectiveEnabled: false,
		IntegrationRevision: "17", DisclosureDigest: "digest-reviewed",
	}, 0)
	if !state.Active || state.Verified != ModeReadOnly || state.Revision != 17 || state.DisclosureDigest != "digest-reviewed" {
		t.Fatalf("disabled local ceiling erased the reviewed central snapshot: %+v", state)
	}
}

func TestModeStateTreatsEitherEnablementBoundaryAsInactive(t *testing.T) {
	for name, status := range map[string]AdminBridgeStatus{
		"product disabled":    {CentralMode: "read_only", ProductEnabled: false, DeploymentEnabled: true, EffectiveEnabled: false, IntegrationRevision: "17", DisclosureDigest: "stale"},
		"deployment disabled": {CentralMode: "read_only", ProductEnabled: true, DeploymentEnabled: false, EffectiveEnabled: false, IntegrationRevision: "17", DisclosureDigest: "stale"},
	} {
		t.Run(name, func(t *testing.T) {
			state := modeStateFromBridge(status, 0)
			if state.Active || state.Verified != ModeDisabled || state.DisclosureDigest != "" {
				t.Fatalf("inactive boundary retained authority: %+v", state)
			}
		})
	}
}
