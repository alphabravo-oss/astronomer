// Fleet orchestrator (migration 056).
//
// Periodic worker that drives every pending/running fleet_operations
// row toward a terminal status. Runs every 10s (registered in
// scheduler.go) and is idempotent — re-running a tick on the same
// operation will not re-fire sub-operations that already completed.
//
// Per tick, for each operation in status IN ('pending','running'):
//
//   1. status='pending'    : evaluate the selector against the clusters
//                            table ONCE, INSERT one fleet_operation_targets
//                            row per match, transition to 'running'.
//                            Subsequent ticks read the persisted list —
//                            we don't re-evaluate the selector.
//
//   2. status='running'    : poll the sub-operation of each running
//                            target; propagate completed/failed.
//                            Dispatch up to (max_concurrent - running)
//                            pending targets. Sequential mode is
//                            modelled as max_concurrent=1 with a hard
//                            cap below — the strategy field is a hint
//                            to the handler validator, not a runtime
//                            knob.
//
//   3. on_error='abort'    : the first failed target transitions the
//                            operation to 'aborted'. In-flight targets
//                            keep running but no new targets are
//                            dispatched.
//
//   4. all targets terminal: transition operation to 'completed'
//                            (no failures) or 'failed' (any failures).
//
// Per-tick wall-clock cap is 30s. If the orchestrator doesn't finish
// every operation in that budget the next tick picks up — we don't
// hold a lease across ticks so a crashed worker doesn't strand an
// operation in a half-state.
//
// Maintenance-window gate: when respect_maintenance_windows=true and
// a MaintenanceWindowChecker says "this cluster is in a window right
// now", the orchestrator SKIPS dispatching that target this tick and
// leaves it 'pending' for the next tick. The real
// maintenance-windows feature ships in a parallel sprint; this code
// integrates via the interface so the two sprints don't share commits.

package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/internal/operationstate"
)

// FleetOrchestrateType is the asynq task type. The scheduler enqueues
// this every 10s; the work itself is leader-gated via
// runPeriodicTaskWithLeader so only one replica drives a given tick.
const FleetOrchestrateType = "fleet:orchestrate"

// fleetOrchestrateTickBudget caps the wall-clock work a single
// orchestrator tick performs. Anything left over rolls into the next
// tick — keeps a slow database from starving the rest of the asynq
// queue.
const fleetOrchestrateTickBudget = 30 * time.Second

// fleetOrchestrateBatchLimit caps how many operations a single tick
// touches. A separate cap from the budget so the slowest path
// (selector eval over thousands of clusters) can't blow the budget on
// a single tick.
const fleetOrchestrateBatchLimit = 25

// fleetOrchestratePendingTargetsPerTick caps how many pending targets
// the orchestrator considers per operation per tick. The real cap on
// in-flight work is max_concurrent — this limit just protects the
// tick from a runaway operation with thousands of pending targets.
const fleetOrchestratePendingTargetsPerTick = 50

// ─────────────────────────────────────────────────────────────────────
// Status constants. Keep in lockstep with the CHECK constraints in
// migration 056 — a typo here would compile but cause every transition
// to fail at runtime with a constraint violation.
// ─────────────────────────────────────────────────────────────────────

const (
	FleetOpStatusPending   = operationstate.Pending
	FleetOpStatusRunning   = operationstate.Running
	FleetOpStatusPaused    = "paused"
	FleetOpStatusCompleted = operationstate.Completed
	FleetOpStatusFailed    = operationstate.Failed
	FleetOpStatusAborted   = "aborted"
)

const (
	FleetTargetStatusPending   = operationstate.Pending
	FleetTargetStatusRunning   = operationstate.Running
	FleetTargetStatusCompleted = operationstate.Completed
	FleetTargetStatusFailed    = operationstate.Failed
	FleetTargetStatusSkipped   = "skipped"
	FleetTargetStatusAborted   = "aborted"
)

const (
	FleetStrategySequential = "sequential"
	FleetStrategyParallel   = "parallel"

	FleetOnErrorAbort    = "abort"
	FleetOnErrorContinue = "continue"
)

// Operation types the orchestrator knows how to dispatch in this
// slice. The handler validates against this list at create time.
const (
	FleetOpTypeToolUpgrade      = "tool_upgrade"
	FleetOpTypeToolInstall      = "tool_install"
	FleetOpTypeToolUninstall    = "tool_uninstall"
	FleetOpTypeApplyTemplate    = "apply_template"
	FleetOpTypeDrainNamespaces  = "drain_namespaces"
	FleetOpTypeRotateAgentToken = "rotate_agent_token"
	FleetOpTypeCustomHelm       = "custom_helm"
)

