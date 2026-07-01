// Package tasks: cluster decommission reconciler.
//
// The reconciler is enqueued from the cluster DELETE handler — the handler
// no longer hard-deletes the cluster row; instead it writes a
// cluster_decommissions row and enqueues a "cluster:decommission" task here.
//
// Phases (run in this order, each idempotent so re-runs are safe):
//
//  1. cleanup_managed_side — send a MsgDecommission over the tunnel asking
//     the agent to uninstall its own DaemonSets/Helm releases and remove
//     resources it labeled astronomer.io/managed=true. Must run BEFORE
//     revoke_agent_token because revoking the token tears the tunnel down.
//     Skipped (with a logged warning) when the agent is not connected.
//
//  2. revoke_agent_token — DELETE rows from cluster_registration_tokens and
//     cluster_agent_tokens, then forcibly close the WS tunnel via
//     hub.Disconnect. After this point any in-flight or future agent dial
//     attempts fail authentication.
//
//  3. archive_audit — INSERT INTO audit_archive SELECT FROM audit_log WHERE
//     cluster_id matches, then DELETE the same rows from audit_log. The
//     archive table is not partitioned and not subject to audit-retention
//     sweeps, so the cluster's history is preserved indefinitely.
//
//  4. delete_dependents — remove rows from every table that references
//     cluster_id (alert_rules, installed_charts, project_namespaces, …).
//     Each table's delete count is recorded in the phases JSONB so an
//     operator can see exactly what was removed. Catalog tables like
//     cluster_tools that hold built-in definitions (not per-cluster
//     rows) are NOT touched here — they're cluster-agnostic.
//
//  5. tombstone_cluster — set clusters.decommissioned_at = now(). We do NOT
//     hard-delete the cluster row: that would orphan audit_archive rows
//     (which reference cluster_id) and would silently break historical
//     dashboards. The row stays around with status='decommissioned'.
//
// Every phase emits a per-phase audit log entry (`cluster.decommission.*`)
// so the action history of the decommission itself is reconstructable from
// the live audit_log even after the cluster's older audit rows have been
// archived.
package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/kubeutil"
	"github.com/alphabravocompany/astronomer-go/internal/operationstate"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// ClusterDecommissionType is the asynq task type. Re-exported as
// worker.TypeClusterDecommission so the mux wiring stays consistent with the
// other reconciler tasks.
const ClusterDecommissionType = "cluster:decommission"

// ClusterDecommissionAllType is the periodic sweep that picks up
// decommissions whose worker process crashed mid-run. Re-enqueued every
// minute by the scheduler.
const ClusterDecommissionAllType = "cluster:decommission_all"

// Phase names — stable strings used as keys in the phases JSONB blob and as
// suffixes for audit actions. Kept here as constants so a typo doesn't
// silently break the idempotent skip path (which compares phase outcomes by
// exact string match).
const (
	PhaseCleanupManagedSide = "cleanup_managed_side"
	PhaseRevokeAgentToken   = "revoke_agent_token"
	PhaseArchiveAudit       = "archive_audit"
	PhaseDeleteDependents   = "delete_dependents"
	PhaseTombstoneCluster   = "tombstone_cluster"
)

// PhaseStatus values written to the phases JSONB.
const (
	PhaseStatusPending   = operationstate.Pending
	PhaseStatusRunning   = operationstate.Running
	PhaseStatusSucceeded = "succeeded"
	PhaseStatusFailed    = operationstate.Failed
	PhaseStatusSkipped   = "skipped"
)

// decommissionTunnelWaitDefault is how long we wait for the agent's
// MsgDecommissionAck before giving up and falling back to "agent unreachable"
// semantics. Long enough for a slow agent to uninstall a couple of helm
// releases serially; short enough that an entire cluster decommission isn't
// blocked by a stuck agent.
const decommissionTunnelWaitDefault = 30 * time.Second

// decommissionLeaseTTLSeconds is the lease TTL passed to the
// MarkClusterDecommissionRunning CAS. It must exceed the cleanup-ACK wait
// (decommissionTunnelWaitDefault, 30s) so the active runner — which renews the
// lease on every UpdateClusterDecommissionPhases — is never preempted mid-RPC.
const decommissionLeaseTTLSeconds = 120

// maxCleanupAttempts / cleanupGraceTimeout bound how long the reconciler waits
// for a disconnected/sibling-pod agent to come back and run managed-side
// cleanup before it gives up and ADVANCES to token-revoke (so a decommission
// can never deadlock on a dead agent). Either bound trips the grace window.
const (
	maxCleanupAttempts  = 10
	cleanupGraceTimeout = 15 * time.Minute
)

// claimNotAcquired reports whether a MarkClusterDecommissionRunning lease-CAS
// returned "no rows" — i.e. a sibling pod or the periodic sweep holds a live
// lease on this row. Detected by message so it works for both the production
// pgx.ErrNoRows and the test fakes' sentinel.
func claimNotAcquired(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no rows in result set")
}

// graceExhausted reports whether the managed-side cleanup grace window has
// elapsed (too many attempts, or the decommission has been running too long),
// at which point the reconciler advances past a still-skipped cleanup phase.
func graceExhausted(row sqlc.ClusterDecommission) bool {
	// Force-delete: the operator asserts the target cluster/agent is gone, so we
	// don't wait out the grace window for a reconnect — advance immediately once
	// the single cleanup attempt has been made (and skipped because the agent is
	// unreachable). Managed-side workloads may be orphaned; that's the explicit
	// trade the operator accepted by forcing.
	if row.Force {
		return true
	}
	if row.Attempts >= maxCleanupAttempts {
		return true
	}
	return row.StartedAt.Valid && time.Since(row.StartedAt.Time) > cleanupGraceTimeout
}

