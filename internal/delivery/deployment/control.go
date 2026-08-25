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

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/asyncop"
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
	ActorID            pgtype.UUID
	IdempotencyKey     string
	ReasonCode         string
	Audit              audit.Intent
}

type Result struct {
	Deployment     sqlc.ClusterDeployment
	Event          sqlc.ClusterDeploymentEvent
	AuditPersisted bool
	Receipt        asyncop.Receipt
	Replayed       bool
}

type Controller interface {
	Act(context.Context, Request) (Result, error)
}

type PostgresController struct {
	pool         *pgxpool.Pool
	now          func() time.Time
	requireAudit bool
}

func (c *PostgresController) RequireTransactionalAudit() {
	if c != nil {
		c.requireAudit = true
	}
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
	if !request.ActorID.Valid || uuid.UUID(request.ActorID.Bytes) == uuid.Nil {
		return Result{}, errors.New("authenticated actor is required")
	}
	if err := asyncop.ValidateKey(request.IdempotencyKey); err != nil {
		return Result{}, err
	}
	reason := request.ReasonCode
	if reason == "" {
		reason = string(request.Action) + "_requested"
	}
	requestDigest, err := asyncop.Digest(struct {
		ProjectID    uuid.UUID `json:"project_id"`
		DeploymentID uuid.UUID `json:"deployment_id"`
		Generation   int64     `json:"generation"`
		Action       Action    `json:"action"`
		Reason       string    `json:"reason_code"`
	}{request.ProjectID, request.DeploymentID, request.ExpectedGeneration, request.Action, reason})
	if err != nil {
		return Result{}, err
	}
	tx, err := c.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Result{}, fmt.Errorf("begin deployment action: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := sqlc.New(tx)
	operation := "deployment." + string(request.Action)
	claim, err := asyncop.ClaimKey(ctx, queries, asyncop.ClaimRequest{
		ActorID: uuid.UUID(request.ActorID.Bytes), ProjectID: request.ProjectID, Operation: operation, Resource: "cluster_deployment", TargetID: request.DeploymentID,
		IdempotencyKey: request.IdempotencyKey, RequestDigest: requestDigest, OperationTable: "cluster_deployment_events",
	})
	if err != nil {
		return Result{}, err
	}
	if claim.Replay {
		return Result{Receipt: claim.Receipt, Replayed: true}, nil
	}
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
	event, err := queries.CreateClusterDeploymentEvent(ctx, sqlc.CreateClusterDeploymentEventParams{
		DeploymentID: updated.ID, RolloutID: pgtype.UUID{}, EventType: eventType,
		FromPhase: current.Phase, ToPhase: updated.Phase, Generation: updated.DesiredGeneration,
		SpecDigest: updated.DesiredSpecDigest, ReasonCode: reason, Message: "", ObservedAt: c.now().UTC(),
	})
	if err != nil {
		return Result{}, err
	}
	auditPersisted := false
	if request.Audit.IsZero() {
		if c.requireAudit {
			return Result{}, audit.ErrOutboxUnavailable
		}
	} else {
		intent := request.Audit
		if intent.Event.Detail == nil {
			intent.Event.Detail = map[string]any{}
		}
		intent.Event.Detail["cluster_id"] = updated.ClusterID.String()
		intent.Event.Detail["target_id"] = updated.TargetID.String()
		intent.Event.Detail["phase"] = updated.Phase
		intent.Event.Detail["desired_generation"] = updated.DesiredGeneration
		if err := audit.RecordIntent(ctx, queries, intent); err != nil {
			return Result{}, err
		}
		auditPersisted = true
	}
	receipt := asyncop.NewReceipt(event.ID, operation, "cluster_deployment", request.DeploymentID, request.ProjectID,
		fmt.Sprintf("/api/v1/delivery/deployments/%s/?project_id=%s", request.DeploymentID, request.ProjectID), c.now())
	if err := asyncop.Attach(ctx, queries, claim, receipt); err != nil {
		return Result{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Result{}, fmt.Errorf("commit deployment action: %w", err)
	}
	return Result{Deployment: updated, Event: event, AuditPersisted: auditPersisted, Receipt: receipt}, nil
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
