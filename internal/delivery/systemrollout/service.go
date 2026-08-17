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
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

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
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, advisoryLockName); err != nil {
		return View{}, fmt.Errorf("lock system rollout: %w", err)
	}

	if existing, err := getByIdempotency(ctx, tx, key); err == nil {
		if existing.ReleaseID != request.ReleaseID {
			return View{}, fmt.Errorf("%w: idempotency key belongs to another release", ErrConflict)
		}
		return existing, tx.Commit(ctx)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return View{}, err
	}

	var releaseState string
	if err := tx.QueryRow(ctx, `SELECT state FROM delivery_system_releases WHERE id=$1 FOR UPDATE`, request.ReleaseID).Scan(&releaseState); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return View{}, fmt.Errorf("%w: release does not exist", ErrNotFound)
		}
		return View{}, fmt.Errorf("load system release: %w", err)
	}
	if releaseState != "draft" {
		return View{}, fmt.Errorf("%w: system release must be draft", ErrPrecondition)
	}
	var active int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM delivery_system_rollouts WHERE state IN ('awaiting_approval','queued','progressing','paused','rolling_back')`).Scan(&active); err != nil {
		return View{}, fmt.Errorf("count active system rollouts: %w", err)
	}
	if active != 0 {
		return View{}, fmt.Errorf("%w: another system rollout is active", ErrConflict)
	}

	var previous pgtype.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM delivery_system_releases WHERE state='released'`).Scan(&previous); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return View{}, fmt.Errorf("load current system release: %w", err)
	}
	if !previous.Valid {
		return View{}, fmt.Errorf("%w: no current system release exists", ErrPrecondition)
	}

	rows, err := tx.Query(ctx, `
		SELECT c.id
		FROM clusters c
		WHERE c.decommissioned_at IS NULL
		  AND c.is_local = false
		  AND c.registration_phase = 'ready'
		  AND EXISTS (SELECT 1 FROM cluster_agent_tokens t WHERE t.cluster_id=c.id AND t.revoked_at IS NULL)
		ORDER BY c.id`)
	if err != nil {
		return View{}, fmt.Errorf("snapshot managed clusters: %w", err)
	}
	var candidates []placement.Candidate
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return View{}, fmt.Errorf("scan managed cluster: %w", err)
		}
		candidates = append(candidates, placement.Candidate{ID: id})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return View{}, fmt.Errorf("snapshot managed clusters: %w", err)
	}
	rows.Close()
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
	row := tx.QueryRow(ctx, `
		INSERT INTO delivery_system_rollouts (
			id,release_id,previous_release_id,strategy,strategy_digest,state,total_clusters,
			idempotency_key,progress_deadline,initiated_by
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		RETURNING id,release_id,previous_release_id,strategy,strategy_digest,state,fencing_generation,
			total_clusters,ready_clusters,failed_clusters,released_clusters,progress_deadline,
			started_at,completed_at,last_error_code,created_at,updated_at`,
		rolloutID, request.ReleaseID, previous, strategyJSON, strategyDigest, state,
		len(planned), key, deadline, initiatedBy)
	view, err := scanView(row)
	if err != nil {
		return View{}, fmt.Errorf("insert system rollout: %w", err)
	}
	for _, item := range planned {
		if _, err := tx.Exec(ctx, `
			INSERT INTO delivery_system_cluster_assignments (
				cluster_id,desired_release_id,previous_release_id,rollout_id,generation,cohort,release_order,phase,deadline
			) VALUES ($1,$2,$3,$4,COALESCE((SELECT generation+1 FROM delivery_system_cluster_assignments WHERE cluster_id=$1),1),$5,$6,'pending',$7)
			ON CONFLICT (cluster_id) DO UPDATE SET
				desired_release_id=EXCLUDED.desired_release_id, previous_release_id=EXCLUDED.previous_release_id,
				rollout_id=EXCLUDED.rollout_id, generation=delivery_system_cluster_assignments.generation+1,
				cohort=EXCLUDED.cohort, release_order=EXCLUDED.release_order, phase='pending',
				fence=delivery_system_cluster_assignments.fence+1, released_at=NULL,
				acknowledged_at=NULL, ready_at=NULL, deadline=EXCLUDED.deadline,
				observed_distribution_digest='', observed_agent_version='', last_error_code=''`,
			item.ClusterID, request.ReleaseID, previous, rolloutID, item.Cohort, item.Order, deadline); err != nil {
			return View{}, fmt.Errorf("insert system assignment: %w", err)
		}
	}
	if err := appendEvent(ctx, tx, rolloutID, request.ReleaseID, 1, "rollout_created", "", state, "approval_required", strategyDigest, now); err != nil {
		return View{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return View{}, fmt.Errorf("commit system rollout: %w", err)
	}
	return view, nil
}