// cleanupSatisfied reports whether the reconciler may advance past the
// cleanup_managed_side phase to token-revoke: either cleanup actually
// succeeded, or it's skipped (agent gone) AND the grace window is exhausted.
func cleanupSatisfied(phases phasesMap, row sqlc.ClusterDecommission) bool {
	rec := phases[PhaseCleanupManagedSide]
	if rec.Status == PhaseStatusSucceeded {
		return true
	}
	return rec.Status == PhaseStatusSkipped && graceExhausted(row)
}

// ClusterDecommissionQuerier is the slice of *sqlc.Queries the reconciler
// needs. Defined locally so the unit tests can stand up a fake without
// dragging the full Queries surface in.
type ClusterDecommissionQuerier interface {
	GetClusterByID(ctx context.Context, id uuid.UUID) (sqlc.Cluster, error)
	GetClusterDecommissionByID(ctx context.Context, id uuid.UUID) (sqlc.ClusterDecommission, error)
	GetLatestClusterDecommissionByCluster(ctx context.Context, clusterID uuid.UUID) (sqlc.ClusterDecommission, error)
	// MarkClusterDecommissionRunning is a lease-CAS claim: it returns
	// pgx.ErrNoRows ("no rows in result set") when a sibling pod (or the
	// periodic sweep) already holds a live lease, so this runner backs off
	// instead of double-running the row.
	MarkClusterDecommissionRunning(ctx context.Context, arg sqlc.MarkClusterDecommissionRunningParams) (sqlc.ClusterDecommission, error)
	// ReleaseClusterDecommissionClaim flips status back to 'pending' so the
	// owning pod can re-claim during the HA re-queue path.
	ReleaseClusterDecommissionClaim(ctx context.Context, id uuid.UUID) error
	UpdateClusterDecommissionPhases(ctx context.Context, arg sqlc.UpdateClusterDecommissionPhasesParams) (sqlc.ClusterDecommission, error)
	MarkClusterDecommissionSucceeded(ctx context.Context, arg sqlc.MarkClusterDecommissionSucceededParams) (sqlc.ClusterDecommission, error)
	MarkClusterDecommissionFailed(ctx context.Context, arg sqlc.MarkClusterDecommissionFailedParams) (sqlc.ClusterDecommission, error)
	ListPendingClusterDecommissions(ctx context.Context, limit int32) ([]sqlc.ClusterDecommission, error)

	// Phase 1: managed-side cleanup is RPC-only, no DB writes here.

	// Phase 2: revoke tokens.
	DeleteClusterRegistrationTokensByCluster(ctx context.Context, clusterID uuid.UUID) (int64, error)
	DeleteClusterAgentTokensByCluster(ctx context.Context, clusterID uuid.UUID) (int64, error)
	DeleteArgoCDClusterProxyTokensByCluster(ctx context.Context, clusterID uuid.UUID) (int64, error)

	// Phase 3: archive then delete audit rows.
	ArchiveAuditLogsForCluster(ctx context.Context, arg sqlc.ArchiveAuditLogsForClusterParams) (int64, error)
	DeleteAuditLogsForCluster(ctx context.Context, clusterIDText string) (int64, error)

	// Phase 4: delete dependents.
	DeleteClusterRegistryConfigsByCluster(ctx context.Context, clusterID uuid.UUID) (int64, error)
	DeleteClusterHealthStatusByCluster(ctx context.Context, clusterID uuid.UUID) (int64, error)
	DeleteClusterConditionsByCluster(ctx context.Context, clusterID uuid.UUID) (int64, error)
	DeleteAgentConnectionsByCluster(ctx context.Context, clusterID uuid.UUID) (int64, error)
	DeleteAlertRulesByCluster(ctx context.Context, clusterID uuid.UUID) (int64, error)
	DeleteAlertSilencesByCluster(ctx context.Context, clusterID uuid.UUID) (int64, error)
	DeleteInstalledChartsByCluster(ctx context.Context, clusterID uuid.UUID) (int64, error)
	DeleteClusterSecurityPoliciesByCluster(ctx context.Context, clusterID uuid.UUID) (int64, error)
	DeleteProjectNamespacesByCluster(ctx context.Context, clusterID uuid.UUID) (int64, error)
	DeleteClusterRoleBindingsByCluster(ctx context.Context, clusterID uuid.UUID) (int64, error)
	// Additional dependent tables that hold a cluster_id FK but were never
	// cleaned on decommission — most importantly cluster_snapshot_schedules,
	// whose orphaned rows keep the snapshot dispatcher firing Velero jobs at a
	// dead cluster (the ListEnabledSnapshotSchedules due-query also grew a
	// decommissioned_at IS NULL guard as belt-and-suspenders).
	DeleteClusterSnapshotSchedulesByCluster(ctx context.Context, clusterID uuid.UUID) (int64, error)
	DeleteGitOpsRegisteredClustersByCluster(ctx context.Context, clusterID uuid.UUID) (int64, error)
	DeleteNativeRBACRulesByCluster(ctx context.Context, clusterID uuid.UUID) (int64, error)
	DeleteDeferredOperationsByCluster(ctx context.Context, clusterID uuid.UUID) (int64, error)
	DeleteAgentLifecycleOperationsByCluster(ctx context.Context, clusterID uuid.UUID) (int64, error)
	// Argo CD managed-cluster mappings + an enumerator so we can audit
	// the orphans (upstream Argo Secrets that need manual unregister).
	ListArgoCDManagedClustersByCluster(ctx context.Context, clusterID uuid.UUID) ([]sqlc.ArgocdManagedCluster, error)
	DeleteArgoCDManagedClustersByCluster(ctx context.Context, clusterID uuid.UUID) (int64, error)

	// Phase 5: tombstone (soft-delete) the cluster row.
	TombstoneCluster(ctx context.Context, id uuid.UUID) error

	// Audit writer for per-phase audit rows.
	CreateAuditLogV1(ctx context.Context, arg sqlc.CreateAuditLogV1Params) error
}

