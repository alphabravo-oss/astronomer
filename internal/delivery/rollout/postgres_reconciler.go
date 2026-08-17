package rollout

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/internal/maintenance"
)

const (
	defaultReconcileLease = 30 * time.Second
	defaultSweepLimit     = 16
)

// Reconciler claims rollout rows with a monotonically increasing database
// fence, evaluates the pure scheduler, then commits desired deployments,
// rollout state, counters, events, and the next durable wake-up atomically.
type Reconciler struct {
	pool    *pgxpool.Pool
	windows maintenance.WindowEvaluator
	owner   string
	now     func() time.Time
	lease   time.Duration
}

func NewPostgresReconciler(pool *pgxpool.Pool, windows maintenance.WindowEvaluator, owner string) (*Reconciler, error) {
	if pool == nil {
		return nil, fail(CodeInvalidInput, "pool", "is required")
	}
	if owner == "" {
		owner = "delivery-rollout-" + uuid.NewString()
	}
	if len(owner) > MaxLeaseOwnerLength {
		return nil, fail(CodeInvalidInput, "owner", "exceeds lease owner limit")
	}
	return &Reconciler{pool: pool, windows: windows, owner: owner, now: time.Now, lease: defaultReconcileLease}, nil
}

// ReconcileOne is an idempotent wake-up. Another replica holding a live lease
// makes this a successful no-op; the periodic sweep repairs lost wake-ups.
func (r *Reconciler) ReconcileOne(ctx context.Context, rolloutID uuid.UUID) error {
	if r == nil || r.pool == nil {
		return errors.New("delivery rollout reconciler is not configured")
	}
	if rolloutID == uuid.Nil {
		return fail(CodeInvalidInput, "rollout_id", "must be a non-zero UUID")
	}
	row, err := sqlc.New(r.pool).ClaimDeliveryRollout(ctx, sqlc.ClaimDeliveryRolloutParams{
		LeaseOwner: r.owner, LeaseDuration: interval(r.lease), ID: rolloutID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("claim delivery rollout %s: %w", rolloutID, err)
	}
	return r.reconcileClaimed(ctx, row)
}

// Sweep claims a bounded SKIP LOCKED batch. It is safe for every worker
// replica to run concurrently and is the recovery path for crashed workers,
// lost Redis messages, expired leases, and time-based soak/deadline gates.
func (r *Reconciler) Sweep(ctx context.Context, limit int) error {
	if r == nil || r.pool == nil {
		return errors.New("delivery rollout reconciler is not configured")
	}
	if limit <= 0 || limit > 256 {
		limit = defaultSweepLimit
	}
	rows, err := sqlc.New(r.pool).ClaimDeliveryRollouts(ctx, sqlc.ClaimDeliveryRolloutsParams{
		LeaseOwner: r.owner, LeaseDuration: interval(r.lease), ClaimLimit: int32(limit),
	})
	if err != nil {
		return fmt.Errorf("claim delivery rollout sweep: %w", err)
	}
	var reconcileErrors []error
	for _, row := range rows {
		if err := r.reconcileClaimed(ctx, row); err != nil {
			reconcileErrors = append(reconcileErrors, fmt.Errorf("reconcile claimed delivery rollout %s: %w", row.ID, err))
		}
	}
	return errors.Join(reconcileErrors...)
}

func (r *Reconciler) reconcileClaimed(ctx context.Context, claimed sqlc.DeliveryRollout) error {
	plan, err := decodeFrozenPlan(claimed)
	if err != nil {
		return err
	}
	queries := sqlc.New(r.pool)
	rows, err := queries.ListDeliveryRolloutRuntime(ctx, claimed.ID)
	if err != nil {
		return fmt.Errorf("load rollout runtime: %w", err)
	}
	runtime, rowByCluster, err := runtimeFromRows(plan, claimed, rows)
	if err != nil {
		return err
	}
	approvalRows, err := queries.ListDeliveryRolloutApprovals(ctx, claimed.ID)
	if err != nil {
		return fmt.Errorf("load rollout approvals: %w", err)
	}
	initialApproval, cohortApprovals := approvalsFromRows(approvalRows)
	now := r.now().UTC()
	maintenanceGate, err := r.maintenanceGate(ctx, plan, rows, now)
	if err != nil {
		return err
	}
	decision, err := Evaluate(EvaluateInput{
		Now: now, Snapshot: runtime,
		Lease:           Lease{Owner: r.owner, Fence: claimed.FencingGeneration, ExpiresAt: claimed.LeaseExpiresAt.Time},
		InitialApproval: initialApproval, CohortApprovals: cohortApprovals, Maintenance: maintenanceGate,
	})
	if err != nil {
		return fmt.Errorf("evaluate rollout decision: %w", err)
	}
	return r.applyDecision(ctx, claimed, plan, rowByCluster, decision, now)
}

func runtimeFromRows(plan FrozenRollout, claimed sqlc.DeliveryRollout, rows []sqlc.ListDeliveryRolloutRuntimeRow) (RuntimeSnapshot, map[uuid.UUID]sqlc.ListDeliveryRolloutRuntimeRow, error) {
	clusters := make([]ClusterRuntime, 0, len(rows))
	rowByCluster := make(map[uuid.UUID]sqlc.ListDeliveryRolloutRuntimeRow, len(rows))
	for _, row := range rows {
		stateValue := model.RolloutClusterState(row.State)
		if !stateValue.Valid() {
			return RuntimeSnapshot{}, nil, fail(CodeInvariant, "rollout_cluster.state", "stored state is invalid")
		}
		available := !row.DeploymentPhase.Valid || row.DeploymentPhase.String == string(model.DeploymentReady)
		var readySince *time.Time
		if row.ReadyAt.Valid {
			value := row.ReadyAt.Time.UTC()
			readySince = &value
		}
		generation := int64(0)
		if row.DesiredGeneration.Valid {
			generation = row.DesiredGeneration.Int64
		}
		lastAction := AssignmentAction(row.AssignmentAction)
		if lastAction != AssignmentApply && lastAction != AssignmentRollback {
			return RuntimeSnapshot{}, nil, fail(CodeInvariant, "rollout_cluster.assignment_action", "stored action is invalid")
		}
		cluster := ClusterRuntime{
			ClusterID: row.ClusterID, State: stateValue, Fence: row.Fence, Connected: row.Connected,
			Available: available, ReadySince: readySince, StateChangedAt: row.UpdatedAt.UTC(),
			CurrentGeneration: generation, EverReleased: row.ReleasedAt.Valid, LastAction: lastAction,
		}
		if row.Deadline.Valid {
			cluster.Deadline = row.Deadline.Time.UTC()
		}
		clusters = append(clusters, cluster)
		rowByCluster[row.ClusterID] = row
	}
	rolloutState := model.RolloutState(claimed.State)
	if !rolloutState.Valid() {
		return RuntimeSnapshot{}, nil, fail(CodeInvariant, "rollout.state", "stored state is invalid")
	}
	return RuntimeSnapshot{Plan: plan, State: rolloutState, Fence: claimed.FencingGeneration, Clusters: clusters}, rowByCluster, nil
}

func approvalsFromRows(rows []sqlc.DeliveryRolloutApproval) (*ApprovalDecision, map[int]ApprovalDecision) {
	cohorts := make(map[int]ApprovalDecision, len(rows))
	var initial *ApprovalDecision
	for _, row := range rows {
		decision := ApprovalDecision{
			ID: row.ID, BindingDigest: model.Digest(row.BindingDigest), Approved: row.Decision == "approved",
			DecidedAt: row.DecidedAt.UTC(), ExpiresAt: row.ExpiresAt.UTC(),
		}
		if row.Cohort == -1 {
			copy := decision
			initial = &copy
		} else {
			cohorts[int(row.Cohort)] = decision
		}
	}
	return initial, cohorts
}

func (r *Reconciler) maintenanceGate(ctx context.Context, plan FrozenRollout, rows []sqlc.ListDeliveryRolloutRuntimeRow, now time.Time) (MaintenanceGate, error) {
	if !plan.Strategy.RespectMaintenanceWindows {
		return MaintenanceGate{Open: true}, nil
	}
	for _, row := range rows {
		stateValue := model.RolloutClusterState(row.State)
		if stateValue != model.RolloutClusterPending && stateValue != model.RolloutClusterBlocked {
			continue
		}
		labels := make(map[string]string)
		if err := json.Unmarshal(row.Labels, &labels); err != nil {
			return MaintenanceGate{}, fail(CodeInvariant, "cluster.labels", "stored labels are invalid")
		}
		blocked, _, err := maintenance.IsBlocked(ctx, r.windows, maintenance.OpDeliveryRollout, labels, now)
		if err != nil {
			return MaintenanceGate{}, fmt.Errorf("evaluate delivery maintenance window: %w", err)
		}
		if blocked {
			return MaintenanceGate{Open: false}, nil
		}
	}
	return MaintenanceGate{Open: true}, nil
}

func (r *Reconciler) applyDecision(ctx context.Context, claimed sqlc.DeliveryRollout, plan FrozenRollout, rowByCluster map[uuid.UUID]sqlc.ListDeliveryRolloutRuntimeRow, decision Decision, now time.Time) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin rollout decision transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := sqlc.New(tx)
	locked, err := queries.GetClaimedDeliveryRolloutForUpdate(ctx, sqlc.GetClaimedDeliveryRolloutForUpdateParams{
		ID: claimed.ID, LeaseOwner: r.owner, ExpectedFence: claimed.FencingGeneration,
	})
	if err != nil {
		return fmt.Errorf("fence rollout decision: %w", err)
	}
	if locked.State != claimed.State || locked.PlanDigest != claimed.PlanDigest {
		return fail(CodeStaleFence, "rollout", "state or plan changed after evaluation")
	}

	for _, transition := range decision.ClusterTransitions {
		row, exists := rowByCluster[transition.ClusterID]
		if !exists {
			return fail(CodeInvariant, "cluster_transition", "cluster is absent from runtime snapshot")
		}
		transitioned, err := queries.ApplyDeliveryRolloutClusterTransitionCAS(ctx, sqlc.ApplyDeliveryRolloutClusterTransitionCASParams{
			ToState: string(transition.To), LastErrorCode: transition.Code, ID: row.ID,
			FromState: string(transition.From), ExpectedClusterFence: transition.ExpectedFence,
			ExpectedLeaseOwner: r.owner, ExpectedRolloutFence: claimed.FencingGeneration,
		})
		if err != nil {
			return fmt.Errorf("apply cluster transition %s: %w", transition.ClusterID, err)
		}
		if err := appendRolloutEvent(ctx, queries, claimed.ID, transition.ClusterID, decision.ID, "cluster_transition", string(transition.From), string(transition.To), transition.Code, transitioned.Fence, now); err != nil {
			return err
		}
	}

	for _, release := range decision.Releases {
		row, exists := rowByCluster[release.ClusterID]
		if !exists {
			return fail(CodeInvariant, "release", "cluster is absent from runtime snapshot")
		}
		if row.ReleaseOrder < 0 || int(row.ReleaseOrder) >= len(plan.Clusters) {
			return fail(CodeInvariant, "release_order", "stored release order is outside the frozen plan")
		}
		planned := plan.Clusters[int(row.ReleaseOrder)]
		if planned.ClusterID != release.ClusterID {
			return fail(CodeInvariant, "release_order", "stored release order does not identify the released cluster")
		}
		previousVersion := pgtype.UUID{}
		if planned.Previous != nil {
			previousVersion = pgtype.UUID{Bytes: planned.Previous.Version.BundleVersionID, Valid: true}
		}
		deployment, err := queries.UpsertClusterDeploymentDesired(ctx, sqlc.UpsertClusterDeploymentDesiredParams{
			TargetID: plan.TargetID, ClusterID: release.ClusterID,
			CurrentRolloutID: pgtype.UUID{Bytes: plan.ID, Valid: true}, DesiredBundleVersionID: pgtype.UUID{Bytes: release.Version.BundleVersionID, Valid: true},
			PreviousBundleVersionID: previousVersion, DesiredGeneration: release.Generation,
			DesiredSpecDigest: release.Version.SpecDigest.String(), DesiredRevision: release.Version.Source.Revision.Value,
			Action: string(model.ActionApply), Phase: string(model.DeploymentPending),
		})
		if err != nil {
			return fmt.Errorf("persist desired deployment for cluster %s: %w", release.ClusterID, err)
		}
		released, err := queries.ReleaseDeliveryRolloutClusterCAS(ctx, sqlc.ReleaseDeliveryRolloutClusterCASParams{
			ToState: string(release.ToState), AssignmentAction: string(release.Action), Deadline: timestamptz(release.Deadline),
			ID: row.ID, FromState: string(release.FromState), ExpectedClusterFence: release.ExpectedFence,
			ExpectedLeaseOwner: r.owner, ExpectedRolloutFence: claimed.FencingGeneration,
		})
		if err != nil {
			return fmt.Errorf("release rollout cluster %s: %w", release.ClusterID, err)
		}
		fromPhase := ""
		if row.DeploymentPhase.Valid {
			fromPhase = row.DeploymentPhase.String
		}
		if _, err := queries.CreateClusterDeploymentEvent(ctx, sqlc.CreateClusterDeploymentEventParams{
			DeploymentID: deployment.ID, RolloutID: pgtype.UUID{Bytes: plan.ID, Valid: true}, EventType: "desired_release",
			FromPhase: fromPhase, ToPhase: string(model.DeploymentPending), Generation: release.Generation,
			SpecDigest: release.Version.SpecDigest.String(), ReasonCode: string(release.Action), Message: "",
			ObservedAt: now,
		}); err != nil {
			return fmt.Errorf("append desired deployment event: %w", err)
		}
		if err := appendRolloutEvent(ctx, queries, claimed.ID, release.ClusterID, decision.ID, "cluster_released", string(release.FromState), string(release.ToState), string(release.Action), released.Fence, now); err != nil {
			return err
		}
	}

	if len(decision.ClusterTransitions) != 0 || len(decision.Releases) != 0 {
		if _, err := queries.RecomputeDeliveryRolloutCounters(ctx, claimed.ID); err != nil {
			return fmt.Errorf("recompute rollout counters: %w", err)
		}
	}
	if decision.RolloutTransition != nil {
		transition := decision.RolloutTransition
		if _, err := queries.ApplyDeliveryRolloutTransitionCAS(ctx, sqlc.ApplyDeliveryRolloutTransitionCASParams{
			ToState: string(transition.To), DecisionDigest: decision.ID.String(), LastErrorCode: transition.Code,
			ID: claimed.ID, FromState: string(transition.From), ExpectedLeaseOwner: r.owner, ExpectedFence: transition.ExpectedFence,
		}); err != nil {
			return fmt.Errorf("apply rollout transition: %w", err)
		}
		if err := appendRolloutEvent(ctx, queries, claimed.ID, uuid.Nil, decision.ID, "rollout_transition", string(transition.From), string(transition.To), transition.Code, transition.ExpectedFence, now); err != nil {
			return err
		}
	} else {
		if _, err := queries.ReleaseDeliveryRolloutLease(ctx, sqlc.ReleaseDeliveryRolloutLeaseParams{
			DecisionDigest: decision.ID.String(), ID: claimed.ID, ExpectedLeaseOwner: r.owner, ExpectedFence: claimed.FencingGeneration,
		}); err != nil {
			return fmt.Errorf("release rollout lease: %w", err)
		}
	}
	if shouldWakeAgain(plan, decision) {
		if err := enqueueRolloutWake(ctx, queries, claimed.ID, decision.ID, now.Add(time.Second)); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit rollout decision: %w", err)
	}
	return nil
}