// ─────────────────────────────────────────────────────────────────────
// Metrics
// ─────────────────────────────────────────────────────────────────────

var (
	fleetOperationsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "astronomer",
			Name:      "fleet_operations_total",
			Help:      "Fleet operations that reached a terminal status, partitioned by operation_type and outcome.",
		},
		observability.MetricLabels("type", "outcome"),
	)
	fleetOperationsInFlight = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "astronomer",
			Name:      "fleet_operations_in_flight",
			Help:      "Fleet operations currently in status 'running'.",
		},
	)
)

func init() {
	prometheus.MustRegister(fleetOperationsTotal, fleetOperationsInFlight)
}

// ─────────────────────────────────────────────────────────────────────
// Dependencies — injected once at startup, swappable for tests.
// ─────────────────────────────────────────────────────────────────────

// FleetOrchestrateQuerier is the slice of *sqlc.Queries the
// orchestrator needs. Local interface so unit tests stand up a
// narrow fake without dragging in the full Queries surface.
type FleetOrchestrateQuerier interface {
	ListPendingFleetOperations(ctx context.Context, queryLimit int32) ([]sqlc.FleetOperation, error)
	GetFleetOperation(ctx context.Context, id uuid.UUID) (sqlc.FleetOperation, error)
	MarkFleetOperationTransition(ctx context.Context, arg sqlc.MarkFleetOperationTransitionParams) (sqlc.FleetOperation, error)
	SetFleetOperationStatus(ctx context.Context, arg sqlc.SetFleetOperationStatusParams) (sqlc.FleetOperation, error)
	UpdateFleetOperationCounters(ctx context.Context, arg sqlc.UpdateFleetOperationCountersParams) (sqlc.FleetOperation, error)

	CreateFleetOperationTarget(ctx context.Context, arg sqlc.CreateFleetOperationTargetParams) (sqlc.FleetOperationTarget, error)
	ListPendingTargetsForOperation(ctx context.Context, arg sqlc.ListPendingTargetsForOperationParams) ([]sqlc.FleetOperationTarget, error)
	ListRunningTargetsForOperation(ctx context.Context, operationID uuid.UUID) ([]sqlc.FleetOperationTarget, error)
	CountRunningTargetsForOperation(ctx context.Context, operationID uuid.UUID) (int64, error)
	CountFleetOperationTargetsByStatus(ctx context.Context, operationID uuid.UUID) ([]sqlc.CountFleetOperationTargetsByStatusRow, error)
	CountTerminalTargetsForOperation(ctx context.Context, operationID uuid.UUID) (int64, error)
	MarkFleetTargetDispatched(ctx context.Context, arg sqlc.MarkFleetTargetDispatchedParams) (sqlc.FleetOperationTarget, error)
	MarkFleetTargetCompleted(ctx context.Context, id uuid.UUID) (sqlc.FleetOperationTarget, error)
	MarkFleetTargetFailed(ctx context.Context, arg sqlc.MarkFleetTargetFailedParams) (sqlc.FleetOperationTarget, error)
	MarkFleetTargetSkipped(ctx context.Context, arg sqlc.MarkFleetTargetSkippedParams) (sqlc.FleetOperationTarget, error)

	ListClustersForSelectorEvaluation(ctx context.Context) ([]sqlc.ListClustersForSelectorEvaluationRow, error)

	// Sub-operation status — for tool_upgrade/tool_install/tool_uninstall
	// we poll tool_operations.
	GetToolOperation(ctx context.Context, id uuid.UUID) (sqlc.ToolOperation, error)
	// For apply_template we poll cluster_template_applications. The row
	// is keyed by cluster_id, not by an id; the orchestrator stamps
	// cluster_id into sub_operation_id at dispatch time.
	GetClusterTemplateApplication(ctx context.Context, clusterID uuid.UUID) (sqlc.ClusterTemplateApplication, error)
	GetClusterTemplateByID(ctx context.Context, id uuid.UUID) (sqlc.ClusterTemplate, error)
	UpsertClusterTemplateApplication(ctx context.Context, arg sqlc.UpsertClusterTemplateApplicationParams) (sqlc.ClusterTemplateApplication, error)
}

