package rollout

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
)

type Action string

const (
	ActionPause    Action = "pause"
	ActionResume   Action = "resume"
	ActionAbort    Action = "abort"
	ActionRetry    Action = "retry"
	ActionRollback Action = "rollback"
)

type ActionRequest struct {
	ProjectID     uuid.UUID
	RolloutID     uuid.UUID
	ExpectedFence int64
	Action        Action
	ActorID       pgtype.UUID
	ReasonCode    string
}

type ApprovalRequest struct {
	ProjectID     uuid.UUID
	RolloutID     uuid.UUID
	ExpectedFence int64
	Cohort        int32
	BindingDigest model.Digest
	Decision      string
	ActorID       pgtype.UUID
	ExpiresAt     time.Time
}

type ControlResult struct {
	Rollout  sqlc.DeliveryRollout
	Event    sqlc.DeliveryRolloutEvent
	Approval *sqlc.DeliveryRolloutApproval
}

type Controller interface {
	Act(context.Context, ActionRequest) (ControlResult, error)
	Approve(context.Context, ApprovalRequest) (ControlResult, error)
}

type PostgresController struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

func NewPostgresController(pool *pgxpool.Pool, now func() time.Time) (*PostgresController, error) {
	if pool == nil {
		return nil, fail(CodeInvalidInput, "pool", "is required")
	}
	if now == nil {
		now = time.Now
	}
	return &PostgresController{pool: pool, now: now}, nil
}