// DecommissionTunnel is the interface the reconciler uses to talk to the
// agent. Returns true if a connected agent existed for that cluster; the
// reconciler reads this flag to skip the managed-side cleanup phase
// gracefully (with a logged warning) when no agent is available.
type DecommissionTunnel interface {
	// SendDecommission sends a MsgDecommission to the agent and waits up to
	// `wait` for a MsgDecommissionAck. The returned bool is true if the
	// agent was connected at the moment of send; ack may be nil on timeout
	// or if the agent disconnected before responding.
	SendDecommission(ctx context.Context, clusterID string, payload protocol.DecommissionPayload, wait time.Duration) (ack *protocol.DecommissionAckPayload, connected bool, err error)
	// Disconnect forcibly closes the WS tunnel for the cluster. Returns
	// true if an agent was registered. Idempotent.
	Disconnect(clusterID string) bool
}

// RBACCacheInvalidator is implemented by the RBAC middleware's binding
// cache. The decommission phase that bulk-deletes cluster_role_bindings
// signals via InvalidateAll so stale entries don't linger up to the TTL on
// hot paths immediately after a cluster removal.
type RBACCacheInvalidator interface {
	InvalidateAll()
}

// ClusterDecommissionDeps wires the reconciler. Set once at server startup
// via ConfigureClusterDecommission; tests can swap a fake DecommissionTunnel.
type ClusterDecommissionDeps struct {
	Queries ClusterDecommissionQuerier
	Tunnel  DecommissionTunnel
	// K8s is the management-plane Kubernetes client used to unregister
	// upstream ArgoCD cluster Secrets during decommission. Optional; when nil
	// the worker still drops DB rows and emits orphan audit events.
	K8s kubernetes.Interface
	// TunnelWait is the per-call wait for MsgDecommissionAck. Defaults to
	// decommissionTunnelWaitDefault when zero.
	TunnelWait time.Duration
	// RBACCache is the per-user binding cache that must be flushed after the
	// bulk cluster_role_bindings delete in phaseDeleteDependents. Optional;
	// nil-safe.
	RBACCache RBACCacheInvalidator
}

var clusterDecommissionDeps ClusterDecommissionDeps

func ConfigureClusterDecommission(deps ClusterDecommissionDeps) {
	clusterDecommissionDeps = deps
}

// ResetClusterDecommission clears the runtime dependencies. Used by tests.
func ResetClusterDecommission() {
	clusterDecommissionDeps = ClusterDecommissionDeps{}
}

// ClusterDecommissionPayload is the asynq task body. The handler enqueues
// the row's ID; the worker re-loads the row to discover the cluster ID and
// the current phase state (so partial re-runs after a crash are idempotent).
type ClusterDecommissionPayload struct {
	DecommissionID string `json:"decommission_id"`
}

// NewClusterDecommissionTask builds an asynq task. Called by the cluster
// DELETE handler with the freshly-inserted cluster_decommissions row ID.
func NewClusterDecommissionTask(decommissionID uuid.UUID) (*asynq.Task, error) {
	body, err := json.Marshal(ClusterDecommissionPayload{DecommissionID: decommissionID.String()})
	if err != nil {
		return nil, fmt.Errorf("marshal cluster decommission payload: %w", err)
	}
	return asynq.NewTask(ClusterDecommissionType, body), nil
}

// NewClusterDecommissionAllTask returns the periodic-sweep task.
func NewClusterDecommissionAllTask() (*asynq.Task, error) {
	return asynq.NewTask(ClusterDecommissionAllType, nil), nil
}

// HandleClusterDecommission is the asynq handler. Loads the row, walks phases
// in order, and persists the outcome. Returns nil even on phase failure so
// asynq doesn't blindly retry the entire task — the row's status reflects the
// failure, and the periodic sweep picks failed rows up for re-runs.
func HandleClusterDecommission(ctx context.Context, t *asynq.Task) error {
	if clusterDecommissionDeps.Queries == nil {
		runtimeLogger().InfoContext(ctx, "cluster decommission runtime not configured, skipping")
		return nil
	}
	var p ClusterDecommissionPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("unmarshal cluster decommission payload: %w", err)
	}
	id, err := uuid.Parse(p.DecommissionID)
	if err != nil {
		return fmt.Errorf("invalid decommission_id: %w", err)
	}
	return runClusterDecommission(ctx, clusterDecommissionDeps, id)
}

// HandleClusterDecommissionAll is the periodic-sweep handler. Walks every
// pending/running row and re-runs the reconciler. Bounded by a fixed limit
// to avoid stampeding the DB after a long outage.
func HandleClusterDecommissionAll(ctx context.Context, _ *asynq.Task) error {
	return runPeriodicTaskWithLeader(ctx, ClusterDecommissionAllType, func() error {
		if clusterDecommissionDeps.Queries == nil {
			runtimeLogger().InfoContext(ctx, "cluster decommission runtime not configured, skipping sweep")
			return nil
		}
		rows, err := clusterDecommissionDeps.Queries.ListPendingClusterDecommissions(ctx, 50)
		if err != nil {
			return fmt.Errorf("list pending cluster decommissions: %w", err)
		}
		for _, row := range rows {
			if err := runClusterDecommission(ctx, clusterDecommissionDeps, row.ID); err != nil {
				runtimeLogger().WarnContext(ctx, "cluster decommission sweep step failed",
					"decommission_id", row.ID.String(),
					"cluster_id", row.ClusterID.String(),
					"error", err)
			}
		}
		return nil
	})
}

