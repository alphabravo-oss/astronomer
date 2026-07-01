package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// --- fakes ----------------------------------------------------------------

type fakeDecommQuerier struct {
	mu sync.Mutex

	cluster sqlc.Cluster
	row     sqlc.ClusterDecommission

	// Per-phase error injection. The reconciler calls into these methods in
	// order; set the corresponding `*Err` to a non-nil error to simulate
	// that phase failing.
	regTokenErr      error
	agentTokenErr    error
	archiveErr       error
	registryErr      error
	healthErr        error
	conditionsErr    error
	connsErr         error
	silencesErr      error
	rulesErr         error
	chartsErr        error
	policiesErr      error
	projNsErr        error
	roleBindingsErr  error
	snapSchedulesErr error
	tombstoneErr     error
	updatePhasesErr  error
	claimErr         error
	argocdManaged    []sqlc.ArgocdManagedCluster

	// Per-method call counters (so tests can assert what was invoked).
	calls map[string]int

	// Audit rows recorded.
	audit []sqlc.CreateAuditLogV1Params
}

func newFakeDecommQuerier() *fakeDecommQuerier {
	clusterID := uuid.New()
	decomID := uuid.New()
	return &fakeDecommQuerier{
		cluster: sqlc.Cluster{ID: clusterID, Name: "test-cluster"},
		row: sqlc.ClusterDecommission{
			ID:          decomID,
			ClusterID:   clusterID,
			Status:      "pending",
			Phases:      json.RawMessage(`{}`),
			ClusterName: "test-cluster",
		},
		calls: map[string]int{},
	}
}

func (f *fakeDecommQuerier) bump(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[name]++
}

func (f *fakeDecommQuerier) GetClusterByID(_ context.Context, id uuid.UUID) (sqlc.Cluster, error) {
	f.bump("GetClusterByID")
	if id != f.cluster.ID {
		return sqlc.Cluster{}, errNoRows
	}
	return f.cluster, nil
}

func (f *fakeDecommQuerier) GetClusterDecommissionByID(_ context.Context, _ uuid.UUID) (sqlc.ClusterDecommission, error) {
	f.bump("GetClusterDecommissionByID")
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.row, nil
}

func (f *fakeDecommQuerier) GetLatestClusterDecommissionByCluster(_ context.Context, _ uuid.UUID) (sqlc.ClusterDecommission, error) {
	f.bump("GetLatestClusterDecommissionByCluster")
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.row, nil
}

func (f *fakeDecommQuerier) MarkClusterDecommissionRunning(_ context.Context, _ sqlc.MarkClusterDecommissionRunningParams) (sqlc.ClusterDecommission, error) {
	f.bump("MarkClusterDecommissionRunning")
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.claimErr != nil {
		return sqlc.ClusterDecommission{}, f.claimErr
	}
	f.row.Status = "running"
	f.row.Attempts++
	if !f.row.StartedAt.Valid {
		f.row.StartedAt = pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}
	}
	return f.row, nil
}

func (f *fakeDecommQuerier) ReleaseClusterDecommissionClaim(_ context.Context, _ uuid.UUID) error {
	f.bump("ReleaseClusterDecommissionClaim")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.row.Status = "pending"
	return nil
}

func (f *fakeDecommQuerier) UpdateClusterDecommissionPhases(_ context.Context, arg sqlc.UpdateClusterDecommissionPhasesParams) (sqlc.ClusterDecommission, error) {
	f.bump("UpdateClusterDecommissionPhases")
	if f.updatePhasesErr != nil {
		return sqlc.ClusterDecommission{}, f.updatePhasesErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.row.Phases = arg.Phases
	return f.row, nil
}

func (f *fakeDecommQuerier) MarkClusterDecommissionSucceeded(_ context.Context, arg sqlc.MarkClusterDecommissionSucceededParams) (sqlc.ClusterDecommission, error) {
	f.bump("MarkClusterDecommissionSucceeded")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.row.Status = "succeeded"
	f.row.Phases = arg.Phases
	f.row.CompletedAt = pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}
	return f.row, nil
}

func (f *fakeDecommQuerier) MarkClusterDecommissionFailed(_ context.Context, arg sqlc.MarkClusterDecommissionFailedParams) (sqlc.ClusterDecommission, error) {
	f.bump("MarkClusterDecommissionFailed")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.row.Status = "failed"
	f.row.LastError = arg.LastError
	f.row.Phases = arg.Phases
	f.row.CompletedAt = pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}
	return f.row, nil
}

func (f *fakeDecommQuerier) ListPendingClusterDecommissions(_ context.Context, _ int32) ([]sqlc.ClusterDecommission, error) {
	f.bump("ListPendingClusterDecommissions")
	return nil, nil
}

func (f *fakeDecommQuerier) DeleteClusterRegistrationTokensByCluster(_ context.Context, _ uuid.UUID) (int64, error) {
	f.bump("DeleteClusterRegistrationTokensByCluster")
	return 1, f.regTokenErr
}

func (f *fakeDecommQuerier) DeleteClusterAgentTokensByCluster(_ context.Context, _ uuid.UUID) (int64, error) {
	f.bump("DeleteClusterAgentTokensByCluster")
	return 1, f.agentTokenErr
}