// FleetSubOperationDispatcher is the bridge to whatever queue the
// per-cluster work actually runs on. For tool_upgrade/tool_install/
// tool_uninstall this maps to the existing tool-operations enqueue
// path (ToolHandler.EnqueueFleetSubOperation). For apply_template it
// upserts a cluster_template_applications row and enqueues the
// existing cluster_template:apply task.
//
// Returning the sub-operation ID lets the orchestrator poll it on
// subsequent ticks. operationType is the asynq/cluster-template task
// type the orchestrator stamps onto the target row.
type FleetSubOperationDispatcher interface {
	DispatchToolOperation(ctx context.Context, kind string, clusterID uuid.UUID, spec FleetToolOperationSpec) (uuid.UUID, string, error)
	DispatchApplyTemplate(ctx context.Context, clusterID, templateID uuid.UUID) (uuid.UUID, string, error)
}

// FleetToolOperationSpec is the type-specific argument bundle for
// tool_upgrade / tool_install / tool_uninstall. Decoded by
// ParseFleetToolOperationSpec from operation_spec JSONB.
type FleetToolOperationSpec struct {
	Slug          string          `json:"slug"`
	TargetVersion string          `json:"target_version,omitempty"`
	ReleaseName   string          `json:"release_name,omitempty"`
	Preset        string          `json:"preset,omitempty"`
	Values        json.RawMessage `json:"values,omitempty"`
}

// FleetApplyTemplateSpec is the operation_spec shape for
// apply_template.
type FleetApplyTemplateSpec struct {
	TemplateID string `json:"template_id"`
}

// FleetMaintenanceWindowChecker is the gate consulted when
// respect_maintenance_windows=true. The real implementation ships in
// a parallel sprint; this interface lets that sprint plug in without
// touching the orchestrator commits.
//
// Implementations MUST be fast (single in-memory map lookup is the
// target) — the orchestrator calls this on the dispatch hot path.
type FleetMaintenanceWindowChecker interface {
	// IsInMaintenanceWindow returns true when the cluster has an
	// active maintenance window that says "do not perform fleet
	// operations right now". The orchestrator will skip the target
	// for this tick and try again next tick.
	IsInMaintenanceWindow(ctx context.Context, clusterID uuid.UUID) bool
}

// NoopMaintenanceWindowChecker always returns false — used when the
// real implementation hasn't shipped yet so respect_maintenance_windows
// is effectively a no-op until that sprint lands.
type NoopMaintenanceWindowChecker struct{}

// IsInMaintenanceWindow always returns false.
func (NoopMaintenanceWindowChecker) IsInMaintenanceWindow(_ context.Context, _ uuid.UUID) bool {
	return false
}

// FleetOrchestrateDeps wires the orchestrator. Set once at startup via
// ConfigureFleetOrchestrate; tests swap fakes.
type FleetOrchestrateDeps struct {
	Queries           FleetOrchestrateQuerier
	Dispatcher        FleetSubOperationDispatcher
	MaintenanceWindow FleetMaintenanceWindowChecker
}

var fleetOrchestrateDeps FleetOrchestrateDeps

// ConfigureFleetOrchestrate wires runtime dependencies. Called once
// from cmd/server. MaintenanceWindow may be nil — defaults to
// NoopMaintenanceWindowChecker.
func ConfigureFleetOrchestrate(deps FleetOrchestrateDeps) {
	if deps.MaintenanceWindow == nil {
		deps.MaintenanceWindow = NoopMaintenanceWindowChecker{}
	}
	fleetOrchestrateDeps = deps
}

// ResetFleetOrchestrate clears the runtime deps. Used by tests.
func ResetFleetOrchestrate() {
	fleetOrchestrateDeps = FleetOrchestrateDeps{}
}

// ─────────────────────────────────────────────────────────────────────
// Asynq entry point
// ─────────────────────────────────────────────────────────────────────

// HandleFleetOrchestrate is the asynq handler. Leader-gated through
// runPeriodicTaskWithLeader — when multiple worker pods race, only
// the lease holder drives the tick.
func HandleFleetOrchestrate(ctx context.Context, _ *asynq.Task) error {
	if fleetOrchestrateDeps.Queries == nil {
		runtimeLogger().InfoContext(ctx, "fleet orchestrate runtime not configured, skipping")
		return nil
	}
	return runPeriodicTaskWithLeader(ctx, FleetOrchestrateType, func() error {
		tickCtx, cancel := context.WithTimeout(ctx, fleetOrchestrateTickBudget)
		defer cancel()
		return runFleetOrchestrateTick(tickCtx, fleetOrchestrateDeps)
	})
}

