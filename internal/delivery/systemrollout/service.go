// Package systemrollout owns staged upgrades of the privileged downstream
// Astronomer agent and Flux distribution. It is deliberately separate from
// workload rollouts: only immutable releases inserted from the signed platform
// release contract can be selected, and its assignments never contain user
// supplied manifests.
package systemrollout

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/placement"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/rollout"
)

const advisoryLockName = "astronomer-delivery-system-rollout"

var (
	ErrNotFound          = errors.New("delivery system rollout not found")
	ErrConflict          = errors.New("delivery system rollout conflict")
	ErrPrecondition      = errors.New("delivery system rollout precondition failed")
	ErrInvalidTransition = errors.New("invalid delivery system rollout transition")
)

// View is the public, credential-free representation of a system rollout.
type View struct {
	ID                uuid.UUID             `json:"id"`
	ReleaseID         uuid.UUID             `json:"release_id"`
	PreviousReleaseID *uuid.UUID            `json:"previous_release_id,omitempty"`
	Strategy          model.RolloutStrategy `json:"strategy"`
	StrategyDigest    string                `json:"strategy_digest"`
	State             string                `json:"state"`
	FencingGeneration int64                 `json:"fencing_generation"`
	TotalClusters     int32                 `json:"total_clusters"`
	ReadyClusters     int32                 `json:"ready_clusters"`
	FailedClusters    int32                 `json:"failed_clusters"`
	ReleasedClusters  int32                 `json:"released_clusters"`
	ProgressDeadline  *time.Time            `json:"progress_deadline,omitempty"`
	StartedAt         *time.Time            `json:"started_at,omitempty"`
	CompletedAt       *time.Time            `json:"completed_at,omitempty"`
	LastErrorCode     string                `json:"last_error_code,omitempty"`
	CreatedAt         time.Time             `json:"created_at"`
	UpdatedAt         time.Time             `json:"updated_at"`
}

type Assignment struct {
	ClusterID                  uuid.UUID  `json:"cluster_id"`
	Generation                 int64      `json:"generation"`
	Cohort                     int32      `json:"cohort"`
	ReleaseOrder               int32      `json:"release_order"`
	Phase                      string     `json:"phase"`
	ObservedDistributionDigest string     `json:"observed_distribution_digest,omitempty"`
	ObservedAgentVersion       string     `json:"observed_agent_version,omitempty"`
	LastErrorCode              string     `json:"last_error_code,omitempty"`
	LastObservedAt             *time.Time `json:"last_observed_at,omitempty"`
}

type StartRequest struct {
	ReleaseID      uuid.UUID
	Strategy       model.RolloutStrategy
	IdempotencyKey string
	ActorID        uuid.UUID
	Audit          audit.Intent
}

type Action string

const (
	ActionApprove  Action = "approve"
	ActionPause    Action = "pause"
	ActionResume   Action = "resume"
	ActionAbort    Action = "abort"
	ActionRetry    Action = "retry"
	ActionRollback Action = "rollback"
)

type Service struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

func (s *Service) persistAudit(ctx context.Context, tx pgx.Tx, intent audit.Intent, view View) error {
	if intent.IsZero() {
		return audit.ErrOutboxUnavailable
	}
	intent.Event.ResourceID = view.ID.String()
	if intent.Event.Detail == nil {
		intent.Event.Detail = map[string]any{}
	}
	intent.Event.Detail["release_id"] = view.ReleaseID.String()
	intent.Event.Detail["strategy_digest"] = view.StrategyDigest
	intent.Event.Detail["cluster_count"] = view.TotalClusters
	intent.Event.Detail["state"] = view.State
	intent.Event.Detail["fencing_generation"] = view.FencingGeneration
	return audit.RecordIntent(ctx, sqlc.New(tx), intent)
}

func New(pool *pgxpool.Pool) (*Service, error) {
	if pool == nil {
		return nil, errors.New("delivery system rollout database is required")
	}
	return &Service{pool: pool, now: time.Now}, nil
}

