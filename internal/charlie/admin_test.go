package charlie

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
)

func TestAdminInstallSpecUsesPersistedSignedReplicaCount(t *testing.T) {
	spec := adminInstallSpec(sqlc.CharlieConnection{ReplicaCount: 7, RequestedMode: "approval"})
	if spec.ReplicaCount != 7 || spec.ModeCeiling != ModeApproval {
		t.Fatalf("admin install spec = %+v", spec)
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
	if !hasAutomationWriteGrant([]AdminPermission{{Permission: "argocd:update", Scope: "global"}}) {
		t.Fatal("exact Argo sync target grant was not recognized")
	}
	for name, grants := range map[string][]AdminPermission{
		"built-in Charlie only": {{Permission: "charlie:create", Scope: "global"}, {Permission: "charlie:read", Scope: "global"}},
		"wildcard":              {{Permission: "*:*", Scope: "global"}},
		"read only":             {{Permission: "monitoring:read", Scope: "global"}},
		"downstream cluster":    {{Permission: "monitoring:update", Scope: "cluster:cluster-a"}},
		"downstream project":    {{Permission: "monitoring:update", Scope: "project:project-a"}},
	} {
		t.Run(name, func(t *testing.T) {
			if hasAutomationWriteGrant(grants) {
				t.Fatal("unsafe or irrelevant grant satisfied auto-mode readiness")
			}
		})
	}
}

func TestReplacementActionRequiresExactStagedSemantics(t *testing.T) {
	now := time.Date(2026, 8, 6, 0, 0, 0, 0, time.UTC)
	current := replacementConnectionFixture(now, "1.0.16", "old", true, "active")
	next := replacementConnectionFixture(now.Add(time.Minute), "1.0.17", "new", false, "consumed")
	if err := validateReplacementAction(current, next, "upgrade", now); err != nil {
		t.Fatalf("valid upgrade rejected: %v", err)
	}

	rollbackCurrent := replacementConnectionFixture(now, "1.0.17", "new", true, "active")
	rollbackNext := replacementConnectionFixture(now.Add(time.Minute), "1.0.16", "old", false, "consumed")
	if err := validateReplacementAction(rollbackCurrent, rollbackNext, "rollback", now); err != nil {
		t.Fatalf("valid rollback rejected: %v", err)
	}

	rotation := replacementConnectionFixture(now.Add(time.Minute), "1.0.16", "old", false, "consumed")
	rotation.OnboardingPackageID = "package-rotated"
	rotation.OnboardingPackageDigest = strings.Repeat("c", 64)
	if err := validateReplacementAction(current, rotation, "rotate", now); err != nil {
		t.Fatalf("valid credential rotation rejected: %v", err)
	}

	for name, mutate := range map[string]func(*sqlc.CharlieConnection){
		"unknown action":       func(*sqlc.CharlieConnection) {},
		"active candidate":     func(row *sqlc.CharlieConnection) { row.Active = true },
		"identity drift":       func(row *sqlc.CharlieConnection) { row.DeploymentID = "other" },
		"route drift":          func(row *sqlc.CharlieConnection) { row.RouteID = "other" },
		"same package":         func(row *sqlc.CharlieConnection) { row.OnboardingPackageID = current.OnboardingPackageID },
		"missing secret proof": func(row *sqlc.CharlieConnection) { row.AgentSecretHmac = "" },
		"expired package":      func(row *sqlc.CharlieConnection) { row.OnboardingPackageExpiresAt = now },
		"upgrade downgrade":    func(row *sqlc.CharlieConnection) { row.ChartVersion = "1.0.15" },
		"upgrade same artifact": func(row *sqlc.CharlieConnection) {
			row.ChartVersion, row.ChartDigest, row.ImageDigest = current.ChartVersion, current.ChartDigest, current.ImageDigest
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := next
			mutate(&candidate)
			action := "upgrade"
			if name == "unknown action" {
				action = "destroy"
			}
			if err := validateReplacementAction(current, candidate, action, now); err == nil {
				t.Fatal("unsafe replacement unexpectedly accepted")
			}
		})
	}
}

func replacementConnectionFixture(created time.Time, version, artifact string, active bool, state string) sqlc.CharlieConnection {
	return sqlc.CharlieConnection{
		ID: uuid.New(), InstallationID: uuid.MustParse("4f1817b0-c4d0-4ea3-a250-56f9daa52265"),
		ProductID: "product", ProductSlug: "astronomer", DeploymentID: "deployment", RouteID: "route",
		CentralUrl: "https://charlie.example", CentralCaFingerprint: strings.Repeat("a", 64),
		SigningKeyID: "signing", SigningKeyFingerprint: strings.Repeat("b", 64),
		LogicalAgentID: "agent", ReplicaCount: 2, BridgeServiceName: "bridge", McpServiceName: "mcp",
		OnboardingPackageID: "package-" + artifact, OnboardingPackageDigest: strings.Repeat(artifact[:1], 64),
		OnboardingPackageExpiresAt: created.Add(time.Hour), EnrollmentCredentialsExpiresAt: created.Add(time.Hour),
		ArtifactCredentialExpiresAt: created.Add(time.Hour), CertificateExpiresAt: created.Add(time.Hour),
		AgentSecretHmac: "integrity", ChartVersion: version, ChartDigest: "sha256:chart-" + artifact,
		ImageDigest: "sha256:image-" + artifact, Active: active, OnboardingState: state, CreatedAt: created,
	}
}