// runFleetOrchestrateTick is the testable core. Lists every pending /
// running fleet operation, then walks each one through its
// reconciliation step. Errors from a single operation are logged and
// don't block the rest of the batch — we never want one stuck
// operation to starve the whole fleet.
func runFleetOrchestrateTick(ctx context.Context, deps FleetOrchestrateDeps) error {
	ops, err := deps.Queries.ListPendingFleetOperations(ctx, fleetOrchestrateBatchLimit)
	if err != nil {
		return fmt.Errorf("list pending fleet operations: %w", err)
	}
	for _, op := range ops {
		if ctx.Err() != nil {
			return nil
		}
		if err := reconcileFleetOperation(ctx, deps, op); err != nil {
			runtimeLogger().ErrorContext(ctx, "reconcile fleet operation", "error", err, "operation_id", op.ID)
		}
	}
	// Refresh the in-flight gauge once at the end of the tick — cheaper
	// than touching it on every transition.
	inFlight := 0
	for _, op := range ops {
		if op.Status == FleetOpStatusRunning {
			inFlight++
		}
	}
	fleetOperationsInFlight.Set(float64(inFlight))
	return nil
}

// reconcileFleetOperation is the per-operation step. Single source of
// truth for the state machine — every call site that wants to move an
// operation forward goes through here. When a pending op transitions
// to running inside this call, we also drive the running tick on the
// same call so a freshly-created 50-cluster fanout starts dispatching
// immediately rather than waiting another scheduler period.
func reconcileFleetOperation(ctx context.Context, deps FleetOrchestrateDeps, op sqlc.FleetOperation) error {
	if op.Status == FleetOpStatusPending {
		if err := launchFleetOperation(ctx, deps, op); err != nil {
			return err
		}
		// Reload — the launch may have transitioned to running (or to
		// completed if the selector matched nothing). Fall through to
		// the running-tick step so the first batch of dispatches
		// happens this tick.
		reloaded, err := deps.Queries.GetFleetOperation(ctx, op.ID)
		if err != nil {
			return err
		}
		op = reloaded
	}
	if op.Status == FleetOpStatusRunning {
		return tickRunningFleetOperation(ctx, deps, op)
	}
	// Paused / completed / failed / aborted — nothing to do.
	return nil
}

// ─────────────────────────────────────────────────────────────────────
// Launch (pending → running)
// ─────────────────────────────────────────────────────────────────────

// launchFleetOperation evaluates the selector against the clusters
// table and INSERTs one target row per matched cluster, then
// transitions the operation to 'running'. The selector is evaluated
// exactly once per operation lifetime — subsequent ticks read the
// persisted target list.
func launchFleetOperation(ctx context.Context, deps FleetOrchestrateDeps, op sqlc.FleetOperation) error {
	sel, err := ParseFleetSelector(op.Selector)
	if err != nil {
		return failOperation(ctx, deps, op, fmt.Sprintf("parse selector: %v", err))
	}
	if sel.IsEmpty() {
		// The handler enforces non-empty at create time, but defend in
		// depth: an operation we can't fanout is a no-op completion
		// rather than a perpetually-pending row.
		return completeOperationNoTargets(ctx, deps, op, "selector matches no clusters")
	}

	rows, err := deps.Queries.ListClustersForSelectorEvaluation(ctx)
	if err != nil {
		return fmt.Errorf("list clusters for selector: %w", err)
	}
	candidates := make([]FleetClusterCandidate, 0, len(rows))
	for _, r := range rows {
		candidates = append(candidates, FleetClusterCandidate{
			ID:     r.ID,
			Name:   r.Name,
			Labels: DecodeClusterLabels(r.Labels),
		})
	}
	matched := EvaluateFleetSelector(sel, candidates)

	subOpType := subOperationTypeFor(op.OperationType)
	for _, c := range matched {
		_, err := deps.Queries.CreateFleetOperationTarget(ctx, sqlc.CreateFleetOperationTargetParams{
			OperationID:      op.ID,
			ClusterID:        c.ID,
			SubOperationType: subOpType,
		})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			// pgx.ErrNoRows means the ON CONFLICT DO NOTHING fired —
			// a target row already existed from a previous half-run.
			// Anything else is fatal for this op (we don't want to
			// transition to 'running' with a partial target list).
			return fmt.Errorf("create target for cluster %s: %w", c.ID, err)
		}
	}

	if len(matched) == 0 {
		return completeOperationNoTargets(ctx, deps, op, "selector matches no clusters")
	}

	// Atomic pending → running transition. The from_status guard
	// makes the call idempotent against a duplicate tick.
	now := pgtype.Timestamptz{Time: time.Now(), Valid: true}
	if _, err := deps.Queries.MarkFleetOperationTransition(ctx, sqlc.MarkFleetOperationTransitionParams{
		ID:         op.ID,
		ToStatus:   FleetOpStatusRunning,
		StartedAt:  now,
		FromStatus: FleetOpStatusPending,
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Someone else (paused via handler, or a concurrent
			// tick) already transitioned. Not an error.
			return nil
		}
		return fmt.Errorf("transition pending->running: %w", err)
	}

	if err := refreshFleetOperationCounters(ctx, deps, op.ID); err != nil {
		runtimeLogger().WarnContext(ctx, "refresh counters after launch", "error", err, "operation_id", op.ID)
	}
	return nil
}