func appendRolloutEvent(ctx context.Context, queries *sqlc.Queries, rolloutID, clusterID uuid.UUID, decisionID model.Digest, eventType, from, to, reason string, fence int64, now time.Time) error {
	cluster := pgtype.UUID{}
	if clusterID != uuid.Nil {
		cluster = pgtype.UUID{Bytes: clusterID, Valid: true}
	}
	if _, err := queries.CreateDeliveryRolloutEvent(ctx, sqlc.CreateDeliveryRolloutEventParams{
		RolloutID: rolloutID, ClusterID: cluster, DecisionDigest: decisionID.String(), EventType: eventType,
		FromState: from, ToState: to, ReasonCode: reason, Fence: fence, OccurredAt: now,
	}); err != nil {
		return fmt.Errorf("append delivery rollout event: %w", err)
	}
	return nil
}

func shouldWakeAgain(plan FrozenRollout, decision Decision) bool {
	if len(decision.Releases) != 0 || len(decision.ClusterTransitions) != 0 {
		return true
	}
	if decision.RolloutTransition == nil {
		return false
	}
	switch decision.RolloutTransition.To {
	case model.RolloutQueued, model.RolloutProgressing, model.RolloutRollingBack:
		return true
	case model.RolloutFailed:
		return plan.Strategy.OnFailure == model.FailureRollback
	default:
		return false
	}
}

func enqueueRolloutWake(ctx context.Context, queries *sqlc.Queries, rolloutID uuid.UUID, decisionID model.Digest, at time.Time) error {
	payload, _ := json.Marshal(struct {
		RolloutID uuid.UUID `json:"rollout_id"`
	}{rolloutID})
	_, err := queries.UpsertTaskOutbox(ctx, sqlc.UpsertTaskOutboxParams{
		DedupeKey: pgtype.Text{String: "delivery-rollout:" + rolloutID.String() + ":" + decisionID.String(), Valid: true},
		TaskType:  TaskType, Payload: payload, QueueName: "default", MaxRetry: defaultTaskMaxRetries,
		TimeoutSeconds: int32(defaultTaskTimeout / time.Second), UniqueSeconds: 1,
		MaxDeliveryAttempts: 20, NextAttemptAt: timestamptz(at),
	})
	if err != nil {
		return fmt.Errorf("enqueue next delivery rollout wake: %w", err)
	}
	return nil
}

func interval(value time.Duration) pgtype.Interval {
	return pgtype.Interval{Microseconds: value.Microseconds(), Valid: true}
}