// phaseRecord is the per-phase entry stored in the phases JSONB blob.
type phaseRecord struct {
	Status      string    `json:"status"`
	StartedAt   time.Time `json:"started_at,omitempty"`
	CompletedAt time.Time `json:"completed_at,omitempty"`
	Error       string    `json:"error,omitempty"`
	// Detail is free-form per-phase metadata (e.g. rows-deleted-per-table
	// for delete_dependents, archived-count for archive_audit).
	Detail map[string]any `json:"detail,omitempty"`
}

// phasesMap is the structured representation of the JSONB blob.
type phasesMap map[string]phaseRecord

func loadPhases(raw json.RawMessage) phasesMap {
	if len(raw) == 0 {
		return phasesMap{}
	}
	out := phasesMap{}
	if err := json.Unmarshal(raw, &out); err != nil {
		// Corrupt blob — discard and start fresh; the worker will redo
		// every phase (which is idempotent).
		return phasesMap{}
	}
	return out
}

func phasesJSON(p phasesMap) json.RawMessage {
	raw, err := json.Marshal(p)
	if err != nil {
		// Should not happen: every value is a serializable struct. Fall
		// back to an empty object so the DB column stays non-null.
		return json.RawMessage(`{}`)
	}
	return raw
}

// runClusterDecommission is the inner reconciler — separated from
// HandleClusterDecommission so the unit tests can call it directly with a
// canned deps struct.
func runClusterDecommission(ctx context.Context, deps ClusterDecommissionDeps, id uuid.UUID) error {
	q := deps.Queries

	row, err := q.GetClusterDecommissionByID(ctx, id)
	if err != nil {
		return fmt.Errorf("load cluster_decommission: %w", err)
	}
	if row.Status == PhaseStatusSucceeded {
		// Idempotent re-entry — nothing to do.
		return nil
	}

	// Bump attempts + flip to "running" via the lease-CAS. A "no rows"
	// result means a sibling pod (or the periodic sweep) already holds a
	// live lease on this row — back off so we never double-run the same
	// decommission concurrently (L15).
	row, err = q.MarkClusterDecommissionRunning(ctx, sqlc.MarkClusterDecommissionRunningParams{
		ID:              id,
		LeaseTtlSeconds: decommissionLeaseTTLSeconds,
	})
	if err != nil {
		if claimNotAcquired(err) {
			return nil
		}
		return fmt.Errorf("mark running: %w", err)
	}

	phases := loadPhases(row.Phases)

	// 1. cleanup_managed_side ------------------------------------------------
	if shouldRunPhase(phases, PhaseCleanupManagedSide) {
		startPhase(phases, PhaseCleanupManagedSide)
		_, _ = q.UpdateClusterDecommissionPhases(ctx, sqlc.UpdateClusterDecommissionPhasesParams{ID: id, Phases: phasesJSON(phases)})

		detail, phaseErr := phaseCleanupManagedSide(ctx, deps, row)
		if phaseErr != nil {
			// HA re-queue: the agent's WS is live on a SIBLING pod. Don't
			// fail the phase — reset it to pending, release our lease so the
			// owning pod can re-claim, and return the error so asynq
			// re-enqueues the WHOLE task onto the shared 'tunnel' queue (the
			// owning pod's local Get then succeeds and the ACK is in-process).
			// The 1-minute sweep is the backstop. Mirrors
			// cluster_template_apply.go's agent-not-connected re-queue.
			if isAgentNotConnectedErr(phaseErr) {
				runtimeLogger().WarnContext(ctx, "cluster decommission cleanup deferred: agent on a sibling pod, returning task to queue",
					"decommission_id", id.String(), "cluster_id", row.ClusterID.String(), "error", phaseErr)
				delete(phases, PhaseCleanupManagedSide)
				_, _ = q.UpdateClusterDecommissionPhases(ctx, sqlc.UpdateClusterDecommissionPhasesParams{ID: id, Phases: phasesJSON(phases)})
				_ = q.ReleaseClusterDecommissionClaim(ctx, id)
				return phaseErr
			}
			finishPhase(phases, PhaseCleanupManagedSide, PhaseStatusFailed, phaseErr.Error(), detail)
			recordPhaseAudit(ctx, q, row, PhaseCleanupManagedSide, PhaseStatusFailed, phaseErr.Error(), detail)
			return persistFailure(ctx, q, id, phases, fmt.Sprintf("phase %s: %v", PhaseCleanupManagedSide, phaseErr))
		}
		status := PhaseStatusSucceeded
		// If the agent was unreachable, the phase did its best — we mark the
		// phase skipped (with detail.skipped=true). Unlike before, the
		// reconciler does NOT immediately advance: it waits within the grace
		// window for the agent to reconnect (see the cleanupSatisfied gate
		// below) so token-revoke is deferred until cleanup succeeds or the
		// grace window is exhausted.
		if skipped, ok := detail["skipped"].(bool); ok && skipped {
			status = PhaseStatusSkipped
		}
		finishPhase(phases, PhaseCleanupManagedSide, status, "", detail)
		recordPhaseAudit(ctx, q, row, PhaseCleanupManagedSide, status, "", detail)
		if _, err := q.UpdateClusterDecommissionPhases(ctx, sqlc.UpdateClusterDecommissionPhasesParams{ID: id, Phases: phasesJSON(phases)}); err != nil {
			return persistFailure(ctx, q, id, phases, fmt.Sprintf("persist phase %s: %v", PhaseCleanupManagedSide, err))
		}
	}

	// GATE: defer token-revoke (and every later phase) until managed-side
	// cleanup actually succeeded — OR it's skipped (agent gone) and the grace
	// window is exhausted. Within grace we return nil and leave the row for
	// the 1-minute sweep to retry; we do NOT revoke the token yet, so a
	// reconnecting agent can still authenticate to run cleanup. The grace cap
	// guarantees we eventually advance (no deadlock on a dead agent).
	if !cleanupSatisfied(phases, row) {
		runtimeLogger().InfoContext(ctx, "cluster decommission waiting for managed-side cleanup before token revoke",
			"decommission_id", id.String(), "cluster_id", row.ClusterID.String(),
			"attempts", row.Attempts)
		return nil
	}

	// 2. revoke_agent_token --------------------------------------------------
	if shouldRunPhase(phases, PhaseRevokeAgentToken) {
		startPhase(phases, PhaseRevokeAgentToken)
		detail, phaseErr := phaseRevokeAgentToken(ctx, deps, row)
		if phaseErr != nil {
			finishPhase(phases, PhaseRevokeAgentToken, PhaseStatusFailed, phaseErr.Error(), detail)
			recordPhaseAudit(ctx, q, row, PhaseRevokeAgentToken, PhaseStatusFailed, phaseErr.Error(), detail)
			return persistFailure(ctx, q, id, phases, fmt.Sprintf("phase %s: %v", PhaseRevokeAgentToken, phaseErr))
		}
		finishPhase(phases, PhaseRevokeAgentToken, PhaseStatusSucceeded, "", detail)
		recordPhaseAudit(ctx, q, row, PhaseRevokeAgentToken, PhaseStatusSucceeded, "", detail)
		if _, err := q.UpdateClusterDecommissionPhases(ctx, sqlc.UpdateClusterDecommissionPhasesParams{ID: id, Phases: phasesJSON(phases)}); err != nil {
			return persistFailure(ctx, q, id, phases, fmt.Sprintf("persist phase %s: %v", PhaseRevokeAgentToken, err))
		}
	}

	// 3. archive_audit -------------------------------------------------------
	if shouldRunPhase(phases, PhaseArchiveAudit) {
		startPhase(phases, PhaseArchiveAudit)
		detail, phaseErr := phaseArchiveAudit(ctx, deps, row)
		if phaseErr != nil {
			finishPhase(phases, PhaseArchiveAudit, PhaseStatusFailed, phaseErr.Error(), detail)
			recordPhaseAudit(ctx, q, row, PhaseArchiveAudit, PhaseStatusFailed, phaseErr.Error(), detail)
			return persistFailure(ctx, q, id, phases, fmt.Sprintf("phase %s: %v", PhaseArchiveAudit, phaseErr))
		}
		finishPhase(phases, PhaseArchiveAudit, PhaseStatusSucceeded, "", detail)
		recordPhaseAudit(ctx, q, row, PhaseArchiveAudit, PhaseStatusSucceeded, "", detail)
		if _, err := q.UpdateClusterDecommissionPhases(ctx, sqlc.UpdateClusterDecommissionPhasesParams{ID: id, Phases: phasesJSON(phases)}); err != nil {
			return persistFailure(ctx, q, id, phases, fmt.Sprintf("persist phase %s: %v", PhaseArchiveAudit, err))
		}
	}

	// 4. delete_dependents ---------------------------------------------------
	if shouldRunPhase(phases, PhaseDeleteDependents) {
		startPhase(phases, PhaseDeleteDependents)
		detail, phaseErr := phaseDeleteDependents(ctx, deps, row)
		if phaseErr != nil {
			finishPhase(phases, PhaseDeleteDependents, PhaseStatusFailed, phaseErr.Error(), detail)
			recordPhaseAudit(ctx, q, row, PhaseDeleteDependents, PhaseStatusFailed, phaseErr.Error(), detail)
			return persistFailure(ctx, q, id, phases, fmt.Sprintf("phase %s: %v", PhaseDeleteDependents, phaseErr))
		}
		finishPhase(phases, PhaseDeleteDependents, PhaseStatusSucceeded, "", detail)
		recordPhaseAudit(ctx, q, row, PhaseDeleteDependents, PhaseStatusSucceeded, "", detail)
		if _, err := q.UpdateClusterDecommissionPhases(ctx, sqlc.UpdateClusterDecommissionPhasesParams{ID: id, Phases: phasesJSON(phases)}); err != nil {
			return persistFailure(ctx, q, id, phases, fmt.Sprintf("persist phase %s: %v", PhaseDeleteDependents, err))
		}
	}

	// 5. tombstone_cluster ---------------------------------------------------
	if shouldRunPhase(phases, PhaseTombstoneCluster) {
		startPhase(phases, PhaseTombstoneCluster)
		detail, phaseErr := phaseTombstoneCluster(ctx, deps, row)
		if phaseErr != nil {
			finishPhase(phases, PhaseTombstoneCluster, PhaseStatusFailed, phaseErr.Error(), detail)
			recordPhaseAudit(ctx, q, row, PhaseTombstoneCluster, PhaseStatusFailed, phaseErr.Error(), detail)
			return persistFailure(ctx, q, id, phases, fmt.Sprintf("phase %s: %v", PhaseTombstoneCluster, phaseErr))
		}
		finishPhase(phases, PhaseTombstoneCluster, PhaseStatusSucceeded, "", detail)
		recordPhaseAudit(ctx, q, row, PhaseTombstoneCluster, PhaseStatusSucceeded, "", detail)
	}

	if _, err := q.MarkClusterDecommissionSucceeded(ctx, sqlc.MarkClusterDecommissionSucceededParams{
		ID:     id,
		Phases: phasesJSON(phases),
	}); err != nil {
		return fmt.Errorf("mark succeeded: %w", err)
	}
	return nil
}