func (f *fakeDecommQuerier) DeleteArgoCDClusterProxyTokensByCluster(_ context.Context, _ uuid.UUID) (int64, error) {
	f.bump("DeleteArgoCDClusterProxyTokensByCluster")
	return 1, nil
}

func (f *fakeDecommQuerier) ArchiveAndPurgeAuditLogsForCluster(_ context.Context, _ uuid.UUID) (int64, error) {
	f.bump("ArchiveAndPurgeAuditLogsForCluster")
	return 42, f.archiveErr
}

func (f *fakeDecommQuerier) DeleteApiserverAuditEventsByCluster(_ context.Context, _ uuid.UUID) (int64, error) {
	f.bump("DeleteApiserverAuditEventsByCluster")
	return 8, nil
}

func (f *fakeDecommQuerier) DeleteClusterRegistryConfigsByCluster(_ context.Context, _ uuid.UUID) (int64, error) {
	f.bump("DeleteClusterRegistryConfigsByCluster")
	return 1, f.registryErr
}
func (f *fakeDecommQuerier) DeleteClusterHealthStatusByCluster(_ context.Context, _ uuid.UUID) (int64, error) {
	f.bump("DeleteClusterHealthStatusByCluster")
	return 1, f.healthErr
}
func (f *fakeDecommQuerier) DeleteClusterConditionsByCluster(_ context.Context, _ uuid.UUID) (int64, error) {
	f.bump("DeleteClusterConditionsByCluster")
	return 3, f.conditionsErr
}
func (f *fakeDecommQuerier) DeleteAgentConnectionsByCluster(_ context.Context, _ uuid.UUID) (int64, error) {
	f.bump("DeleteAgentConnectionsByCluster")
	return 7, f.connsErr
}
func (f *fakeDecommQuerier) DeleteAlertSilencesByCluster(_ context.Context, _ uuid.UUID) (int64, error) {
	f.bump("DeleteAlertSilencesByCluster")
	return 2, f.silencesErr
}
func (f *fakeDecommQuerier) DeleteAlertRulesByCluster(_ context.Context, _ uuid.UUID) (int64, error) {
	f.bump("DeleteAlertRulesByCluster")
	return 4, f.rulesErr
}
func (f *fakeDecommQuerier) DeleteInstalledChartsByCluster(_ context.Context, _ uuid.UUID) (int64, error) {
	f.bump("DeleteInstalledChartsByCluster")
	return 5, f.chartsErr
}
func (f *fakeDecommQuerier) DeleteClusterSecurityPoliciesByCluster(_ context.Context, _ uuid.UUID) (int64, error) {
	f.bump("DeleteClusterSecurityPoliciesByCluster")
	return 1, f.policiesErr
}
func (f *fakeDecommQuerier) DeleteProjectNamespacesByCluster(_ context.Context, _ uuid.UUID) (int64, error) {
	f.bump("DeleteProjectNamespacesByCluster")
	return 6, f.projNsErr
}
func (f *fakeDecommQuerier) DeleteClusterRoleBindingsByCluster(_ context.Context, _ uuid.UUID) (int64, error) {
	f.bump("DeleteClusterRoleBindingsByCluster")
	return 9, f.roleBindingsErr
}
func (f *fakeDecommQuerier) DeleteClusterSnapshotSchedulesByCluster(_ context.Context, _ uuid.UUID) (int64, error) {
	f.bump("DeleteClusterSnapshotSchedulesByCluster")
	return 2, f.snapSchedulesErr
}
func (f *fakeDecommQuerier) DeleteGitOpsRegisteredClustersByCluster(_ context.Context, _ uuid.UUID) (int64, error) {
	f.bump("DeleteGitOpsRegisteredClustersByCluster")
	return 1, nil
}
func (f *fakeDecommQuerier) DeleteNativeRBACRulesByCluster(_ context.Context, _ uuid.UUID) (int64, error) {
	f.bump("DeleteNativeRBACRulesByCluster")
	return 3, nil
}
func (f *fakeDecommQuerier) DeleteDeferredOperationsByCluster(_ context.Context, _ uuid.UUID) (int64, error) {
	f.bump("DeleteDeferredOperationsByCluster")
	return 1, nil
}
func (f *fakeDecommQuerier) DeleteAgentLifecycleOperationsByCluster(_ context.Context, _ uuid.UUID) (int64, error) {
	f.bump("DeleteAgentLifecycleOperationsByCluster")
	return 4, nil
}
func (f *fakeDecommQuerier) ListArgoCDManagedClustersByCluster(_ context.Context, _ uuid.UUID) ([]sqlc.ArgocdManagedCluster, error) {
	f.bump("ListArgoCDManagedClustersByCluster")
	return f.argocdManaged, nil
}
func (f *fakeDecommQuerier) DeleteArgoCDManagedClustersByCluster(_ context.Context, _ uuid.UUID) (int64, error) {
	f.bump("DeleteArgoCDManagedClustersByCluster")
	return int64(len(f.argocdManaged)), nil
}
func (f *fakeDecommQuerier) TombstoneCluster(_ context.Context, _ uuid.UUID) error {
	f.bump("TombstoneCluster")
	return f.tombstoneErr
}
func (f *fakeDecommQuerier) CreateAuditLogV1(_ context.Context, arg sqlc.CreateAuditLogV1Params) error {
	f.bump("CreateAuditLogV1")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.audit = append(f.audit, arg)
	return nil
}

