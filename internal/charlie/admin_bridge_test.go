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
		EffectiveMode: "read_only", ProductEnabled: true, IntegrationRevision: "16", DisclosureDigest: "digest-a",
	}, 2)
	if state.Revision != 16 || state.Verified != ModeReadOnly || state.DisclosureDigest != "digest-a" {
		t.Fatalf("central authority revision was not preserved: %+v", state)
	}
}