// shouldRunPhase returns true when the phase has not yet completed
// successfully. A Skipped phase IS re-attempted on subsequent runs: the only
// phase that ever reaches Skipped is cleanup_managed_side (agent not
// connected), and we want a reconnecting agent to get cleaned up. Termination
// is still guaranteed — the cleanupSatisfied grace gate advances the
// reconciler past a perpetually-skipped cleanup, and the top-of-reconciler
// `status == succeeded` short-circuit stops a finished row from re-running.
func shouldRunPhase(phases phasesMap, name string) bool {
	rec, ok := phases[name]
	if !ok {
		return true
	}
	return rec.Status != PhaseStatusSucceeded
}

func startPhase(phases phasesMap, name string) {
	prev := phases[name]
	prev.Status = PhaseStatusRunning
	prev.StartedAt = time.Now().UTC()
	prev.Error = ""
	phases[name] = prev
}

func finishPhase(phases phasesMap, name, status, errMsg string, detail map[string]any) {
	rec := phases[name]
	rec.Status = status
	rec.CompletedAt = time.Now().UTC()
	rec.Error = errMsg
	rec.Detail = detail
	phases[name] = rec
}

func persistFailure(ctx context.Context, q ClusterDecommissionQuerier, id uuid.UUID, phases phasesMap, msg string) error {
	if _, err := q.MarkClusterDecommissionFailed(ctx, sqlc.MarkClusterDecommissionFailedParams{
		ID:        id,
		LastError: msg,
		Phases:    phasesJSON(phases),
	}); err != nil {
		return fmt.Errorf("mark failed: %w (original: %s)", err, msg)
	}
	// Return nil so asynq doesn't blindly retry — the row reflects the
	// failure and the periodic sweep handles the re-run.
	return nil
}