func (s *Service) Get(ctx context.Context, id uuid.UUID) (View, error) {
	view, err := getView(ctx, s.pool, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return View{}, ErrNotFound
	}
	return view, err
}

func (s *Service) Assignments(ctx context.Context, id uuid.UUID) ([]Assignment, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT cluster_id,generation,cohort,release_order,phase,observed_distribution_digest,
		       observed_agent_version,last_error_code,last_observed_at
		FROM delivery_system_cluster_assignments WHERE rollout_id=$1
		ORDER BY cohort,release_order,cluster_id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Assignment, 0)
	for rows.Next() {
		var item Assignment
		var observed pgtype.Timestamptz
		if err := rows.Scan(&item.ClusterID, &item.Generation, &item.Cohort, &item.ReleaseOrder,
			&item.Phase, &item.ObservedDistributionDigest, &item.ObservedAgentVersion,
			&item.LastErrorCode, &observed); err != nil {
			return nil, err
		}
		item.LastObservedAt = timePointer(observed)
		items = append(items, item)
	}
	return items, rows.Err()
}

// Act performs a user-requested CAS transition. The periodic reconciler owns
// cohort release and completion, keeping HTTP requests bounded regardless of
// cluster count.
func (s *Service) Act(ctx context.Context, id uuid.UUID, expectedFence int64, action Action, actor uuid.UUID, reason string) (View, error) {
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
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, advisoryLockName); err != nil {
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
		if _, err := tx.Exec(ctx, `UPDATE delivery_system_cluster_assignments SET phase='pending',last_error_code='',released_at=NULL,acknowledged_at=NULL,ready_at=NULL,fence=fence+1 WHERE rollout_id=$1 AND phase='failed'`, id); err != nil {
			return View{}, err
		}
	}
	if action == ActionAbort {
		if _, err := tx.Exec(ctx, `UPDATE delivery_system_cluster_assignments SET phase='aborted',fence=fence+1 WHERE rollout_id=$1 AND phase='pending'`, id); err != nil {
			return View{}, err
		}
	}
	var completed any
	if next == "aborted" {
		completed = now
	}
	row := tx.QueryRow(ctx, `
		UPDATE delivery_system_rollouts SET state=$2,fencing_generation=fencing_generation+1,
			lease_owner='',lease_expires_at=NULL,last_error_code=$3,
			started_at=CASE WHEN $2 IN ('queued','progressing','rolling_back') THEN COALESCE(started_at,$4) ELSE started_at END,
			completed_at=COALESCE($5::timestamptz,completed_at)
		WHERE id=$1 AND fencing_generation=$6
		RETURNING id,release_id,previous_release_id,strategy,strategy_digest,state,fencing_generation,
			total_clusters,ready_clusters,failed_clusters,released_clusters,progress_deadline,
			started_at,completed_at,last_error_code,created_at,updated_at`,
		id, next, errorCode, now, completed, expectedFence)
	updated, err := scanView(row)
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
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, advisoryLockName); err != nil {
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
	counts, err := assignmentCounts(ctx, tx, id)
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
		if counts.rolledBack == current.TotalClusters {
			return finish(ctx, tx, current, "rolled_back", "", now)
		}
		return updateCountersAndCommit(ctx, tx, current)
	}

	if current.State == "queued" {
		released, err := releaseNextCohort(ctx, tx, current.ID, now)
		if err != nil {
			return err
		}
		if released > 0 {
			current.State = "progressing"
			current.ReleasedClusters += released
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
		if _, err := tx.Exec(ctx, `UPDATE delivery_system_releases SET state='retired',retired_at=$2 WHERE state='released' AND id<>$1`, current.ReleaseID, now); err != nil {
			return err
		}
		if tag, err := tx.Exec(ctx, `UPDATE delivery_system_releases SET state='released',released_at=COALESCE(released_at,$2),retired_at=NULL WHERE id=$1 AND state='draft'`, current.ReleaseID, now); err != nil {
			return err
		} else if tag.RowsAffected() != 1 {
			return fmt.Errorf("promote system release: %w", ErrConflict)
		}
		return finish(ctx, tx, current, "succeeded", "", now)
	}
	if current.State == "progressing" && counts.inFlight == 0 && counts.pending > 0 {
		if current.Strategy.Type == model.StrategyCanary && current.Strategy.Canary != nil &&
			current.Strategy.Canary.ApprovalAfterCanary && counts.maxReadyCohort == 0 {
			current.State = "awaiting_approval"
		} else {
			released, releaseErr := releaseNextCohort(ctx, tx, current.ID, now)
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
	rows, err := s.pool.Query(ctx, `SELECT id FROM delivery_system_rollouts WHERE state IN ('queued','progressing','rolling_back') ORDER BY created_at LIMIT $1`, limit)
	if err != nil {
		return err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		if err := s.ReconcileOne(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

type counts struct {
	ready, failed, released, pending, inFlight, rolledBack, rollbackFailed int32
	maxReadyCohort                                                         int32
}

func assignmentCounts(ctx context.Context, tx pgx.Tx, id uuid.UUID) (counts, error) {
	var c counts
	err := tx.QueryRow(ctx, `
		SELECT
			count(*) FILTER (WHERE phase='ready'),
			count(*) FILTER (WHERE phase='failed'),
			count(*) FILTER (WHERE phase<>'pending'),
			count(*) FILTER (WHERE phase='pending'),
			count(*) FILTER (WHERE phase IN ('released','applying')),
			count(*) FILTER (WHERE phase='rolled_back'),
			count(*) FILTER (WHERE phase='rollback_failed'),
			COALESCE(max(cohort) FILTER (WHERE phase='ready'),-1)
		FROM delivery_system_cluster_assignments WHERE rollout_id=$1`, id).
		Scan(&c.ready, &c.failed, &c.released, &c.pending, &c.inFlight, &c.rolledBack, &c.rollbackFailed, &c.maxReadyCohort)
	return c, err
}

func releaseNextCohort(ctx context.Context, tx pgx.Tx, id uuid.UUID, now time.Time) (int32, error) {
	var cohort int32
	if err := tx.QueryRow(ctx, `SELECT min(cohort) FROM delivery_system_cluster_assignments WHERE rollout_id=$1 AND phase='pending'`, id).Scan(&cohort); err != nil {
		return 0, err
	}
	tag, err := tx.Exec(ctx, `UPDATE delivery_system_cluster_assignments SET phase='released',released_at=$3,fence=fence+1 WHERE rollout_id=$1 AND cohort=$2 AND phase='pending'`, id, cohort, now)
	if err != nil {
		return 0, err
	}
	return int32(tag.RowsAffected()), nil
}

func beginRollback(ctx context.Context, tx pgx.Tx, current View, reason string, now time.Time) error {
	if current.PreviousReleaseID == nil {
		return fmt.Errorf("%w: previous release is unavailable", ErrPrecondition)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE delivery_system_cluster_assignments SET
			desired_release_id=$2,previous_release_id=$3,generation=generation+1,phase='rolling_back',
			fence=fence+1,released_at=$4,acknowledged_at=NULL,ready_at=NULL,
			observed_distribution_digest='',observed_agent_version='',last_error_code=''
		WHERE rollout_id=$1 AND phase NOT IN ('pending','aborted')`, current.ID, *current.PreviousReleaseID, current.ReleaseID, now); err != nil {
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
	if _, err := tx.Exec(ctx, `
		UPDATE delivery_system_rollouts SET state=$2,fencing_generation=fencing_generation+1,
			ready_clusters=$3,failed_clusters=$4,released_clusters=$5,last_error_code=$6,
			completed_at=$7,lease_owner='',lease_expires_at=NULL WHERE id=$1`, current.ID, state,
		current.ReadyClusters, current.FailedClusters, current.ReleasedClusters, code, now); err != nil {
		return err
	}
	if err := appendEvent(ctx, tx, current.ID, current.ReleaseID, current.FencingGeneration+1,
		"rollout_"+state, from, state, code, current.StrategyDigest, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func updateCountersAndCommit(ctx context.Context, tx pgx.Tx, current View) error {
	if _, err := tx.Exec(ctx, `
		UPDATE delivery_system_rollouts SET state=$2,fencing_generation=fencing_generation+1,
			ready_clusters=$3,failed_clusters=$4,released_clusters=$5,last_error_code=$6,
			started_at=CASE WHEN $2 IN ('progressing','rolling_back') THEN COALESCE(started_at,now()) ELSE started_at END,
			lease_owner='',lease_expires_at=NULL WHERE id=$1`, current.ID, current.State,
		current.ReadyClusters, current.FailedClusters, current.ReleasedClusters, current.LastErrorCode); err != nil {
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
	_, err := tx.Exec(ctx, `
		INSERT INTO delivery_system_events (rollout_id,release_id,generation,event_type,from_phase,to_phase,reason_code,decision_digest,occurred_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, rolloutID, releaseID, generation, eventType, from, to, reason, digest, now)
	return err
}

type rowScanner interface{ Scan(...any) error }

func scanView(row rowScanner) (View, error) {
	var value View
	var previous pgtype.UUID
	var strategy []byte
	var deadline, started, completed pgtype.Timestamptz
	err := row.Scan(&value.ID, &value.ReleaseID, &previous, &strategy, &value.StrategyDigest,
		&value.State, &value.FencingGeneration, &value.TotalClusters, &value.ReadyClusters,
		&value.FailedClusters, &value.ReleasedClusters, &deadline, &started, &completed,
		&value.LastErrorCode, &value.CreatedAt, &value.UpdatedAt)
	if err != nil {
		return View{}, err
	}
	if err := json.Unmarshal(strategy, &value.Strategy); err != nil {
		return View{}, fmt.Errorf("decode persisted system strategy: %w", err)
	}
	if previous.Valid {
		id := uuid.UUID(previous.Bytes)
		value.PreviousReleaseID = &id
	}
	value.ProgressDeadline = timePointer(deadline)
	value.StartedAt = timePointer(started)
	value.CompletedAt = timePointer(completed)
	return value, nil
}

const viewColumns = `id,release_id,previous_release_id,strategy,strategy_digest,state,fencing_generation,
	total_clusters,ready_clusters,failed_clusters,released_clusters,progress_deadline,
	started_at,completed_at,last_error_code,created_at,updated_at`

func getView(ctx context.Context, db interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, id uuid.UUID) (View, error) {
	return scanView(db.QueryRow(ctx, `SELECT `+viewColumns+` FROM delivery_system_rollouts WHERE id=$1`, id))
}

func getViewForUpdate(ctx context.Context, tx pgx.Tx, id uuid.UUID) (View, error) {
	return scanView(tx.QueryRow(ctx, `SELECT `+viewColumns+` FROM delivery_system_rollouts WHERE id=$1 FOR UPDATE`, id))
}

func getByIdempotency(ctx context.Context, tx pgx.Tx, key string) (View, error) {
	return scanView(tx.QueryRow(ctx, `SELECT `+viewColumns+` FROM delivery_system_rollouts WHERE idempotency_key=$1`, key))
}

func timePointer(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	timestamp := value.Time.UTC()
	return &timestamp
}