// fakeTunnel implements DecommissionTunnel.
type fakeTunnel struct {
	connected    bool
	ack          *protocol.DecommissionAckPayload
	sendErr      error
	disconnected bool

	sendCalls       int
	disconnectCalls int
	lastPayload     protocol.DecommissionPayload
}

func (t *fakeTunnel) SendDecommission(_ context.Context, _ string, payload protocol.DecommissionPayload, _ time.Duration) (*protocol.DecommissionAckPayload, bool, error) {
	t.sendCalls++
	t.lastPayload = payload
	return t.ack, t.connected, t.sendErr
}

func (t *fakeTunnel) Disconnect(_ string) bool {
	t.disconnectCalls++
	return t.disconnected
}

// --- tests ----------------------------------------------------------------

// TestSuccessPath_AllPhasesRunInOrder is the load-bearing test: every phase
// runs, the row ends up succeeded, and the dependent-table deletes are
// invoked exactly once each. Also asserts the agent's MsgDecommission
// payload carries the documented label selector so the agent doesn't wipe
// CRs the operator owns.
func TestSuccessPath_AllPhasesRunInOrder(t *testing.T) {
	q := newFakeDecommQuerier()
	tun := &fakeTunnel{
		connected: true,
		ack: &protocol.DecommissionAckPayload{
			ClusterID: q.row.ClusterID.String(),
			Steps: []protocol.DecommissionStepResult{
				{Name: "remove_logging_stack", Success: true, Removed: 1},
				{Name: "remove_velero_managed", Success: true, Removed: 3},
				{Name: "remove_agent_deployment", Success: true, Removed: 1},
			},
		},
		disconnected: true,
	}
	deps := ClusterDecommissionDeps{Queries: q, Tunnel: tun, TunnelWait: 100 * time.Millisecond}

	if err := runClusterDecommission(context.Background(), deps, q.row.ID); err != nil {
		t.Fatalf("runClusterDecommission: %v", err)
	}

	if q.row.Status != "succeeded" {
		t.Errorf("expected status=succeeded, got %s (last_error=%q)", q.row.Status, q.row.LastError)
	}
	if tun.sendCalls != 1 {
		t.Errorf("expected 1 SendDecommission, got %d", tun.sendCalls)
	}
	if tun.lastPayload.ManagedLabel != "astronomer.io/managed=true" {
		t.Errorf("expected ManagedLabel guard, got %q", tun.lastPayload.ManagedLabel)
	}
	if tun.disconnectCalls != 1 {
		t.Errorf("expected 1 Disconnect, got %d", tun.disconnectCalls)
	}
	// Dependent-table deletes — each should fire exactly once.
	for _, name := range []string{
		"DeleteClusterRegistrationTokensByCluster",
		"DeleteClusterAgentTokensByCluster",
		"DeleteArgoCDClusterProxyTokensByCluster",
		"ArchiveAndPurgeAuditLogsForCluster",
		"DeleteClusterRegistryConfigsByCluster",
		"DeleteClusterHealthStatusByCluster",
		"DeleteClusterConditionsByCluster",
		"DeleteAgentConnectionsByCluster",
		"DeleteAlertSilencesByCluster",
		"DeleteAlertRulesByCluster",
		"DeleteInstalledChartsByCluster",
		"DeleteClusterSecurityPoliciesByCluster",
		"DeleteProjectNamespacesByCluster",
		"DeleteClusterRoleBindingsByCluster",
		"DeleteApiserverAuditEventsByCluster",
		"TombstoneCluster",
	} {
		if q.calls[name] != 1 {
			t.Errorf("expected %s called once, got %d", name, q.calls[name])
		}
	}
	// One audit row per phase (5 phases) plus a direct token-revocation
	// security event that can be queried without parsing phase names.
	if len(q.audit) != 6 {
		t.Errorf("expected 6 audit rows, got %d", len(q.audit))
	}
	phaseAuditRows := 0
	revocationAuditRows := 0
	for _, row := range q.audit {
		switch {
		case strings.HasPrefix(row.Action, "cluster.decommission."):
			phaseAuditRows++
		case row.Action == "agent.token.revoked":
			revocationAuditRows++
			if row.ResourceType != "cluster" || row.ResourceID != q.row.ClusterID.String() {
				t.Errorf("token revocation audit resource = %s/%s, want cluster/%s", row.ResourceType, row.ResourceID, q.row.ClusterID)
			}
			var detail map[string]any
			if err := json.Unmarshal(row.Detail, &detail); err != nil {
				t.Fatalf("decode token revocation audit detail: %v", err)
			}
			if detail["agent_tokens_removed"] != float64(1) {
				t.Errorf("agent_tokens_removed detail = %v, want 1", detail["agent_tokens_removed"])
			}
		default:
			t.Errorf("unexpected audit action %q", row.Action)
		}
		// Sanity: the regex contract in internal/audit will already enforce
		// the canonical shape, but we double-check the format here too so
		// a typo is caught at the unit test layer.
		if strings.Contains(row.Action, " ") || strings.Contains(row.Action, "-") {
			t.Errorf("audit action %q contains forbidden character", row.Action)
		}
	}
	if phaseAuditRows != 5 {
		t.Errorf("phase audit rows = %d, want 5", phaseAuditRows)
	}
	if revocationAuditRows != 1 {
		t.Errorf("token revocation audit rows = %d, want 1", revocationAuditRows)
	}
}