// completeOperationNoTargets fast-paths an operation whose selector
// matched nothing to 'completed' so the UI stops showing it as pending
// forever. The aggregate counters all stay at 0.
func completeOperationNoTargets(ctx context.Context, deps FleetOrchestrateDeps, op sqlc.FleetOperation, reason string) error {
	now := pgtype.Timestamptz{Time: time.Now(), Valid: true}
	if _, err := deps.Queries.MarkFleetOperationTransition(ctx, sqlc.MarkFleetOperationTransitionParams{
		ID:          op.ID,
		ToStatus:    FleetOpStatusCompleted,
		StartedAt:   now,
		CompletedAt: now,
		LastError:   reason,
		FromStatus:  FleetOpStatusPending,
	}); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("transition pending->completed (no targets): %w", err)
	}
	fleetOperationsTotal.WithLabelValues(observability.MetricValues(op.OperationType, "no_targets")...).Inc()
	return nil
}

// ─────────────────────────────────────────────────────────────────────
// Running tick (dispatch + poll)
// ─────────────────────────────────────────────────────────────────────

// tickRunningFleetOperation does three things per call:
//  1. Poll every running target's sub-operation; propagate terminal.
//  2. Decide whether to abort based on on_error policy.
//  3. Dispatch up to (max_concurrent - running) pending targets.
//
// After each transition we refresh the aggregate counters on the
// operation row so the read endpoints don't have to GROUP BY.
func tickRunningFleetOperation(ctx context.Context, deps FleetOrchestrateDeps, op sqlc.FleetOperation) error {
	// 1. Poll running targets.
	running, err := deps.Queries.ListRunningTargetsForOperation(ctx, op.ID)
	if err != nil {
		return fmt.Errorf("list running targets: %w", err)
	}
	for _, t := range running {
		if err := pollFleetTarget(ctx, deps, op, t); err != nil {
			runtimeLogger().WarnContext(ctx, "poll target", "error", err, "operation_id", op.ID, "target_id", t.ID)
		}
	}

	// Refresh op + counters because step 1 may have transitioned
	// targets. Refresh first (writes to DB), then reload so the in-
	// memory op carries the freshly-written aggregate counts the
	// abort and finalize checks rely on.
	if err := refreshFleetOperationCounters(ctx, deps, op.ID); err != nil {
		runtimeLogger().WarnContext(ctx, "refresh counters mid-tick", "error", err, "operation_id", op.ID)
	}
	op, err = deps.Queries.GetFleetOperation(ctx, op.ID)
	if err != nil {
		return fmt.Errorf("reload operation: %w", err)
	}

	// 2. Abort-on-error? Check before dispatching more work.
	if op.OnError == FleetOnErrorAbort && op.FailedClusters > 0 {
		if _, err := deps.Queries.SetFleetOperationStatus(ctx, sqlc.SetFleetOperationStatusParams{
			ID:        op.ID,
			Status:    FleetOpStatusAborted,
			LastError: fmt.Sprintf("aborted after %d cluster failure(s)", op.FailedClusters),
		}); err != nil {
			return fmt.Errorf("set aborted: %w", err)
		}
		fleetOperationsTotal.WithLabelValues(observability.MetricValues(op.OperationType, "aborted")...).Inc()
		return nil
	}

	// 3. Have all targets reached a terminal state?
	terminal, err := deps.Queries.CountTerminalTargetsForOperation(ctx, op.ID)
	if err != nil {
		return fmt.Errorf("count terminal targets: %w", err)
	}
	if int32(terminal) >= op.TotalClusters && op.TotalClusters > 0 {
		return finalizeFleetOperation(ctx, deps, op)
	}

	// 4. Dispatch more if we have headroom.
	return dispatchPendingFleetTargets(ctx, deps, op)
}

