package charlie

import "testing"

func TestAgentEnrollmentReadinessIsIndependentFromExecutionActivation(t *testing.T) {
	disabled := agentBridgeStatusFromAdmin(AdminBridgeStatus{
		CentralHealth: "healthy", LogicalAgentID: "agent-1", IntegrationRevision: "1",
		LeaderInstanceID: "instance-1", Epoch: 1, ReplicaCount: 2,
		ProductEnabled: false, DeploymentEnabled: false, EffectiveEnabled: false,
	})
	if !disabled.CentralEnrolled || !disabled.LeaderElected || !disabled.StandbyVisible {
		t.Fatalf("healthy disabled enrollment was not ready: %+v", disabled)
	}

	for name, status := range map[string]AdminBridgeStatus{
		"central unavailable": {CentralHealth: "unavailable", LogicalAgentID: "agent-1", IntegrationRevision: "1"},
		"agent missing":       {CentralHealth: "healthy", IntegrationRevision: "1"},
		"revision missing":    {CentralHealth: "healthy", LogicalAgentID: "agent-1"},
	} {
		t.Run(name, func(t *testing.T) {
			if agentBridgeStatusFromAdmin(status).CentralEnrolled {
				t.Fatal("incomplete central enrollment was accepted")
			}
		})
	}
}

func TestModeStateUsesCharliesSignedIntegrationRevision(t *testing.T) {
	state := modeStateFromBridge(AdminBridgeStatus{
		EffectiveMode: "read_only", ProductEnabled: true, DeploymentEnabled: true, EffectiveEnabled: true, IntegrationRevision: "16", DisclosureDigest: "digest-a",
	}, 2)
	if state.Revision != 16 || state.Verified != ModeReadOnly || state.DisclosureDigest != "digest-a" {
		t.Fatalf("central authority revision was not preserved: %+v", state)
	}
}

func TestModeStateTreatsEitherEnablementBoundaryAsInactive(t *testing.T) {
	for name, status := range map[string]AdminBridgeStatus{
		"product disabled":    {EffectiveMode: "read_only", ProductEnabled: false, DeploymentEnabled: true, EffectiveEnabled: false, IntegrationRevision: "17", DisclosureDigest: "stale"},
		"deployment disabled": {EffectiveMode: "read_only", ProductEnabled: true, DeploymentEnabled: false, EffectiveEnabled: false, IntegrationRevision: "17", DisclosureDigest: "stale"},
	} {
		t.Run(name, func(t *testing.T) {
			state := modeStateFromBridge(status, 0)
			if state.Active || state.Verified != ModeDisabled {
				t.Fatalf("inactive boundary retained authority: %+v", state)
			}
		})
	}
}