func (c *PostgresController) Act(ctx context.Context, request ActionRequest) (ControlResult, error) {
	if request.ProjectID == uuid.Nil || request.RolloutID == uuid.Nil || request.ExpectedFence < 1 {
		return ControlResult{}, fail(CodeInvalidInput, "action", "project, rollout, and positive If-Match fence are required")
	}
	if len(request.ReasonCode) > 96 || strings.ContainsAny(request.ReasonCode, "\r\n\x00") {
		return ControlResult{}, fail(CodeInvalidInput, "reason_code", "must be at most 96 safe bytes")
	}
	allowed, next, err := actionTransition(request.Action)
	if err != nil {
		return ControlResult{}, err
	}
	tx, err := c.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return ControlResult{}, fmt.Errorf("begin rollout action: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := sqlc.New(tx)
	current, err := queries.GetDeliveryRolloutForAction(ctx, sqlc.GetDeliveryRolloutForActionParams{ID: request.RolloutID, ProjectID: request.ProjectID})
	if err != nil {
		return ControlResult{}, err
	}
	if current.FencingGeneration != request.ExpectedFence {
		return ControlResult{}, fail(CodeStaleFence, "if_match", "rollout fencing generation changed")
	}
	if request.Action == ActionRollback {
		plan, decodeErr := decodeFrozenPlan(current)
		if decodeErr != nil {
			return ControlResult{}, decodeErr
		}
		for _, cluster := range plan.Clusters {
			if cluster.Previous == nil {
				return ControlResult{}, fail(CodeInvalidInput, "rollback", "not every rollout cluster has a frozen previous known-good version")
			}
		}
	}
	if request.Action == ActionRetry {
		reset, resetErr := queries.ResetRetryableDeliveryRolloutClusters(ctx, request.RolloutID)
		if resetErr != nil {
			return ControlResult{}, resetErr
		}
		if reset == 0 {
			return ControlResult{}, fail(CodeInvalidInput, "retry", "rollout has no failed, timed-out, or blocked clusters")
		}
	}
	reason := strings.TrimSpace(request.ReasonCode)
	if reason == "" {
		reason = "user_" + string(request.Action)
	}
	updated, err := queries.TransitionDeliveryRolloutCAS(ctx, sqlc.TransitionDeliveryRolloutCASParams{
		ToState: next, ReasonCode: reason, ID: request.RolloutID,
		ExpectedFence: request.ExpectedFence, AllowedStates: allowed,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ControlResult{}, fail(CodeInvalidInput, "state", "action is not valid for the current rollout state")
		}
		return ControlResult{}, err
	}
	digest, err := actionDigest(request.RolloutID, request.ExpectedFence, request.Action, request.ActorID, reason)
	if err != nil {
		return ControlResult{}, err
	}
	event, err := queries.CreateDeliveryRolloutEvent(ctx, sqlc.CreateDeliveryRolloutEventParams{
		RolloutID: request.RolloutID, DecisionDigest: digest.String(), EventType: "rollout_" + string(request.Action),
		FromState: current.State, ToState: updated.State, ReasonCode: reason,
		Fence: updated.FencingGeneration, OccurredAt: c.now().UTC(),
	})
	if err != nil {
		return ControlResult{}, err
	}
	if err := enqueueControl(ctx, queries, request.RolloutID, updated.FencingGeneration, string(request.Action), c.now().UTC()); err != nil {
		return ControlResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ControlResult{}, fmt.Errorf("commit rollout action: %w", err)
	}
	return ControlResult{Rollout: updated, Event: event}, nil
}

func (c *PostgresController) Approve(ctx context.Context, request ApprovalRequest) (ControlResult, error) {
	if request.ProjectID == uuid.Nil || request.RolloutID == uuid.Nil || request.ExpectedFence < 1 || request.Cohort < -1 {
		return ControlResult{}, fail(CodeInvalidInput, "approval", "project, rollout, positive fence, and cohort >= -1 are required")
	}
	if err := request.BindingDigest.Validate(); err != nil {
		return ControlResult{}, &Error{Code: CodeInvalidInput, Field: "binding_digest", Cause: err}
	}
	if request.Decision != "approved" && request.Decision != "rejected" {
		return ControlResult{}, fail(CodeInvalidInput, "decision", "must be approved or rejected")
	}
	now := c.now().UTC()
	if !request.ExpiresAt.After(now) || request.ExpiresAt.After(now.Add(24*time.Hour)) {
		return ControlResult{}, fail(CodeInvalidInput, "expires_at", "must be after now and no more than 24 hours in the future")
	}
	tx, err := c.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return ControlResult{}, fmt.Errorf("begin rollout approval: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := sqlc.New(tx)
	current, err := queries.GetDeliveryRolloutForAction(ctx, sqlc.GetDeliveryRolloutForActionParams{ID: request.RolloutID, ProjectID: request.ProjectID})
	if err != nil {
		return ControlResult{}, err
	}
	if current.FencingGeneration != request.ExpectedFence {
		return ControlResult{}, fail(CodeStaleFence, "if_match", "rollout fencing generation changed")
	}
	plan, err := decodeFrozenPlan(current)
	if err != nil {
		return ControlResult{}, err
	}
	expectedDigest := plan.Approval.Digest
	if request.Cohort >= 0 {
		if int(request.Cohort) >= len(plan.Cohorts) || plan.Cohorts[request.Cohort].Index != int(request.Cohort) || !plan.Cohorts[request.Cohort].ApprovalRequired {
			return ControlResult{}, fail(CodeInvalidInput, "cohort", "does not identify a frozen approval gate")
		}
		expectedDigest = plan.Cohorts[request.Cohort].ApprovalDigest
	} else if !plan.Approval.Required {
		return ControlResult{}, fail(CodeInvalidInput, "cohort", "rollout has no initial approval gate")
	}
	if request.BindingDigest != expectedDigest {
		return ControlResult{}, fail(CodeInvalidInput, "binding_digest", "does not match the frozen approval gate")
	}
	approval, err := queries.CreateDeliveryRolloutApproval(ctx, sqlc.CreateDeliveryRolloutApprovalParams{
		RolloutID: request.RolloutID, Cohort: request.Cohort, BindingDigest: request.BindingDigest.String(),
		Decision: request.Decision, DecidedBy: request.ActorID, ExpiresAt: request.ExpiresAt.UTC(),
	})
	if err != nil {
		return ControlResult{}, err
	}
	updated := current
	if request.Cohort == -1 {
		to := string(model.RolloutQueued)
		if request.Decision == "rejected" {
			to = string(model.RolloutRejected)
		}
		updated, err = queries.TransitionDeliveryRolloutCAS(ctx, sqlc.TransitionDeliveryRolloutCASParams{
			ToState: to, ReasonCode: "approval_" + request.Decision,
			ID: request.RolloutID, ExpectedFence: request.ExpectedFence,
			AllowedStates: []string{string(model.RolloutAwaitingApproval)},
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ControlResult{}, fail(CodeInvalidInput, "state", "initial approval is only valid while awaiting approval")
			}
			return ControlResult{}, err
		}
	}
	event, err := queries.CreateDeliveryRolloutEvent(ctx, sqlc.CreateDeliveryRolloutEventParams{
		RolloutID: request.RolloutID, DecisionDigest: request.BindingDigest.String(), EventType: "rollout_" + request.Decision,
		FromState: current.State, ToState: updated.State, ReasonCode: fmt.Sprintf("cohort_%d", request.Cohort),
		Fence: updated.FencingGeneration, OccurredAt: now,
	})
	if err != nil {
		return ControlResult{}, err
	}
	if request.Decision == "approved" {
		if err := enqueueControl(ctx, queries, request.RolloutID, updated.FencingGeneration, "approve", now); err != nil {
			return ControlResult{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return ControlResult{}, fmt.Errorf("commit rollout approval: %w", err)
	}
	return ControlResult{Rollout: updated, Event: event, Approval: &approval}, nil
}

func actionTransition(action Action) ([]string, string, error) {
	switch action {
	case ActionPause:
		return []string{string(model.RolloutResolving), string(model.RolloutQueued), string(model.RolloutProgressing)}, string(model.RolloutPaused), nil
	case ActionResume:
		return []string{string(model.RolloutPaused)}, string(model.RolloutQueued), nil
	case ActionAbort:
		return []string{string(model.RolloutResolving), string(model.RolloutAwaitingApproval), string(model.RolloutQueued), string(model.RolloutProgressing), string(model.RolloutPaused), string(model.RolloutFailed)}, string(model.RolloutAborted), nil
	case ActionRetry:
		return []string{string(model.RolloutFailed), string(model.RolloutPaused)}, string(model.RolloutQueued), nil
	case ActionRollback:
		return []string{string(model.RolloutSucceeded), string(model.RolloutFailed), string(model.RolloutPaused)}, string(model.RolloutRollingBack), nil
	default:
		return nil, "", fail(CodeInvalidInput, "action", "is unsupported")
	}
}

func actionDigest(rolloutID uuid.UUID, fence int64, action Action, actor pgtype.UUID, reason string) (model.Digest, error) {
	actorID := ""
	if actor.Valid {
		actorID = uuid.UUID(actor.Bytes).String()
	}
	return model.CanonicalDigest(struct {
		RolloutID uuid.UUID `json:"rollout_id"`
		Fence     int64     `json:"fence"`
		Action    Action    `json:"action"`
		Actor     string    `json:"actor,omitempty"`
		Reason    string    `json:"reason_code"`
	}{rolloutID, fence, action, actorID, reason})
}

func enqueueControl(ctx context.Context, queries *sqlc.Queries, rolloutID uuid.UUID, fence int64, action string, now time.Time) error {
	payload, _ := json.Marshal(struct {
		RolloutID uuid.UUID `json:"rollout_id"`
	}{rolloutID})
	_, err := queries.UpsertTaskOutbox(ctx, sqlc.UpsertTaskOutboxParams{
		DedupeKey: pgtype.Text{String: fmt.Sprintf("delivery-rollout-control:%s:%d:%s", rolloutID, fence, action), Valid: true},
		TaskType:  TaskType, Payload: payload, QueueName: "default", MaxRetry: defaultTaskMaxRetries,
		TimeoutSeconds: int32(defaultTaskTimeout / time.Second), UniqueSeconds: 1,
		MaxDeliveryAttempts: 20, NextAttemptAt: timestamptz(now),
	})
	return err
}