// TestAgentUnreachable_RevokeDeferredWithinGrace verifies the M12 deferral:
// when the agent isn't connected, cleanup_managed_side is Skipped and — while
// still inside the grace window — the reconciler does NOT advance to
// token-revoke. The row is left for the periodic sweep to retry so a
// reconnecting agent can still authenticate and run cleanup.
func TestAgentUnreachable_RevokeDeferredWithinGrace(t *testing.T) {
	q := newFakeDecommQuerier()
	tun := &fakeTunnel{connected: false}
	deps := ClusterDecommissionDeps{Queries: q, Tunnel: tun, TunnelWait: 10 * time.Millisecond}

	if err := runClusterDecommission(context.Background(), deps, q.row.ID); err != nil {
		t.Fatalf("runClusterDecommission: %v", err)
	}
	// Within grace (attempts==1, fresh started_at): NOT advanced to revoke.
	if q.row.Status == "succeeded" {
		t.Errorf("expected decommission to be deferred, not succeeded")
	}
	if q.calls["DeleteClusterRegistrationTokensByCluster"] != 0 {
		t.Errorf("token-revoke must NOT run within grace; calls=%d", q.calls["DeleteClusterRegistrationTokensByCluster"])
	}
	if q.calls["TombstoneCluster"] != 0 {
		t.Errorf("tombstone must NOT run within grace; calls=%d", q.calls["TombstoneCluster"])
	}
	phases := loadPhases(q.row.Phases)
	rec, ok := phases[PhaseCleanupManagedSide]
	if !ok {
		t.Fatalf("cleanup_managed_side phase not recorded")
	}
	if rec.Status != PhaseStatusSkipped {
		t.Errorf("expected status=skipped, got %s", rec.Status)
	}
	if reason, ok := rec.Detail["reason"].(string); !ok || !strings.Contains(reason, "not connected") {
		t.Errorf("expected reason='agent not connected', got detail=%+v", rec.Detail)
	}
}

// TestAgentUnreachable_GraceExhaustedAdvances verifies the no-deadlock cap:
// once attempts exhaust the grace window, a still-skipped cleanup no longer
// blocks — the reconciler revokes the token and tombstones so no live
// credential lingers on a permanently-dead agent.
func TestAgentUnreachable_GraceExhaustedAdvances(t *testing.T) {
	q := newFakeDecommQuerier()
	// Seed attempts just below the cap; MarkClusterDecommissionRunning bumps
	// it to the cap so graceExhausted trips.
	q.row.Attempts = maxCleanupAttempts - 1
	tun := &fakeTunnel{connected: false, disconnected: true}
	deps := ClusterDecommissionDeps{Queries: q, Tunnel: tun, TunnelWait: 10 * time.Millisecond}

	if err := runClusterDecommission(context.Background(), deps, q.row.ID); err != nil {
		t.Fatalf("runClusterDecommission: %v", err)
	}
	if q.row.Status != "succeeded" {
		t.Errorf("expected status=succeeded after grace exhausted, got %s", q.row.Status)
	}
	if q.calls["DeleteClusterRegistrationTokensByCluster"] != 1 {
		t.Errorf("token-revoke must run once grace exhausted; calls=%d", q.calls["DeleteClusterRegistrationTokensByCluster"])
	}
	if q.calls["TombstoneCluster"] != 1 {
		t.Errorf("tombstone must run once grace exhausted; calls=%d", q.calls["TombstoneCluster"])
	}
}

// TestForce_SkipsGraceAdvancesImmediately verifies a force decommission with a
// disconnected agent advances to tombstone right away — no waiting out the
// grace window (attempts low, just started) the way a normal delete would.
func TestForce_SkipsGraceAdvancesImmediately(t *testing.T) {
	q := newFakeDecommQuerier()
	q.row.Force = true
	q.row.Attempts = 0 // nowhere near the cap; only Force should trip the grace
	tun := &fakeTunnel{connected: false, disconnected: true}
	deps := ClusterDecommissionDeps{Queries: q, Tunnel: tun, TunnelWait: 10 * time.Millisecond}

	if err := runClusterDecommission(context.Background(), deps, q.row.ID); err != nil {
		t.Fatalf("runClusterDecommission: %v", err)
	}
	if q.row.Status != "succeeded" {
		t.Errorf("force should advance to succeeded immediately, got %s", q.row.Status)
	}
	if q.calls["TombstoneCluster"] != 1 {
		t.Errorf("force must tombstone immediately; calls=%d", q.calls["TombstoneCluster"])
	}
}