// recordPhaseAudit writes a `cluster.decommission.<phase>` audit row for the
// per-phase outcome. action == "cluster.decommission.cleanup_managed_side"
// (and similar) — matches the canonical regex `^[a-z]+(\.[a-z0-9_]+)+$` per
// the contract test in internal/audit/action_contract_test.go.
func recordPhaseAudit(ctx context.Context, q ClusterDecommissionQuerier, row sqlc.ClusterDecommission, phase, status, errMsg string, detail map[string]any) {
	if q == nil {
		return
	}
	d := map[string]any{
		"phase":           phase,
		"phase_status":    status,
		"decommission_id": row.ID.String(),
		"cluster_id":      row.ClusterID.String(),
		"cluster_name":    row.ClusterName,
	}
	for k, v := range detail {
		d[k] = v
	}
	if errMsg != "" {
		d["error"] = errMsg
	}
	audit.Record(ctx, q, audit.Event{
		Source:        "worker",
		UserID:        row.RequestedByID,
		Action:        "cluster.decommission." + phase,
		ResourceType:  "cluster",
		ResourceID:    row.ClusterID.String(),
		ResourceName:  row.ClusterName,
		Detail:        d,
		CorrelationID: row.ID.String(),
		IPAddress:     (*netip.Addr)(nil),
	})
}

// ---- phase implementations ----------------------------------------------

// phaseCleanupManagedSide sends a MsgDecommission RPC to the agent and waits
// for the ACK. If no agent is connected we mark the step "skipped" rather
// than failing — the cluster is being decommissioned, so a disconnected
// agent is plausible (the operator may have already kubectl-deleted the
// agent Deployment).
func phaseCleanupManagedSide(ctx context.Context, deps ClusterDecommissionDeps, row sqlc.ClusterDecommission) (map[string]any, error) {
	detail := map[string]any{}
	if deps.Tunnel == nil {
		detail["skipped"] = true
		detail["reason"] = "tunnel hub not configured"
		return detail, nil
	}
	wait := deps.TunnelWait
	if wait <= 0 {
		wait = decommissionTunnelWaitDefault
	}
	ack, connected, err := deps.Tunnel.SendDecommission(ctx, row.ClusterID.String(), protocol.DecommissionPayload{
		ClusterID:             row.ClusterID.String(),
		RemoveLoggingStack:    true,
		RemoveVeleroManaged:   true,
		RemoveAgentDeployment: true,
		RemoveFullFootprint:   true,
		// Label gates — verified against deploy/agent/install.yaml.template.
		VeleroLabel:    "app.kubernetes.io/managed-by=astronomer-go",
		ManagedByLabel: "app.kubernetes.io/managed-by=astronomer-server",
		RBACLabel:      "app.kubernetes.io/part-of=astronomer",
		// ManagedLabel kept for an OLD agent that only understands the legacy
		// field (it gates the legacy Velero delete); harmless to a new agent.
		ManagedLabel: "astronomer.io/managed=true",
	}, wait)
	// Propagate the sibling-pod re-queue signal FIRST: with the locator-aware
	// SendDecommission, a "cluster agent not connected to this pod" error
	// comes back with connected=false, so we must surface it BEFORE the
	// generic !connected skip below (the caller re-queues onto the owning pod).
	if err != nil && isAgentNotConnectedErr(err) {
		return detail, err
	}
	if !connected {
		detail["skipped"] = true
		detail["reason"] = "agent not connected"
		return detail, nil
	}
	if err != nil {
		detail["agent_error"] = err.Error()
		return detail, err
	}
	if ack != nil {
		detail["steps"] = ack.Steps
		// Aggregate any per-step errors into a single string so the operator
		// sees them at a glance; we DON'T fail the phase on per-step errors
		// (the operator can fix those manually) — we only fail when the
		// agent itself returns an envelope-level error above.
		errStrs := []string{}
		orphanBSLs := []string{}
		for _, s := range ack.Steps {
			if !s.Success && s.Error != "" {
				errStrs = append(errStrs, s.Name+": "+s.Error)
			}
			orphanBSLs = append(orphanBSLs, s.Orphans...)
		}
		if len(errStrs) > 0 {
			detail["per_step_errors"] = errStrs
		}
		// Orphan audit (L16): residual Velero BackupStorageLocations whose
		// backing cloud blobs need manual cleanup. Mirrors the
		// argocd_secret_orphan worker-side audit action.
		if len(orphanBSLs) > 0 {
			detail["velero_orphans"] = orphanBSLs
			emitVeleroOrphanAudit(ctx, deps.Queries, row, orphanBSLs)
		}
	}
	return detail, nil
}