// pollFleetTarget reads the sub-operation status and, if terminal,
// transitions the target row to completed/failed. Idempotent — when
// the sub-op is still running we no-op and try again next tick.
func pollFleetTarget(ctx context.Context, deps FleetOrchestrateDeps, op sqlc.FleetOperation, target sqlc.FleetOperationTarget) error {
	if !target.SubOperationID.Valid {
		// We dispatched but didn't record an ID — defensive. Treat as
		// failed so we don't stall forever.
		_, err := deps.Queries.MarkFleetTargetFailed(ctx, sqlc.MarkFleetTargetFailedParams{
			ID:        target.ID,
			LastError: "sub-operation id missing",
		})
		return err
	}
	subID := uuid.UUID(target.SubOperationID.Bytes)

	switch target.SubOperationType {
	case FleetOpTypeToolUpgrade, FleetOpTypeToolInstall, FleetOpTypeToolUninstall:
		subOp, err := deps.Queries.GetToolOperation(ctx, subID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				// Sub-op vanished — treat as failed.
				_, mErr := deps.Queries.MarkFleetTargetFailed(ctx, sqlc.MarkFleetTargetFailedParams{
					ID:        target.ID,
					LastError: "sub-operation no longer exists",
				})
				return mErr
			}
			return err
		}
		switch subOp.Status {
		case operationstate.Completed:
			_, err := deps.Queries.MarkFleetTargetCompleted(ctx, target.ID)
			return err
		case operationstate.Failed, operationstate.Superseded:
			_, err := deps.Queries.MarkFleetTargetFailed(ctx, sqlc.MarkFleetTargetFailedParams{
				ID:        target.ID,
				LastError: nonEmpty(subOp.ErrorMessage, subOp.Status),
			})
			return err
		default:
			// pending / running — keep waiting.
			return nil
		}

	case FleetOpTypeApplyTemplate:
		// For apply_template the sub_operation_id is the cluster_id
		// (the cluster_template_applications row is keyed by cluster).
		app, err := deps.Queries.GetClusterTemplateApplication(ctx, subID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				_, mErr := deps.Queries.MarkFleetTargetFailed(ctx, sqlc.MarkFleetTargetFailedParams{
					ID:        target.ID,
					LastError: "template application row missing",
				})
				return mErr
			}
			return err
		}
		switch app.Status {
		case "applied":
			_, err := deps.Queries.MarkFleetTargetCompleted(ctx, target.ID)
			return err
		case "failed":
			_, err := deps.Queries.MarkFleetTargetFailed(ctx, sqlc.MarkFleetTargetFailedParams{
				ID:        target.ID,
				LastError: nonEmpty(app.LastError, "template apply failed"),
			})
			return err
		default:
			return nil
		}

	default:
		// Reserved operation types (drain_namespaces, rotate_agent_token,
		// custom_helm) — orchestrator can't drive them yet, so a target
		// whose dispatch produced no sub-operation just stays at
		// 'running' forever otherwise. Skip with a clear last_error.
		_, _ = deps.Queries.MarkFleetTargetSkipped(ctx, sqlc.MarkFleetTargetSkippedParams{
			ID:        target.ID,
			LastError: "operation type " + op.OperationType + " not yet implemented",
		})
		return nil
	}
}

