package charlie

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestAdminAgentUsesPersistedSignedReplicaCount(t *testing.T) {
	view := safeAdminAgent(sqlc.CharlieConnection{ReplicaCount: 7})
	if view.DesiredReplicas != 7 || len(view.Replicas) != 7 {
		t.Fatalf("admin agent view = %+v", view)
	}
}

func TestAdminModeExposesProductOwnedWorkloadCeilingAsUnverifiedUntilReadback(t *testing.T) {
	view := safeAdminMode(sqlc.CharlieConnection{RequestedMode: "auto", VerifiedMode: "read_only", VerifiedModeRevision: 7})
	if view.WorkloadCeiling != ModeAuto || view.WorkloadCeilingReady || view.Authoritative != ModeReadOnly {
		t.Fatalf("unsafe mode status: %+v", view)
	}
	if empty := emptyAdminStatus().Mode; empty.WorkloadCeiling != ModeDisabled || empty.WorkloadCeilingReady {
		t.Fatalf("empty mode status did not fail closed: %+v", empty)
	}
}

func TestQuiescedAdminStatusFailsClosedWithoutHistoricalRuntimeClaims(t *testing.T) {
	view := quiescedAdminStatus(sqlc.CharlieConnection{
		OnboardingState: "active", HealthState: "ready", Active: true,
		RequestedMode: "auto", VerifiedMode: "auto", EmergencyDisabled: true,
		ReplicaCount: 2, LeaderInstanceID: "stale-leader", AgentProtocolVersion: "1.0.0",
		LastConnectedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	if !view.Mode.EmergencyDisabled || view.Mode.Requested != ModeDisabled || view.Mode.Authoritative != ModeDisabled ||
		view.Mode.WorkloadCeiling != ModeDisabled || view.Mode.WorkloadCeilingReady {
		t.Fatalf("quiesced mode did not fail closed: %+v", view.Mode)
	}
	if view.Agent.ApplicationState != "inactive" || view.Agent.ReadyReplicas != 0 || view.Agent.LeaderReplica != "" ||
		view.Agent.LastHeartbeatAt != "" || len(view.Agent.StandbyReplicas) != 0 {
		t.Fatalf("quiesced agent retained historical runtime claims: %+v", view.Agent)
	}
	for _, replica := range view.Agent.Replicas {
		if replica.Role != "unknown" || replica.State != "unknown" || replica.LastHeartbeatAt != "" {
			t.Fatalf("quiesced replica retained historical runtime claims: %+v", replica)
		}
	}
}

func TestAdminServiceAuditFailurePrecedesAutomationAuthorityMutation(t *testing.T) {
	service := &AdminService{auditor: &authorityAuditFake{err: errors.New("database-SENTINEL")}}
	if _, err := service.SetAutomationIdentity(context.Background(), true); err == nil || strings.Contains(err.Error(), "database-SENTINEL") {
		t.Fatalf("admin authority audit failure was not bounded: %v", err)
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

func TestAutomationWriteGrantRequiresExactGlobalAutoEligiblePermission(t *testing.T) {
	if !hasAutomationWriteGrant([]AdminPermission{{Permission: "monitoring:update", Scope: "global"}}) {
		t.Fatal("exact queue-retry target grant was not recognized")
	}
	for name, grants := range map[string][]AdminPermission{
		"built-in Charlie only":   {{Permission: "charlie:create", Scope: "global"}, {Permission: "charlie:read", Scope: "global"}},
		"wildcard":                {{Permission: "*:*", Scope: "global"}},
		"read only":               {{Permission: "monitoring:read", Scope: "global"}},
		"non-auto delivery write": {{Permission: "delivery_rollouts:update", Scope: "global"}},
		"downstream cluster":      {{Permission: "monitoring:update", Scope: "cluster:cluster-a"}},
		"downstream project":      {{Permission: "monitoring:update", Scope: "project:project-a"}},
	} {
		t.Run(name, func(t *testing.T) {
			if hasAutomationWriteGrant(grants) {
				t.Fatal("unsafe or irrelevant grant satisfied auto-mode readiness")
			}
		})
	}
}
