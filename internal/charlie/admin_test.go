package charlie

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

func TestAdminInstallSpecUsesPersistedSignedReplicaCount(t *testing.T) {
	spec := adminInstallSpec(sqlc.CharlieConnection{ReplicaCount: 7})
	if spec.ReplicaCount != 7 {
		t.Fatalf("admin readiness expected replicas = %d, want 7", spec.ReplicaCount)
	}
}

func TestAdminAgentReplicaStatusIsBoundedAndDoesNotReadCredentials(t *testing.T) {
	row := sqlc.CharlieConnection{
		ReplicaCount: 2, LeaderInstanceID: "leader-opaque", AgentProtocolVersion: "bridge/v1",
		HealthState: "ready", AgentSecretName: "must-not-appear", LocalTrustMaterialEncrypted: "must-not-appear",
	}
	view := AdminStatusView{Agent: safeAdminAgent(row)}
	applyBridgeStatus(&view, AdminBridgeStatus{
		CentralHealth: "healthy", InstanceID: "leader-opaque", LeaderInstanceID: "leader-opaque",
		ReplicaCount: 2, ReplicaOrdinal: 0, ArtifactVersion: "1.0.0", Epoch: 7,
	})
	if len(view.Agent.Replicas) != 2 || view.Agent.Replicas[0].Role != "leader" || view.Agent.Replicas[0].State != "ready" ||
		view.Agent.Replicas[0].Version != "1.0.0" || view.Agent.Replicas[1].State != "unknown" {
		t.Fatalf("replica status = %+v", view.Agent.Replicas)
	}
	encoded, err := json.Marshal(view.Agent)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "must-not-appear") {
		t.Fatalf("safe replica status exposed Secret metadata: %s", encoded)
	}
}