// dispatchPendingFleetTargets pops pending targets and fires their
// sub-operations until either:
//   - max_concurrent is reached
//   - we run out of pending targets
//   - the maintenance-window gate forces a skip
func dispatchPendingFleetTargets(ctx context.Context, deps FleetOrchestrateDeps, op sqlc.FleetOperation) error {
	running, err := deps.Queries.CountRunningTargetsForOperation(ctx, op.ID)
	if err != nil {
		return fmt.Errorf("count running: %w", err)
	}
	headroom := int(op.MaxConcurrent) - int(running)
	if op.Strategy == FleetStrategySequential {
		// Sequential mode is a hard cap of 1 regardless of how
		// max_concurrent is configured. The handler enforces
		// max_concurrent=1 on sequential at create time; this
		// belt-and-suspenders cap protects against an operator
		// hand-editing the DB.
		headroom = 1 - int(running)
	}
	if headroom <= 0 {
		return nil
	}

	pending, err := deps.Queries.ListPendingTargetsForOperation(ctx, sqlc.ListPendingTargetsForOperationParams{
		OperationID: op.ID,
		Limit:       fleetOrchestratePendingTargetsPerTick,
	})
	if err != nil {
		return fmt.Errorf("list pending targets: %w", err)
	}

	dispatched := 0
	for _, t := range pending {
		if dispatched >= headroom {
			break
		}
		if ctx.Err() != nil {
			break
		}
		// Maintenance window check — when the cluster is currently in
		// a maintenance window we leave the target 'pending' for the
		// next tick. We don't mark it skipped because the operator's
		// intent is "wait for the window to close, then dispatch".
		if op.RespectMaintenanceWindows && deps.MaintenanceWindow != nil &&
			deps.MaintenanceWindow.IsInMaintenanceWindow(ctx, t.ClusterID) {
			continue
		}

		subID, subType, dErr := dispatchOneFleetTarget(ctx, deps, op, t)
		if dErr != nil {
			runtimeLogger().WarnContext(ctx, "dispatch target failed", "error", dErr, "operation_id", op.ID, "cluster_id", t.ClusterID)
			_, _ = deps.Queries.MarkFleetTargetFailed(ctx, sqlc.MarkFleetTargetFailedParams{
				ID:        t.ID,
				LastError: dErr.Error(),
			})
			continue
		}
		_, err := deps.Queries.MarkFleetTargetDispatched(ctx, sqlc.MarkFleetTargetDispatchedParams{
			ID:               t.ID,
			SubOperationID:   pgtype.UUID{Bytes: subID, Valid: true},
			SubOperationType: subType,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				// Already dispatched by a concurrent tick. Not an error.
				continue
			}
			return fmt.Errorf("mark target dispatched: %w", err)
		}
		dispatched++
	}
	return nil
}

// dispatchOneFleetTarget calls into the type-specific dispatcher and
// returns the sub-operation ID + sub-operation type stamp.
func dispatchOneFleetTarget(ctx context.Context, deps FleetOrchestrateDeps, op sqlc.FleetOperation, target sqlc.FleetOperationTarget) (uuid.UUID, string, error) {
	if deps.Dispatcher == nil {
		return uuid.Nil, "", fmt.Errorf("no dispatcher wired")
	}
	switch op.OperationType {
	case FleetOpTypeToolUpgrade, FleetOpTypeToolInstall, FleetOpTypeToolUninstall:
		spec, err := parseFleetToolSpec(op.OperationSpec)
		if err != nil {
			return uuid.Nil, "", err
		}
		return deps.Dispatcher.DispatchToolOperation(ctx, op.OperationType, target.ClusterID, spec)

	case FleetOpTypeApplyTemplate:
		spec, err := parseFleetApplyTemplateSpec(op.OperationSpec)
		if err != nil {
			return uuid.Nil, "", err
		}
		tmplID, err := uuid.Parse(spec.TemplateID)
		if err != nil {
			return uuid.Nil, "", fmt.Errorf("invalid template_id %q: %w", spec.TemplateID, err)
		}
		return deps.Dispatcher.DispatchApplyTemplate(ctx, target.ClusterID, tmplID)

	case FleetOpTypeDrainNamespaces, FleetOpTypeRotateAgentToken, FleetOpTypeCustomHelm:
		// TODO: implement in follow-up sprint. We deliberately return
		// an error so the orchestrator marks the target failed with a
		// clear message rather than stranding it pending forever.
		return uuid.Nil, "", fmt.Errorf("operation type %q is not yet implemented", op.OperationType)

	default:
		return uuid.Nil, "", fmt.Errorf("unknown operation type %q", op.OperationType)
	}
}

// finalizeFleetOperation transitions an operation whose targets are
// all terminal to its final status (completed when zero failures,
// failed otherwise). Idempotent — the MarkFleetOperationTransition
// guard makes a second call to this from a concurrent tick a no-op.
func finalizeFleetOperation(ctx context.Context, deps FleetOrchestrateDeps, op sqlc.FleetOperation) error {
	now := pgtype.Timestamptz{Time: time.Now(), Valid: true}
	to := FleetOpStatusCompleted
	lastErr := ""
	if op.FailedClusters > 0 {
		to = FleetOpStatusFailed
		lastErr = fmt.Sprintf("%d cluster(s) failed", op.FailedClusters)
	}
	if _, err := deps.Queries.MarkFleetOperationTransition(ctx, sqlc.MarkFleetOperationTransitionParams{
		ID:          op.ID,
		ToStatus:    to,
		CompletedAt: now,
		LastError:   lastErr,
		FromStatus:  FleetOpStatusRunning,
	}); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("finalize %s: %w", to, err)
	}
	fleetOperationsTotal.WithLabelValues(observability.MetricValues(op.OperationType, to)...).Inc()
	return nil
}