// emitVeleroOrphanAudit records a cluster.decommission.velero_orphan worker
// audit row listing residual BackupStorageLocations the agent could not fully
// clean up (deleting a BSL does not remove its backing cloud blobs). Worker-side
// action — no central registration; matches the canonical action regex.
func emitVeleroOrphanAudit(ctx context.Context, q ClusterDecommissionQuerier, row sqlc.ClusterDecommission, orphans []string) {
	if q == nil {
		return
	}
	payload, err := json.Marshal(map[string]any{
		"cluster_id":               row.ClusterID.String(),
		"backup_storage_locations": orphans,
		"reason":                   "velero BSLs left in place; backing cloud blobs require manual cleanup",
	})
	if err != nil {
		return
	}
	_ = q.CreateAuditLogV1(ctx, sqlc.CreateAuditLogV1Params{
		Source:       "worker",
		Action:       "cluster.decommission.velero_orphan",
		ResourceType: "cluster",
		ResourceID:   row.ClusterID.String(),
		ResourceName: row.ClusterName,
		Detail:       payload,
	})
}

func phaseRevokeAgentToken(ctx context.Context, deps ClusterDecommissionDeps, row sqlc.ClusterDecommission) (map[string]any, error) {
	regRows, err := deps.Queries.DeleteClusterRegistrationTokensByCluster(ctx, row.ClusterID)
	if err != nil {
		return nil, fmt.Errorf("delete registration tokens: %w", err)
	}
	agentRows, err := deps.Queries.DeleteClusterAgentTokensByCluster(ctx, row.ClusterID)
	if err != nil {
		return nil, fmt.Errorf("delete agent tokens: %w", err)
	}
	argoRows, err := deps.Queries.DeleteArgoCDClusterProxyTokensByCluster(ctx, row.ClusterID)
	if err != nil {
		return nil, fmt.Errorf("delete argocd cluster proxy tokens: %w", err)
	}
	disconnected := false
	if deps.Tunnel != nil {
		disconnected = deps.Tunnel.Disconnect(row.ClusterID.String())
	}
	detail := map[string]any{
		"registration_tokens_removed": regRows,
		"agent_tokens_removed":        agentRows,
		"argocd_proxy_tokens_removed": argoRows,
		"tunnel_disconnected":         disconnected,
	}
	audit.Record(ctx, deps.Queries, audit.Event{
		Source:          "worker",
		ActorAuthMethod: "system",
		Action:          "agent.token.revoked",
		ResourceType:    "cluster",
		ResourceID:      row.ClusterID.String(),
		ResourceName:    row.ClusterName,
		Detail:          detail,
	})
	return detail, nil
}

func phaseArchiveAudit(ctx context.Context, deps ClusterDecommissionDeps, row sqlc.ClusterDecommission) (map[string]any, error) {
	idText := row.ClusterID.String()
	archived, err := deps.Queries.ArchiveAuditLogsForCluster(ctx, sqlc.ArchiveAuditLogsForClusterParams{
		ClusterID:     row.ClusterID,
		ClusterIDText: idText,
	})
	if err != nil {
		return nil, fmt.Errorf("archive audit_log rows: %w", err)
	}
	deleted, err := deps.Queries.DeleteAuditLogsForCluster(ctx, idText)
	if err != nil {
		// Partial state: rows in audit_archive but still in audit_log. The
		// archive query is ON CONFLICT DO NOTHING so a re-run is a no-op
		// on the archive side and the delete will succeed cleanly.
		return map[string]any{"archived": archived}, fmt.Errorf("delete audit_log rows: %w", err)
	}
	return map[string]any{
		"archived": archived,
		"deleted":  deleted,
	}, nil
}