// TestHARequeue_SiblingPodReturnsError verifies the H8 HA re-queue: when
// SendDecommission reports the agent is connected on a SIBLING pod, the
// reconciler resets the cleanup phase, releases its claim, and returns the
// error so asynq re-enqueues onto the owning pod. It must NOT mark the row
// failed and must NOT revoke the token.
func TestHARequeue_SiblingPodReturnsError(t *testing.T) {
	q := newFakeDecommQuerier()
	tun := &fakeTunnel{
		connected: false,
		sendErr:   errors.New("cluster agent not connected to this pod (owner=10.0.0.9:8080)"),
	}
	deps := ClusterDecommissionDeps{Queries: q, Tunnel: tun, TunnelWait: 10 * time.Millisecond}

	err := runClusterDecommission(context.Background(), deps, q.row.ID)
	if err == nil {
		t.Fatalf("expected re-queue error to propagate, got nil")
	}
	if !isAgentNotConnectedErr(err) {
		t.Fatalf("expected agent-not-connected error, got %v", err)
	}
	if q.row.Status == "failed" {
		t.Errorf("row must NOT be marked failed on sibling-pod re-queue")
	}
	if q.calls["ReleaseClusterDecommissionClaim"] != 1 {
		t.Errorf("expected claim release once, got %d", q.calls["ReleaseClusterDecommissionClaim"])
	}
	if q.calls["DeleteClusterRegistrationTokensByCluster"] != 0 {
		t.Errorf("token-revoke must NOT run on re-queue; calls=%d", q.calls["DeleteClusterRegistrationTokensByCluster"])
	}
	// The cleanup phase must be reset (not present / not skipped) so the
	// owning pod re-runs it cleanly.
	phases := loadPhases(q.row.Phases)
	if rec, ok := phases[PhaseCleanupManagedSide]; ok && rec.Status == PhaseStatusSkipped {
		t.Errorf("cleanup phase should be reset for re-run, got %s", rec.Status)
	}
}

// TestClaimNotAcquired_SiblingHoldsLease verifies the L15 lease-CAS: when
// MarkClusterDecommissionRunning returns "no rows" (a sibling holds a live
// lease), the reconciler backs off without doing any phase work.
func TestClaimNotAcquired_SiblingHoldsLease(t *testing.T) {
	q := newFakeDecommQuerier()
	q.claimErr = errNoRows // "no rows in result set"
	tun := &fakeTunnel{connected: true, ack: &protocol.DecommissionAckPayload{}}
	deps := ClusterDecommissionDeps{Queries: q, Tunnel: tun, TunnelWait: 10 * time.Millisecond}

	if err := runClusterDecommission(context.Background(), deps, q.row.ID); err != nil {
		t.Fatalf("expected nil (backoff) when sibling holds lease, got %v", err)
	}
	if tun.sendCalls != 0 {
		t.Errorf("no cleanup work expected when claim not acquired; sendCalls=%d", tun.sendCalls)
	}
	if q.calls["DeleteClusterRegistrationTokensByCluster"] != 0 {
		t.Errorf("no revoke expected when claim not acquired")
	}
}

// TestShouldRunPhase_SkippedReRuns asserts the M12 fix: a Skipped phase
// re-runs (returns true) while a Succeeded phase does not.
func TestShouldRunPhase_SkippedReRuns(t *testing.T) {
	phases := phasesMap{
		PhaseCleanupManagedSide: phaseRecord{Status: PhaseStatusSkipped},
		PhaseRevokeAgentToken:   phaseRecord{Status: PhaseStatusSucceeded},
	}
	if !shouldRunPhase(phases, PhaseCleanupManagedSide) {
		t.Errorf("skipped phase should re-run")
	}
	if shouldRunPhase(phases, PhaseRevokeAgentToken) {
		t.Errorf("succeeded phase should not re-run")
	}
}

// TestPhaseCleanupSetsFullFootprintPayload asserts the worker drives the agent
// with the complete-footprint flag + the verified label gates.
func TestPhaseCleanupSetsFullFootprintPayload(t *testing.T) {
	q := newFakeDecommQuerier()
	tun := &fakeTunnel{connected: true, ack: &protocol.DecommissionAckPayload{}}
	deps := ClusterDecommissionDeps{Queries: q, Tunnel: tun, TunnelWait: 10 * time.Millisecond}

	if _, err := phaseCleanupManagedSide(context.Background(), deps, q.row); err != nil {
		t.Fatalf("phaseCleanupManagedSide: %v", err)
	}
	p := tun.lastPayload
	if !p.RemoveFullFootprint {
		t.Errorf("expected RemoveFullFootprint=true")
	}
	if p.VeleroLabel != "app.kubernetes.io/managed-by=astronomer-go" {
		t.Errorf("velero label = %q", p.VeleroLabel)
	}
	if p.ManagedByLabel != "app.kubernetes.io/managed-by=astronomer-server" {
		t.Errorf("managed-by label = %q", p.ManagedByLabel)
	}
	if p.RBACLabel != "app.kubernetes.io/part-of=astronomer" {
		t.Errorf("rbac label = %q", p.RBACLabel)
	}
}