// Start freezes the managed-cluster set and deterministic cohorts in the same
// serializable transaction that inserts the rollout and assignments. Startup
// never calls this method; promotion is always an explicit platform action.
func (s *Service) Start(ctx context.Context, request StartRequest) (View, error) {
	if request.Audit.IsZero() {
		return View{}, audit.ErrOutboxUnavailable
	}
	if request.ReleaseID == uuid.Nil {
		return View{}, fmt.Errorf("%w: release_id is required", ErrPrecondition)
	}
	if err := request.Strategy.Validate(); err != nil {
		return View{}, fmt.Errorf("%w: invalid strategy: %v", ErrPrecondition, err)
	}
	if request.Strategy.Type == model.StrategyPartitioned {
		return View{}, fmt.Errorf("%w: system rollouts do not accept user-defined partitions", ErrPrecondition)
	}
	key := strings.TrimSpace(request.IdempotencyKey)
	if key == "" || len(key) > rollout.MaxIdempotencyKeyLength || strings.ContainsAny(key, "\r\n\x00") {
		return View{}, fmt.Errorf("%w: invalid idempotency key", ErrPrecondition)
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return View{}, fmt.Errorf("begin system rollout: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New(tx)
	if err := q.LockSystemRolloutTransaction(ctx, advisoryLockName); err != nil {
		return View{}, fmt.Errorf("lock system rollout: %w", err)
	}

	if existing, err := getByIdempotency(ctx, tx, key); err == nil {
		if existing.ReleaseID != request.ReleaseID {
			return View{}, fmt.Errorf("%w: idempotency key belongs to another release", ErrConflict)
		}
		if err := s.persistAudit(ctx, tx, request.Audit, existing); err != nil {
			return View{}, err
		}
		return existing, tx.Commit(ctx)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return View{}, err
	}

	releaseState, err := q.GetSystemRolloutReleaseStateForUpdate(ctx, request.ReleaseID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return View{}, fmt.Errorf("%w: release does not exist", ErrNotFound)
		}
		return View{}, fmt.Errorf("load system release: %w", err)
	}
	if releaseState != "draft" {
		return View{}, fmt.Errorf("%w: system release must be draft", ErrPrecondition)
	}
	active, err := q.CountActiveSystemRollouts(ctx)
	if err != nil {
		return View{}, fmt.Errorf("count active system rollouts: %w", err)
	}
	if active != 0 {
		return View{}, fmt.Errorf("%w: another system rollout is active", ErrConflict)
	}

	previousID, err := q.GetCurrentSystemRolloutRelease(ctx)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return View{}, fmt.Errorf("load current system release: %w", err)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return View{}, fmt.Errorf("%w: no current system release exists", ErrPrecondition)
	}

	previous := pgtype.UUID{Bytes: previousID, Valid: true}
	ids, err := q.ListSystemRolloutEligibleClusters(ctx)
	if err != nil {
		return View{}, fmt.Errorf("snapshot managed clusters: %w", err)
	}
	var candidates []placement.Candidate
	for _, id := range ids {
		candidates = append(candidates, placement.Candidate{ID: id})
	}

	if len(candidates) == 0 {
		return View{}, fmt.Errorf("%w: no enrolled managed clusters are eligible", ErrPrecondition)
	}
	_, planned, err := rollout.BuildCohorts(request.Strategy, candidates)
	if err != nil {
		return View{}, fmt.Errorf("%w: build system cohorts: %v", ErrPrecondition, err)
	}
	strategyJSON, strategyDigest, err := canonicalStrategy(request.Strategy)
	if err != nil {
		return View{}, err
	}
	now := s.now().UTC()
	deadline := now.Add(time.Duration(request.Strategy.ProgressDeadline))
	rolloutID := uuid.New()
	state := "awaiting_approval"
	var initiatedBy pgtype.UUID
	if request.ActorID != uuid.Nil {
		initiatedBy = pgtype.UUID{Bytes: request.ActorID, Valid: true}
	}
	if len(planned) > math.MaxInt32 {
		return View{}, fmt.Errorf("%w: managed cluster count exceeds supported range", ErrPrecondition)
	}
	record, err := q.CreateSystemRollout(ctx, sqlc.CreateSystemRolloutParams{ID: rolloutID, ReleaseID: request.ReleaseID, PreviousReleaseID: previous, Strategy: strategyJSON, StrategyDigest: strategyDigest, State: state, TotalClusters: int32(len(planned)), IdempotencyKey: key, ProgressDeadline: timestamp(deadline), InitiatedBy: initiatedBy})
	view, err := viewFromRecord(record, err)
	if err != nil {
		return View{}, fmt.Errorf("insert system rollout: %w", err)
	}
	for _, item := range planned {
		if item.Cohort < 0 || item.Cohort > math.MaxInt32 || item.Order < 0 || item.Order > math.MaxInt32 {
			return View{}, fmt.Errorf("%w: cohort index exceeds supported range", ErrPrecondition)
		}
		if err := q.EnsureSystemRolloutAssignment(ctx, sqlc.EnsureSystemRolloutAssignmentParams{ClusterID: item.ClusterID, DesiredReleaseID: request.ReleaseID, PreviousReleaseID: previous, RolloutID: uuidValue(rolloutID), Cohort: int32(item.Cohort), ReleaseOrder: int32(item.Order), Deadline: timestamp(deadline)}); err != nil {
			return View{}, fmt.Errorf("insert system assignment: %w", err)
		}
	}

	if err := appendEvent(ctx, tx, rolloutID, request.ReleaseID, 1, "rollout_created", "", state, "approval_required", strategyDigest, now); err != nil {
		return View{}, err
	}
	if err := s.persistAudit(ctx, tx, request.Audit, view); err != nil {
		return View{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return View{}, fmt.Errorf("commit system rollout: %w", err)
	}
	return view, nil
}

func (s *Service) Get(ctx context.Context, id uuid.UUID) (View, error) {
	view, err := viewFromRecord(sqlc.New(s.pool).GetSystemRollout(ctx, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return View{}, ErrNotFound
	}
	return view, err
}

func (s *Service) Assignments(ctx context.Context, id uuid.UUID) ([]Assignment, error) {
	rows, err := sqlc.New(s.pool).ListSystemRolloutAssignments(ctx, uuidValue(id))
	if err != nil {
		return nil, err
	}
	items := make([]Assignment, 0, len(rows))
	for _, row := range rows {
		items = append(items, Assignment{ClusterID: row.ClusterID, Generation: row.Generation, Cohort: row.Cohort, ReleaseOrder: row.ReleaseOrder, Phase: row.Phase, ObservedDistributionDigest: row.ObservedDistributionDigest, ObservedAgentVersion: row.ObservedAgentVersion, LastErrorCode: row.LastErrorCode, LastObservedAt: timePointer(row.LastObservedAt)})
	}
	return items, nil
}

// Act performs a user-requested CAS transition. The periodic reconciler owns
// cohort release and completion, keeping HTTP requests bounded regardless of
// cluster count.
func (s *Service) Act(ctx context.Context, id uuid.UUID, expectedFence int64, action Action, actor uuid.UUID, reason string, intent audit.Intent) (View, error) {
	if intent.IsZero() {
		return View{}, audit.ErrOutboxUnavailable
	}
	if id == uuid.Nil || expectedFence < 1 {
		return View{}, ErrPrecondition
	}
	if len(reason) > 96 || strings.ContainsAny(reason, "\r\n\x00") {
		return View{}, fmt.Errorf("%w: invalid reason code", ErrPrecondition)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return View{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New(tx)
	if err := q.LockSystemRolloutTransaction(ctx, advisoryLockName); err != nil {
		return View{}, err
	}
	current, err := getViewForUpdate(ctx, tx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return View{}, ErrNotFound
	}
	if err != nil {
		return View{}, err
	}
	if current.FencingGeneration != expectedFence {
		return View{}, ErrConflict
	}
	next, errorCode, err := actionTransition(current.State, action, current.PreviousReleaseID != nil)
	if err != nil {
		return View{}, err
	}
	now := s.now().UTC()
	if action == ActionRollback {
		if err := beginRollback(ctx, tx, current, "manual_rollback", now); err != nil {
			return View{}, err
		}
	}
	if action == ActionRetry {
		if err := q.RetrySystemRolloutAssignments(ctx, uuidValue(id)); err != nil {
			return View{}, err
		}
	}
	if action == ActionAbort {
		if err := q.AbortSystemRolloutPendingAssignments(ctx, uuidValue(id)); err != nil {
			return View{}, err
		}
	}
	var completed pgtype.Timestamptz
	if next == "aborted" {
		completed = timestamp(now)
	}
	updated, err := viewFromRecord(q.ActSystemRollout(ctx, sqlc.ActSystemRolloutParams{ID: id, State: next, LastErrorCode: errorCode, StartedAt: timestamp(now), CompletedAt: completed, FencingGeneration: expectedFence}))

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return View{}, ErrConflict
		}
		return View{}, err
	}
	actorDigest := decisionDigest(action, actor, reason, updated.FencingGeneration)
	if err := appendEvent(ctx, tx, id, current.ReleaseID, updated.FencingGeneration,
		"rollout_"+string(action), current.State, next, reason, actorDigest, now); err != nil {
		return View{}, err
	}
	if err := s.persistAudit(ctx, tx, intent, updated); err != nil {
		return View{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return View{}, err
	}
	return updated, nil
}

func actionTransition(state string, action Action, hasPrevious bool) (string, string, error) {
	switch action {
	case ActionApprove:
		if state == "awaiting_approval" {
			return "queued", "", nil
		}
	case ActionPause:
		if state == "queued" || state == "progressing" {
			return "paused", "operator_paused", nil
		}
	case ActionResume:
		if state == "paused" {
			return "queued", "", nil
		}
	case ActionAbort:
		if state == "awaiting_approval" || state == "queued" || state == "progressing" || state == "paused" {
			return "aborted", "operator_aborted", nil
		}
	case ActionRetry:
		if state == "failed" || state == "rollback_failed" {
			return "queued", "", nil
		}
	case ActionRollback:
		if !hasPrevious {
			return "", "", fmt.Errorf("%w: no previous signed release", ErrPrecondition)
		}
		if state == "progressing" || state == "paused" || state == "failed" || state == "succeeded" {
			return "rolling_back", "manual_rollback", nil
		}
	default:
		return "", "", fmt.Errorf("%w: unsupported action", ErrPrecondition)
	}
	return "", "", fmt.Errorf("%w: %s from %s", ErrInvalidTransition, action, state)
}

// ReconcileOne advances a single rollout under an advisory lock. Status ingest
// is the only writer of Ready/failed observations; this loop only aggregates,
// releases a bounded cohort, and promotes or rolls back immutable releases.
func (s *Service) ReconcileOne(ctx context.Context, id uuid.UUID) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New(tx)
	if err := q.LockSystemRolloutTransaction(ctx, advisoryLockName); err != nil {
		return err
	}
	current, err := getViewForUpdate(ctx, tx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if terminal(current.State) || current.State == "awaiting_approval" || current.State == "paused" {
		return tx.Commit(ctx)
	}
	now := s.now().UTC()
	counts, err := assignmentCounts(ctx, tx, id, time.Duration(current.Strategy.MinReady))
	if err != nil {
		return err
	}
	current.ReadyClusters = counts.ready
	current.FailedClusters = counts.failed
	current.ReleasedClusters = counts.released

	if current.State == "rolling_back" {
		if counts.rollbackFailed > 0 {
			return finish(ctx, tx, current, "rollback_failed", "rollback_failed", now)
		}
		// Only assignments released before rollback are rollback targets. Pending
		// cohorts never left the previous release and are intentionally excluded
		// by beginRollback; waiting for total_clusters here makes a partial
		// canary rollback impossible to complete.
		if rollbackComplete(counts) {
			return finish(ctx, tx, current, "rolled_back", "", now)
		}
		return updateCountersAndCommit(ctx, tx, current)
	}

	if current.State == "queued" {
		released, err := releaseNextCohort(ctx, tx, current.ID, now, systemReleaseSlots(current.Strategy, current.TotalClusters, counts))
		if err != nil {
			return err
		}
		if released > 0 {
			current.State = "progressing"
			current.ReleasedClusters += released
			// Later cohort checks must include assignments released by this
			// transaction, rather than the counts from before its first write.
			counts.inFlight += released
			counts.pending -= released
			if err := appendEvent(ctx, tx, current.ID, current.ReleaseID, current.FencingGeneration+1,
				"cohort_released", "queued", "progressing", "", current.StrategyDigest, now); err != nil {
				return err
			}
		}
	}

	if current.FailedClusters > 0 && failureExceeded(current.Strategy.FailureThreshold, int(current.TotalClusters), int(current.FailedClusters)) {
		switch current.Strategy.OnFailure {
		case model.FailureRollback:
			if current.PreviousReleaseID == nil {
				return finish(ctx, tx, current, "failed", "failure_budget_exceeded_no_rollback", now)
			}
			if err := beginRollback(ctx, tx, current, "failure_budget_exceeded", now); err != nil {
				return err
			}
			current.State = "rolling_back"
		case model.FailureAbort:
			return finish(ctx, tx, current, "failed", "failure_budget_exceeded", now)
		default:
			current.State = "paused"
			current.LastErrorCode = "failure_budget_exceeded"
		}
	}
	if current.ProgressDeadline != nil && !now.Before(*current.ProgressDeadline) && current.State != "rolling_back" {
		if current.Strategy.OnFailure == model.FailureRollback && current.PreviousReleaseID != nil {
			if err := beginRollback(ctx, tx, current, "progress_deadline_exceeded", now); err != nil {
				return err
			}
			current.State = "rolling_back"
			current.LastErrorCode = "progress_deadline_exceeded"
		} else {
			return finish(ctx, tx, current, "failed", "progress_deadline_exceeded", now)
		}
	}
	if current.State == "progressing" && current.ReadyClusters == current.TotalClusters {
		if err := q.RetirePreviousSystemRolloutReleases(ctx, sqlc.RetirePreviousSystemRolloutReleasesParams{ID: current.ReleaseID, RetiredAt: timestamp(now)}); err != nil {
			return err
		}
		if affected, err := q.PromoteSystemRolloutRelease(ctx, sqlc.PromoteSystemRolloutReleaseParams{ID: current.ReleaseID, ReleasedAt: timestamp(now)}); err != nil {
			return err
		} else if affected != 1 {
			return fmt.Errorf("promote system release: %w", ErrConflict)
		}
		return finish(ctx, tx, current, "succeeded", "", now)
	}
	if current.State == "progressing" && counts.inFlight == 0 && counts.immatureReady == 0 && counts.pending > 0 {
		if current.Strategy.Type == model.StrategyCanary && current.Strategy.Canary != nil &&
			current.Strategy.Canary.ApprovalAfterCanary && counts.maxReadyCohort == 0 && counts.pendingCanary == 0 {
			current.State = "awaiting_approval"
		} else {
			released, releaseErr := releaseNextCohort(ctx, tx, current.ID, now, systemReleaseSlots(current.Strategy, current.TotalClusters, counts))
			if releaseErr != nil {
				return releaseErr
			}
			current.ReleasedClusters += released
		}
	}
	return updateCountersAndCommit(ctx, tx, current)
}

func (s *Service) Sweep(ctx context.Context, limit int) error {
	if limit < 1 || limit > 100 {
		limit = 16
	}
	ids, err := sqlc.New(s.pool).ListReconcilableSystemRollouts(ctx, int32(limit))
	if err != nil {
		return err
	}

	for _, id := range ids {
		if err := s.ReconcileOne(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

type counts struct {
	ready, failed, released, pending, inFlight, immatureReady  int32
	rolledBack, rollbackFailed, rollbackTargets, pendingCanary int32
	maxReadyCohort                                             int32
}

func assignmentCounts(ctx context.Context, tx pgx.Tx, id uuid.UUID, minReady time.Duration) (counts, error) {
	row, err := sqlc.New(tx).GetSystemRolloutAssignmentCounts(ctx, sqlc.GetSystemRolloutAssignmentCountsParams{RolloutID: uuidValue(id), MinReadyMs: minReady.Milliseconds()})
	if err != nil {
		return counts{}, err
	}
	return counts{ready: row.Ready, failed: row.Failed, released: row.Released, pending: row.Pending, inFlight: row.InFlight, immatureReady: row.ImmatureReady, rolledBack: row.RolledBack, rollbackFailed: row.RollbackFailed, rollbackTargets: row.RollbackTargets, pendingCanary: row.PendingCanary, maxReadyCohort: row.MaxReadyCohort}, nil
}
func releaseNextCohort(ctx context.Context, tx pgx.Tx, id uuid.UUID, now time.Time, limit int32) (int32, error) {
	if limit <= 0 {
		return 0, nil
	}
	affected, err := sqlc.New(tx).ReleaseSystemRolloutNextCohort(ctx, sqlc.ReleaseSystemRolloutNextCohortParams{RolloutID: uuidValue(id), ReleasedAt: timestamp(now), ReleaseLimit: limit})
	if err != nil {
		return 0, err
	}
	if affected < 0 || affected > int64(limit) {
		return 0, errors.New("system rollout release count exceeds requested limit")
	}
	return int32(affected), nil
}

func rollbackComplete(current counts) bool {
	return current.rollbackFailed == 0 && current.rollbackTargets > 0 && current.rolledBack == current.rollbackTargets
}

func beginRollback(ctx context.Context, tx pgx.Tx, current View, reason string, now time.Time) error {
	if current.PreviousReleaseID == nil {
		return fmt.Errorf("%w: previous release is unavailable", ErrPrecondition)
	}
	if err := sqlc.New(tx).BeginSystemRolloutAssignmentRollback(ctx, sqlc.BeginSystemRolloutAssignmentRollbackParams{RolloutID: uuidValue(current.ID), DesiredReleaseID: *current.PreviousReleaseID, PreviousReleaseID: uuidValue(current.ReleaseID), ReleasedAt: timestamp(now)}); err != nil {
		return err
	}

	return appendEvent(ctx, tx, current.ID, current.ReleaseID, current.FencingGeneration+1,
		"rollback_started", current.State, "rolling_back", reason, current.StrategyDigest, now)
}

func failureExceeded(amount model.Amount, total, failed int) bool {
	if failed == 0 {
		return false
	}
	switch amount.Type {
	case model.AmountCount:
		return failed >= int(amount.Value)
	case model.AmountPercent:
		return failed*100 >= total*int(amount.Value)
	default:
		return true
	}
}

func terminal(state string) bool {
	switch state {
	case "aborted", "succeeded", "failed", "rolled_back", "rollback_failed":
		return true
	default:
		return false
	}
}

func finish(ctx context.Context, tx pgx.Tx, current View, state, code string, now time.Time) error {
	from := current.State
	current.State = state
	current.LastErrorCode = code
	if err := sqlc.New(tx).FinishSystemRollout(ctx, sqlc.FinishSystemRolloutParams{ID: current.ID, State: state, ReadyClusters: current.ReadyClusters, FailedClusters: current.FailedClusters, ReleasedClusters: current.ReleasedClusters, LastErrorCode: code, CompletedAt: timestamp(now)}); err != nil {
		return err
	}

	if err := appendEvent(ctx, tx, current.ID, current.ReleaseID, current.FencingGeneration+1,
		"rollout_"+state, from, state, code, current.StrategyDigest, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func updateCountersAndCommit(ctx context.Context, tx pgx.Tx, current View) error {
	if err := sqlc.New(tx).UpdateSystemRolloutCounters(ctx, sqlc.UpdateSystemRolloutCountersParams{ID: current.ID, State: current.State, ReadyClusters: current.ReadyClusters, FailedClusters: current.FailedClusters, ReleasedClusters: current.ReleasedClusters, LastErrorCode: current.LastErrorCode}); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func canonicalStrategy(strategy model.RolloutStrategy) ([]byte, string, error) {
	digest, err := strategy.CanonicalDigest()
	if err != nil {
		return nil, "", err
	}
	canonical := strategy
	if strategy.Canary != nil {
		canary := *strategy.Canary
		canary.ClusterIDs = append([]uuid.UUID(nil), canary.ClusterIDs...)
		sort.Slice(canary.ClusterIDs, func(i, j int) bool {
			return strings.Compare(canary.ClusterIDs[i].String(), canary.ClusterIDs[j].String()) < 0
		})
		canonical.Canary = &canary
	}
	payload, err := json.Marshal(canonical)
	if err != nil {
		return nil, "", err
	}
	return payload, digest.String(), nil
}

func decisionDigest(action Action, actor uuid.UUID, reason string, fence int64) string {
	payload, _ := json.Marshal(struct {
		Action Action    `json:"action"`
		Actor  uuid.UUID `json:"actor"`
		Reason string    `json:"reason"`
		Fence  int64     `json:"fence"`
	}{action, actor, reason, fence})
	digest := sha256.Sum256(payload)
	return fmt.Sprintf("sha256:%x", digest)
}

func appendEvent(ctx context.Context, tx pgx.Tx, rolloutID, releaseID uuid.UUID, generation int64, eventType, from, to, reason, digest string, now time.Time) error {
	return sqlc.New(tx).AppendSystemRolloutEvent(ctx, sqlc.AppendSystemRolloutEventParams{RolloutID: uuidValue(rolloutID), ReleaseID: releaseID, Generation: generation, EventType: eventType, FromPhase: from, ToPhase: to, ReasonCode: reason, DecisionDigest: digest, OccurredAt: now})
}

func viewFromRecord(row sqlc.DeliverySystemRollout, err error) (View, error) {
	if err != nil {
		return View{}, err
	}
	value := View{ID: row.ID, ReleaseID: row.ReleaseID, StrategyDigest: row.StrategyDigest, State: row.State, FencingGeneration: row.FencingGeneration, TotalClusters: row.TotalClusters, ReadyClusters: row.ReadyClusters, FailedClusters: row.FailedClusters, ReleasedClusters: row.ReleasedClusters, LastErrorCode: row.LastErrorCode, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
	if value.Strategy, err = model.ReadRolloutStrategyV1(row.Strategy); err != nil {
		return View{}, fmt.Errorf("decode persisted system strategy: %w", err)
	}
	if row.PreviousReleaseID.Valid {
		id := uuid.UUID(row.PreviousReleaseID.Bytes)
		value.PreviousReleaseID = &id
	}
	value.ProgressDeadline = timePointer(row.ProgressDeadline)
	value.StartedAt = timePointer(row.StartedAt)
	value.CompletedAt = timePointer(row.CompletedAt)
	return value, nil
}
func getViewForUpdate(ctx context.Context, tx pgx.Tx, id uuid.UUID) (View, error) {
	return viewFromRecord(sqlc.New(tx).GetSystemRolloutForUpdate(ctx, id))
}
func getByIdempotency(ctx context.Context, tx pgx.Tx, key string) (View, error) {
	return viewFromRecord(sqlc.New(tx).GetSystemRolloutByIdempotency(ctx, key))
}
func timestamp(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: true}
}
func uuidValue(value uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: value, Valid: true} }

func timePointer(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	timestamp := value.Time.UTC()
	return &timestamp
}
