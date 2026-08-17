package deployment

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type Action string

const (
	ActionReconcile Action = "reconcile"
	ActionSuspend   Action = "suspend"
	ActionResume    Action = "resume"
)

type Request struct {
	ProjectID          uuid.UUID
	DeploymentID       uuid.UUID
	ExpectedGeneration int64
	Action             Action
	ReasonCode         string
}

type Result struct {
	Deployment sqlc.ClusterDeployment
	Event      sqlc.ClusterDeploymentEvent
}

type Controller interface {
	Act(context.Context, Request) (Result, error)
}

type PostgresController struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

func NewPostgresController(pool *pgxpool.Pool, now func() time.Time) (*PostgresController, error) {
	if pool == nil {
		return nil, errors.New("deployment controller pool is required")
	}
	if now == nil {
		now = time.Now
	}
	return &PostgresController{pool: pool, now: now}, nil
}

func (c *PostgresController) Act(ctx context.Context, request Request) (Result, error) {
	if request.ProjectID == uuid.Nil || request.DeploymentID == uuid.Nil || request.ExpectedGeneration < 0 {
		return Result{}, errors.New("project, deployment, and non-negative If-Match generation are required")
	}
	action, eventType, ok := resolveAction(request.Action)
	if !ok {
		return Result{}, errors.New("unsupported deployment action")
	}
	tx, err := c.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Result{}, fmt.Errorf("begin deployment action: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := sqlc.New(tx)
	current, err := queries.GetClusterDeploymentForAction(ctx, sqlc.GetClusterDeploymentForActionParams{ID: request.DeploymentID, ProjectID: request.ProjectID})
	if err != nil {
		return Result{}, err
	}
	if current.DesiredGeneration != request.ExpectedGeneration {
		return Result{}, ErrStaleGeneration
	}
	updated, err := queries.TransitionClusterDeploymentCAS(ctx, sqlc.TransitionClusterDeploymentCASParams{
		Action: action, ID: request.DeploymentID, ExpectedGeneration: request.ExpectedGeneration,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Result{}, ErrStaleGeneration
		}
		return Result{}, err
	}
	reason := request.ReasonCode
	if reason == "" {
		reason = string(request.Action) + "_requested"
	}
	event, err := queries.CreateClusterDeploymentEvent(ctx, sqlc.CreateClusterDeploymentEventParams{
		DeploymentID: updated.ID, RolloutID: pgtype.UUID{}, EventType: eventType,
		FromPhase: current.Phase, ToPhase: updated.Phase, Generation: updated.DesiredGeneration,
		SpecDigest: updated.DesiredSpecDigest, ReasonCode: reason, Message: "", ObservedAt: c.now().UTC(),
	})
	if err != nil {
		return Result{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Result{}, fmt.Errorf("commit deployment action: %w", err)
	}
	return Result{Deployment: updated, Event: event}, nil
}

var ErrStaleGeneration = errors.New("deployment desired generation changed")

func resolveAction(action Action) (assignmentAction, eventType string, ok bool) {
	switch action {
	case ActionReconcile:
		return "apply", "deployment_reconcile_requested", true
	case ActionSuspend:
		return "suspend", "deployment_suspend_requested", true
	case ActionResume:
		return "apply", "deployment_resume_requested", true
	default:
		return "", "", false
	}
}