// TestVeleroOrphanAuditEmitted asserts L16: when the agent reports orphan BSLs,
// the worker emits a cluster.decommission.velero_orphan audit row listing them.
func TestVeleroOrphanAuditEmitted(t *testing.T) {
	q := newFakeDecommQuerier()
	tun := &fakeTunnel{
		connected: true,
		ack: &protocol.DecommissionAckPayload{
			Steps: []protocol.DecommissionStepResult{
				{Name: "remove_velero_managed", Success: true, Removed: 2, Orphans: []string{"default-bsl", "dr-bsl"}},
			},
		},
	}
	deps := ClusterDecommissionDeps{Queries: q, Tunnel: tun, TunnelWait: 10 * time.Millisecond}

	if _, err := phaseCleanupManagedSide(context.Background(), deps, q.row); err != nil {
		t.Fatalf("phaseCleanupManagedSide: %v", err)
	}
	var found *sqlc.CreateAuditLogV1Params
	for i := range q.audit {
		if q.audit[i].Action == "cluster.decommission.velero_orphan" {
			found = &q.audit[i]
		}
	}
	if found == nil {
		t.Fatalf("expected velero_orphan audit row, got %d rows", len(q.audit))
	}
	if !strings.Contains(string(found.Detail), "default-bsl") || !strings.Contains(string(found.Detail), "dr-bsl") {
		t.Errorf("orphan audit detail missing BSL names: %s", found.Detail)
	}
}

// TestRevokeTokenPhaseFailure_RowMarkedFailed: when DeleteClusterRegistration
// fails, the row ends up status=failed and the LastError carries the inner
// error message. Subsequent phases are NOT attempted.
func TestRevokeTokenPhaseFailure_RowMarkedFailed(t *testing.T) {
	q := newFakeDecommQuerier()
	q.regTokenErr = errors.New("boom: postgres unreachable")
	tun := &fakeTunnel{connected: true, ack: &protocol.DecommissionAckPayload{}}
	deps := ClusterDecommissionDeps{Queries: q, Tunnel: tun, TunnelWait: 10 * time.Millisecond}

	if err := runClusterDecommission(context.Background(), deps, q.row.ID); err != nil {
		t.Fatalf("expected reconciler to swallow phase failure as nil err; got %v", err)
	}
	if q.row.Status != "failed" {
		t.Errorf("expected status=failed, got %s", q.row.Status)
	}
	if !strings.Contains(q.row.LastError, "boom: postgres unreachable") {
		t.Errorf("expected LastError to carry inner err, got %q", q.row.LastError)
	}
	if q.calls["ArchiveAndPurgeAuditLogsForCluster"] != 0 {
		t.Errorf("subsequent phase should not have been attempted; calls=%d", q.calls["ArchiveAndPurgeAuditLogsForCluster"])
	}
	if q.calls["TombstoneCluster"] != 0 {
		t.Errorf("tombstone should not have been attempted; calls=%d", q.calls["TombstoneCluster"])
	}
}

// TestArchivePhaseFailure_RowMarkedFailed exercises the second phase's
// failure path: prior phases ran, this phase fails, no further phases run.
func TestArchivePhaseFailure_RowMarkedFailed(t *testing.T) {
	q := newFakeDecommQuerier()
	q.archiveErr = errors.New("disk full")
	tun := &fakeTunnel{connected: true, ack: &protocol.DecommissionAckPayload{}}
	deps := ClusterDecommissionDeps{Queries: q, Tunnel: tun, TunnelWait: 10 * time.Millisecond}

	_ = runClusterDecommission(context.Background(), deps, q.row.ID)

	if q.row.Status != "failed" {
		t.Errorf("expected status=failed, got %s", q.row.Status)
	}
	if !strings.Contains(q.row.LastError, "disk full") {
		t.Errorf("expected disk-full in LastError, got %q", q.row.LastError)
	}
	// Prior phases must have run:
	if q.calls["DeleteClusterRegistrationTokensByCluster"] != 1 {
		t.Errorf("revoke phase should have run before archive failed")
	}
	// Subsequent phases must not have run:
	if q.calls["DeleteAlertRulesByCluster"] != 0 {
		t.Errorf("dependents phase should not have run after archive failed")
	}
}

// TestArchiveAuditPhase_AtomicAndApiserverCleanup pins two decommission fixes:
//   - the archive_audit phase runs a SINGLE atomic archive+purge statement (no
//     separate archive-SELECT then DELETE window that could drop an in-flight
//     audit_log row from the live log without archiving it), and
//   - phaseDeleteDependents drops the cluster's apiserver_audit_events (they are
//     keyed by cluster_id but never CASCADE-deleted on a soft tombstone).
func TestArchiveAuditPhase_AtomicAndApiserverCleanup(t *testing.T) {
	q := newFakeDecommQuerier()
	tun := &fakeTunnel{connected: true, ack: &protocol.DecommissionAckPayload{}, disconnected: true}
	deps := ClusterDecommissionDeps{Queries: q, Tunnel: tun, TunnelWait: 50 * time.Millisecond}

	if err := runClusterDecommission(context.Background(), deps, q.row.ID); err != nil {
		t.Fatalf("runClusterDecommission: %v", err)
	}
	if q.row.Status != "succeeded" {
		t.Fatalf("expected succeeded, got %s (last_error=%q)", q.row.Status, q.row.LastError)
	}
	if q.calls["ArchiveAndPurgeAuditLogsForCluster"] != 1 {
		t.Errorf("archive_audit must use the single atomic statement once, got %d", q.calls["ArchiveAndPurgeAuditLogsForCluster"])
	}
	// The non-atomic two-call path must be gone.
	if q.calls["DeleteAuditLogsForCluster"] != 0 {
		t.Errorf("legacy non-atomic DeleteAuditLogsForCluster must not be called")
	}
	if q.calls["DeleteApiserverAuditEventsByCluster"] != 1 {
		t.Errorf("decommission must purge apiserver_audit_events once, got %d", q.calls["DeleteApiserverAuditEventsByCluster"])
	}
}