// failOperation moves a launch-time-failed operation to 'failed' with
// the supplied message in last_error. Used for selector parse errors,
// not for per-target failures (those go through the abort branch).
func failOperation(ctx context.Context, deps FleetOrchestrateDeps, op sqlc.FleetOperation, msg string) error {
	now := pgtype.Timestamptz{Time: time.Now(), Valid: true}
	if _, err := deps.Queries.MarkFleetOperationTransition(ctx, sqlc.MarkFleetOperationTransitionParams{
		ID:          op.ID,
		ToStatus:    FleetOpStatusFailed,
		CompletedAt: now,
		LastError:   msg,
		FromStatus:  FleetOpStatusPending,
	}); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("fail operation: %w", err)
	}
	fleetOperationsTotal.WithLabelValues(observability.MetricValues(op.OperationType, FleetOpStatusFailed)...).Inc()
	return nil
}

// refreshFleetOperationCounters re-reads the per-status counts from
// the targets table and writes them back to the operation row so the
// read endpoints don't have to aggregate on every render.
func refreshFleetOperationCounters(ctx context.Context, deps FleetOrchestrateDeps, opID uuid.UUID) error {
	rows, err := deps.Queries.CountFleetOperationTargetsByStatus(ctx, opID)
	if err != nil {
		return err
	}
	var total, completed, failed, skipped int32
	for _, r := range rows {
		total += int32(r.N)
		switch r.Status {
		case FleetTargetStatusCompleted:
			completed = int32(r.N)
		case FleetTargetStatusFailed:
			failed = int32(r.N)
		case FleetTargetStatusSkipped:
			skipped = int32(r.N)
		}
	}
	_, err = deps.Queries.UpdateFleetOperationCounters(ctx, sqlc.UpdateFleetOperationCountersParams{
		ID:                opID,
		TotalClusters:     total,
		CompletedClusters: completed,
		FailedClusters:    failed,
		SkippedClusters:   skipped,
	})
	return err
}

// ─────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────

// subOperationTypeFor returns the sub_operation_type stamp the
// orchestrator records on each target row. Mostly mirrors the
// operation_type for tool ops; apply_template keeps its own label so
// the poll switch knows which queue to interrogate.
func subOperationTypeFor(opType string) string {
	switch opType {
	case FleetOpTypeToolUpgrade, FleetOpTypeToolInstall, FleetOpTypeToolUninstall:
		return opType
	case FleetOpTypeApplyTemplate:
		return FleetOpTypeApplyTemplate
	default:
		return opType
	}
}

func parseFleetToolSpec(raw json.RawMessage) (FleetToolOperationSpec, error) {
	var s FleetToolOperationSpec
	if len(raw) == 0 {
		return s, fmt.Errorf("tool operation spec is empty")
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return s, fmt.Errorf("parse tool spec: %w", err)
	}
	if s.Slug == "" {
		return s, fmt.Errorf("tool operation spec missing slug")
	}
	return s, nil
}

func parseFleetApplyTemplateSpec(raw json.RawMessage) (FleetApplyTemplateSpec, error) {
	var s FleetApplyTemplateSpec
	if len(raw) == 0 {
		return s, fmt.Errorf("apply_template spec is empty")
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return s, fmt.Errorf("parse apply_template spec: %w", err)
	}
	if s.TemplateID == "" {
		return s, fmt.Errorf("apply_template spec missing template_id")
	}
	return s, nil
}

// nonEmpty returns the first non-empty string. Used so a target's
// last_error always has SOMETHING in it even if the sub-op didn't
// stamp a message — the UI's "why did this fail?" tooltip should
// never be blank.
func nonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// NewFleetOrchestrateTask returns the asynq task envelope the
// scheduler enqueues every 10s.
func NewFleetOrchestrateTask() (*asynq.Task, error) {
	return asynq.NewTask(FleetOrchestrateType, nil), nil
}