func phaseDeleteDependents(ctx context.Context, deps ClusterDecommissionDeps, row sqlc.ClusterDecommission) (map[string]any, error) {
	q := deps.Queries
	cid := row.ClusterID
	counts := map[string]any{}
	type op struct {
		name string
		fn   func(context.Context, uuid.UUID) (int64, error)
	}
	ops := []op{
		{"cluster_registry_configs", q.DeleteClusterRegistryConfigsByCluster},
		{"cluster_health_statuses", q.DeleteClusterHealthStatusByCluster},
		{"cluster_conditions", q.DeleteClusterConditionsByCluster},
		{"agent_connections", q.DeleteAgentConnectionsByCluster},
		{"alert_silences", q.DeleteAlertSilencesByCluster}, // before alert_rules (FK)
		{"alert_rules", q.DeleteAlertRulesByCluster},
		{"installed_charts", q.DeleteInstalledChartsByCluster},
		{"cluster_security_policies", q.DeleteClusterSecurityPoliciesByCluster},
		{"project_namespaces", q.DeleteProjectNamespacesByCluster},
		{"cluster_role_bindings", q.DeleteClusterRoleBindingsByCluster},
		// cluster_snapshot_schedules first — the actively-harmful orphan whose
		// rows keep the snapshot dispatcher creating Velero backup jobs for a
		// tombstoned cluster.
		{"cluster_snapshot_schedules", q.DeleteClusterSnapshotSchedulesByCluster},
		{"gitops_registered_clusters", q.DeleteGitOpsRegisteredClustersByCluster},
		{"native_rbac_rules", q.DeleteNativeRBACRulesByCluster},
		{"deferred_operations", q.DeleteDeferredOperationsByCluster},
		{"agent_lifecycle_operations", q.DeleteAgentLifecycleOperationsByCluster},
	}
	var firstErr error
	for _, o := range ops {
		n, err := o.fn(ctx, cid)
		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("delete %s: %w", o.name, err)
		}
		counts[o.name] = n
	}
	// Argo CD managed-cluster mappings: delete the upstream cluster Secret
	// when the management-plane K8s client is wired, then drop the local rows.
	// If the client is unavailable or a delete fails, emit an audit event so
	// operators have a clear orphan cleanup signal.
	managed, mErr := q.ListArgoCDManagedClustersByCluster(ctx, cid)
	if mErr != nil && firstErr == nil {
		firstErr = fmt.Errorf("list argocd_managed_clusters: %w", mErr)
	}
	argoSecretsRemoved := int64(0)
	argoSecretsMissing := int64(0)
	if len(managed) > 0 {
		orphans := make([]map[string]any, 0, len(managed))
		for _, m := range managed {
			orphan := map[string]any{
				"argocd_instance_id":  m.ArgocdInstanceID.String(),
				"cluster_secret_name": m.ClusterSecretName,
				"server_url":          m.ServerUrl,
			}
			if deps.K8s == nil {
				orphan["reason"] = "kubernetes client not configured"
				orphans = append(orphans, orphan)
				continue
			}
			secret, err := lookupClusterSecret(ctx, deps.K8s, m.ClusterSecretName, m.ServerUrl)
			if err != nil {
				orphan["reason"] = "lookup_failed"
				orphan["error"] = err.Error()
				orphans = append(orphans, orphan)
				if firstErr == nil {
					firstErr = fmt.Errorf("lookup argocd cluster secret: %w", err)
				}
				continue
			}
			if secret == nil {
				argoSecretsMissing++
				continue
			}
			orphan["cluster_secret_name"] = secret.Name
			if err := deps.K8s.CoreV1().Secrets(argoCDNamespace).Delete(ctx, secret.Name, kubeutil.DeleteOptions()); err != nil && !apierrors.IsNotFound(err) {
				orphan["reason"] = "delete_failed"
				orphan["error"] = err.Error()
				orphans = append(orphans, orphan)
				if firstErr == nil {
					firstErr = fmt.Errorf("delete argocd cluster secret %s: %w", secret.Name, err)
				}
				continue
			}
			argoSecretsRemoved++
		}
		if len(orphans) > 0 {
			auditDetail := map[string]any{
				"cluster_id": cid.String(),
				"orphans":    orphans,
			}
			if payload, err := json.Marshal(auditDetail); err == nil {
				_ = q.CreateAuditLogV1(ctx, sqlc.CreateAuditLogV1Params{
					Source:       "worker",
					Action:       "cluster.decommission.argocd_secret_orphan",
					ResourceType: "cluster",
					ResourceID:   cid.String(),
					Detail:       payload,
				})
			}
		}
	}
	deleted, mErr := q.DeleteArgoCDManagedClustersByCluster(ctx, cid)
	if mErr != nil && firstErr == nil {
		firstErr = fmt.Errorf("delete argocd_managed_clusters: %w", mErr)
	}
	counts["argocd_managed_clusters"] = deleted
	counts["argocd_cluster_secrets_removed"] = argoSecretsRemoved
	counts["argocd_cluster_secrets_missing"] = argoSecretsMissing
	// Flush the per-user binding cache: every user who had any cluster role
	// on this cluster just had it removed and would otherwise see the stale
	// binding for up to one cache TTL on the hot path.
	if deps.RBACCache != nil {
		deps.RBACCache.InvalidateAll()
	}
	return counts, firstErr
}

func phaseTombstoneCluster(ctx context.Context, deps ClusterDecommissionDeps, row sqlc.ClusterDecommission) (map[string]any, error) {
	// Verify the cluster still exists (i.e. wasn't manually hard-deleted by
	// an operator). If it's gone, the phase has nothing to do — we
	// succeed.
	cluster, err := deps.Queries.GetClusterByID(ctx, row.ClusterID)
	if err != nil {
		// Match "no rows" by message, not errors.Is against a local sentinel:
		// the real error here is pgx.ErrNoRows (a DIFFERENT value from our
		// errNoRows), so errors.Is never matched and an out-of-band row delete
		// failed the decommission terminally instead of succeeding as
		// already-gone. Mirrors claimNotAcquired's string match.
		if strings.Contains(err.Error(), "no rows in result set") {
			return map[string]any{"already_gone": true}, nil
		}
		return nil, fmt.Errorf("load cluster: %w", err)
	}
	if cluster.DecommissionedAt.Valid {
		return map[string]any{
			"already_tombstoned": true,
			"decommissioned_at":  cluster.DecommissionedAt.Time.UTC().Format(time.RFC3339),
		}, nil
	}
	if err := deps.Queries.TombstoneCluster(ctx, row.ClusterID); err != nil {
		return nil, fmt.Errorf("tombstone cluster: %w", err)
	}
	return map[string]any{
		"tombstoned_at": time.Now().UTC().Format(time.RFC3339),
	}, nil
}

// pgxErrNoRows returns the canonical pgx "no rows" sentinel. Declared as a
// helper so the test fake can detect it without taking a pgx import in the
// reconciler's public API. We can't reference pgx.ErrNoRows directly here
// without an extra import; instead we string-match in errors.Is fallback
// since the production driver returns wrapped errors that all match
// "no rows in result set". The helper returns a static sentinel so the
// errors.Is shortcut still works for hand-rolled fakes that return the same
// sentinel.
func pgxErrNoRows() error {
	return errNoRows
}

var errNoRows = errors.New("no rows in result set")

// ---- audit helper conformance --------------------------------------------

// Ensure ClusterDecommissionQuerier satisfies audit.Querier for
// recordPhaseAudit indirection: audit.Querier requires CreateAuditLogV1;
// our interface declares the same. Static check at compile time.
// (Renamed from audit.Writer when the async batched writer was introduced —
// the new audit.Writer is a struct; the original interface is now
// audit.Querier.)
var _ audit.Querier = (ClusterDecommissionQuerier)(nil)