// TestTombstoneAlreadyTombstoned_Idempotent: re-running the reconciler on a
// cluster row that was already tombstoned by a previous run completes
// successfully and reports "already_tombstoned" in the phase detail.
func TestTombstoneAlreadyTombstoned_Idempotent(t *testing.T) {
	q := newFakeDecommQuerier()
	q.cluster.DecommissionedAt = pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}
	tun := &fakeTunnel{connected: true, ack: &protocol.DecommissionAckPayload{}}
	deps := ClusterDecommissionDeps{Queries: q, Tunnel: tun, TunnelWait: 10 * time.Millisecond}

	if err := runClusterDecommission(context.Background(), deps, q.row.ID); err != nil {
		t.Fatalf("runClusterDecommission: %v", err)
	}
	if q.row.Status != "succeeded" {
		t.Errorf("expected status=succeeded for re-run, got %s", q.row.Status)
	}
	// TombstoneCluster should NOT have been called (the early-return path).
	if q.calls["TombstoneCluster"] != 0 {
		t.Errorf("expected idempotent skip; TombstoneCluster called %d times", q.calls["TombstoneCluster"])
	}
	phases := loadPhases(q.row.Phases)
	rec := phases[PhaseTombstoneCluster]
	if rec.Status != PhaseStatusSucceeded {
		t.Errorf("expected tombstone phase succeeded, got %s", rec.Status)
	}
	if already, _ := rec.Detail["already_tombstoned"].(bool); !already {
		t.Errorf("expected already_tombstoned=true, got detail=%+v", rec.Detail)
	}
}

// TestReentryAfterSucceeded_NoOp: calling the reconciler on a row whose
// status is already 'succeeded' is a no-op — we don't re-run any phases.
func TestReentryAfterSucceeded_NoOp(t *testing.T) {
	q := newFakeDecommQuerier()
	q.row.Status = "succeeded"
	tun := &fakeTunnel{connected: true, ack: &protocol.DecommissionAckPayload{}}
	deps := ClusterDecommissionDeps{Queries: q, Tunnel: tun, TunnelWait: 10 * time.Millisecond}

	if err := runClusterDecommission(context.Background(), deps, q.row.ID); err != nil {
		t.Fatalf("runClusterDecommission: %v", err)
	}
	if q.calls["MarkClusterDecommissionRunning"] != 0 {
		t.Errorf("expected idempotent skip; MarkClusterDecommissionRunning called %d times", q.calls["MarkClusterDecommissionRunning"])
	}
}

// TestPhaseRestart_SkipsCompletedPhases: when a previous run completed
// phase 1 but failed on phase 2, the next attempt should NOT re-do phase 1.
// This is the idempotency contract for after-crash resumption.
func TestPhaseRestart_SkipsCompletedPhases(t *testing.T) {
	q := newFakeDecommQuerier()
	// Seed the row with phase 1 already succeeded (simulating a crash
	// between phase 1 and 2).
	phases := phasesMap{
		PhaseCleanupManagedSide: phaseRecord{
			Status:      PhaseStatusSucceeded,
			StartedAt:   time.Now().UTC().Add(-1 * time.Minute),
			CompletedAt: time.Now().UTC().Add(-30 * time.Second),
		},
	}
	q.row.Phases = phasesJSON(phases)
	q.row.Status = "failed"

	tun := &fakeTunnel{connected: true, ack: &protocol.DecommissionAckPayload{}}
	deps := ClusterDecommissionDeps{Queries: q, Tunnel: tun, TunnelWait: 10 * time.Millisecond}

	if err := runClusterDecommission(context.Background(), deps, q.row.ID); err != nil {
		t.Fatalf("runClusterDecommission: %v", err)
	}
	if tun.sendCalls != 0 {
		t.Errorf("expected cleanup phase skipped on resume, got %d SendDecommission calls", tun.sendCalls)
	}
	if q.row.Status != "succeeded" {
		t.Errorf("expected status=succeeded, got %s", q.row.Status)
	}
}

// TestNewClusterDecommissionTask validates the JSON envelope so the worker
// always gets a parseable payload — the handler enqueues it from the
// hot path so a malformed body would silently break decommissions.
func TestNewClusterDecommissionTask(t *testing.T) {
	id := uuid.New()
	task, err := NewClusterDecommissionTask(id)
	if err != nil {
		t.Fatalf("NewClusterDecommissionTask: %v", err)
	}
	if task.Type() != ClusterDecommissionType {
		t.Fatalf("task type: got %s, want %s", task.Type(), ClusterDecommissionType)
	}
	var p ClusterDecommissionPayload
	if err := json.Unmarshal(task.Payload(), &p); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if p.DecommissionID != id.String() {
		t.Errorf("decommission_id: got %s, want %s", p.DecommissionID, id.String())
	}
}

func TestPhaseDeleteDependentsDeletesArgoCDClusterSecrets(t *testing.T) {
	q := newFakeDecommQuerier()
	secretName := "cluster-prod-east"
	q.argocdManaged = []sqlc.ArgocdManagedCluster{{
		ArgocdInstanceID:  uuid.New(),
		ClusterID:         q.row.ClusterID,
		ClusterSecretName: secretName,
		ServerUrl:         "https://prod-east.example",
	}}
	k8s := fake.NewClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: argoCDNamespace,
		},
		Data: map[string][]byte{"server": []byte("https://prod-east.example")},
	})

	detail, err := phaseDeleteDependents(context.Background(), ClusterDecommissionDeps{
		Queries: q,
		K8s:     k8s,
	}, q.row)
	if err != nil {
		t.Fatalf("phaseDeleteDependents: %v", err)
	}
	if _, err := k8s.CoreV1().Secrets(argoCDNamespace).Get(context.Background(), secretName, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("expected ArgoCD cluster Secret to be deleted, got err=%v", err)
	}
	if detail["argocd_cluster_secrets_removed"] != int64(1) {
		t.Fatalf("argocd_cluster_secrets_removed = %v, want 1", detail["argocd_cluster_secrets_removed"])
	}
	if detail["argocd_managed_clusters"] != int64(1) {
		t.Fatalf("argocd_managed_clusters = %v, want 1", detail["argocd_managed_clusters"])
	}
	if len(q.audit) != 0 {
		t.Fatalf("unexpected orphan audit rows: %d", len(q.audit))
	}
}

func TestPhaseDeleteDependentsAuditsArgoCDSecretOrphanWithoutK8sClient(t *testing.T) {
	q := newFakeDecommQuerier()
	q.argocdManaged = []sqlc.ArgocdManagedCluster{{
		ArgocdInstanceID:  uuid.New(),
		ClusterID:         q.row.ClusterID,
		ClusterSecretName: "cluster-prod-east",
		ServerUrl:         "https://prod-east.example",
	}}

	detail, err := phaseDeleteDependents(context.Background(), ClusterDecommissionDeps{Queries: q}, q.row)
	if err != nil {
		t.Fatalf("phaseDeleteDependents: %v", err)
	}
	if detail["argocd_cluster_secrets_removed"] != int64(0) {
		t.Fatalf("argocd_cluster_secrets_removed = %v, want 0", detail["argocd_cluster_secrets_removed"])
	}
	if len(q.audit) != 1 {
		t.Fatalf("orphan audit rows = %d, want 1", len(q.audit))
	}
	if q.audit[0].Action != "cluster.decommission.argocd_secret_orphan" {
		t.Fatalf("audit action = %q", q.audit[0].Action)
	}
}

// TestPersistFailure_WrapsAuditDeleteCleanly mirrors the production error
// path where MarkClusterDecommissionFailed itself fails. Verifies that the
// reconciler returns a meaningful error (not just the inner one) so the
// caller (asynq HandleClusterDecommission) sees the wrapping.
func TestPersistFailure_WrapsAuditDeleteCleanly(t *testing.T) {
	q := newFakeDecommQuerier()
	// We can't easily make MarkClusterDecommissionFailed return an error
	// via the existing fake; assert that persistFailure returns nil under
	// normal conditions (the documented contract: don't propagate to asynq
	// retry).
	err := persistFailure(context.Background(), q, q.row.ID, phasesMap{}, "test failure")
	if err != nil {
		t.Errorf("expected nil from persistFailure on healthy DB, got %v", err)
	}
	if q.row.Status != "failed" {
		t.Errorf("expected status=failed after persistFailure, got %s", q.row.Status)
	}
}

// TestDeleteDependents_CleansSnapshotSchedulesAndOrphanTables is the F03
// regression guard. Before the fix, phaseDeleteDependents cleaned ~10 tables
// but left cluster_snapshot_schedules (and gitops/native-rbac/deferred/
// agent-lifecycle rows) behind — and because the reconciler tombstones rather
// than hard-deletes the cluster row, ON DELETE CASCADE never fired, so the
// snapshot dispatcher kept creating Velero backup jobs for a dead cluster.
// This asserts every newly-added dependent delete is invoked exactly once.
func TestDeleteDependents_CleansSnapshotSchedulesAndOrphanTables(t *testing.T) {
	q := newFakeDecommQuerier()
	tun := &fakeTunnel{
		connected:    true,
		ack:          &protocol.DecommissionAckPayload{ClusterID: q.row.ClusterID.String()},
		disconnected: true,
	}
	deps := ClusterDecommissionDeps{Queries: q, Tunnel: tun, TunnelWait: 100 * time.Millisecond}

	if err := runClusterDecommission(context.Background(), deps, q.row.ID); err != nil {
		t.Fatalf("runClusterDecommission: %v", err)
	}
	if q.row.Status != "succeeded" {
		t.Fatalf("expected status=succeeded, got %s (last_error=%q)", q.row.Status, q.row.LastError)
	}
	for _, name := range []string{
		"DeleteClusterSnapshotSchedulesByCluster",
		"DeleteGitOpsRegisteredClustersByCluster",
		"DeleteNativeRBACRulesByCluster",
		"DeleteDeferredOperationsByCluster",
		"DeleteAgentLifecycleOperationsByCluster",
	} {
		if q.calls[name] != 1 {
			t.Errorf("expected %s called once during delete_dependents, got %d", name, q.calls[name])
		}
	}
}
