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

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

const AdminQueueOperationType = "admin_queue:apply_operation"

type AdminQueueOperationPayload struct {
	OperationID string `json:"operation_id"`
}

func NewAdminQueueOperationTask(operationID uuid.UUID) (*asynq.Task, error) {
	if operationID == uuid.Nil {
		return nil, errors.New("admin queue operation ID is required")
	}
	payload, err := json.Marshal(AdminQueueOperationPayload{OperationID: operationID.String()})
	if err != nil {
		return nil, fmt.Errorf("marshal admin queue operation task: %w", err)
	}
	return asynq.NewTask(AdminQueueOperationType, payload, asynq.MaxRetry(8)), nil
}

type AdminQueueOperationQuerier interface {
	ClaimAdminQueueOperation(context.Context, sqlc.ClaimAdminQueueOperationParams) (sqlc.AdminQueueOperation, error)
	GetAdminQueueOperation(context.Context, uuid.UUID) (sqlc.AdminQueueOperation, error)
	MarkAdminQueueOperationEffectStarted(context.Context, uuid.UUID) (sqlc.AdminQueueOperation, error)
	MarkAdminQueueOperationSucceeded(context.Context, uuid.UUID) (sqlc.AdminQueueOperation, error)
	MarkAdminQueueOperationFailed(context.Context, sqlc.MarkAdminQueueOperationFailedParams) (sqlc.AdminQueueOperation, error)
	MarkAdminQueueOperationRetrying(context.Context, sqlc.MarkAdminQueueOperationRetryingParams) (sqlc.AdminQueueOperation, error)
}

type AdminQueueOperationInspector interface {
	GetTaskInfo(queue, taskID string) (*asynq.TaskInfo, error)
	RunTask(queue, taskID string) error
	DeleteTask(queue, taskID string) error
}

type AdminQueueOperationDeps struct {
	Queries   AdminQueueOperationQuerier
	Inspector AdminQueueOperationInspector
	Now       func() time.Time
}

func (runtime DispatchRuntime) HandleAdminQueueOperation(ctx context.Context, task *asynq.Task) error {
	if task == nil {
		return fmt.Errorf("admin queue operation task is required: %w", asynq.SkipRetry)
	}
	var payload AdminQueueOperationPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return fmt.Errorf("decode admin queue operation: %w: %w", err, asynq.SkipRetry)
	}
	operationID, err := uuid.Parse(payload.OperationID)
	if err != nil {
		return fmt.Errorf("invalid admin queue operation ID: %w", asynq.SkipRetry)
	}
	return applyAdminQueueOperation(ctx, runtime.AdminQueue, operationID)
}

func applyAdminQueueOperation(ctx context.Context, deps AdminQueueOperationDeps, operationID uuid.UUID) error {
	if deps.Queries == nil || deps.Inspector == nil {
		return errors.New("admin queue operation runtime is not configured")
	}
	now := time.Now().UTC()
	if deps.Now != nil {
		now = deps.Now().UTC()
	}
	row, err := deps.Queries.ClaimAdminQueueOperation(ctx, sqlc.ClaimAdminQueueOperationParams{
		ID:          operationID,
		Now:         pgtype.Timestamptz{Time: now, Valid: true},
		LockedUntil: pgtype.Timestamptz{Time: now.Add(2 * time.Minute), Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		row, err = deps.Queries.GetAdminQueueOperation(ctx, operationID)
		if err == nil && row.Status == "succeeded" {
			return nil
		}
		if err == nil && row.Status == "running" {
			return errors.New("admin queue operation lease is held")
		}
		if err == nil {
			return fmt.Errorf("admin queue operation is not recoverable from status %q: %w", row.Status, asynq.SkipRetry)
		}
	}
	if err != nil {
		return fmt.Errorf("claim admin queue operation: %w", err)
	}

	applyErr := applyAdminQueueExternalEffect(ctx, deps, row)
	if applyErr != nil {
		terminal := errors.Is(applyErr, asynq.SkipRetry) || errors.Is(applyErr, errAdminQueueMissing) || adminQueueRetriesExhausted(ctx)
		errorCode := adminQueueOperationErrorCode(applyErr)
		var markErr error
		if terminal {
			_, markErr = deps.Queries.MarkAdminQueueOperationFailed(ctx, sqlc.MarkAdminQueueOperationFailedParams{
				ID: row.ID, LastError: errorCode,
			})
		} else {
			_, markErr = deps.Queries.MarkAdminQueueOperationRetrying(ctx, sqlc.MarkAdminQueueOperationRetryingParams{
				ID: row.ID, LastError: errorCode,
			})
		}
		// Returned handler errors are persisted by Asynq and commonly emitted to
		// worker logs. Keep that second sink as secret-free as the PostgreSQL
		// receipt: inspector/Redis errors can contain credential-bearing URLs.
		safeErr := errors.New("admin queue operation failed: " + errorCode)
		if markErr != nil {
			return fmt.Errorf("%v; persist failure: %w", safeErr, markErr)
		}
		if terminal {
			return fmt.Errorf("%w: %v", asynq.SkipRetry, safeErr)
		}
		return safeErr
	}
	if _, err := deps.Queries.MarkAdminQueueOperationSucceeded(ctx, row.ID); err != nil {
		return fmt.Errorf("persist admin queue operation success: %w", err)
	}
	return nil
}

func adminQueueRetriesExhausted(ctx context.Context) bool {
	retried, retryOK := asynq.GetRetryCount(ctx)
	maximum, maximumOK := asynq.GetMaxRetry(ctx)
	return retryOK && maximumOK && retried >= maximum
}

var (
	errAdminQueueInspect = errors.New("admin queue target inspection failed")
	errAdminQueueEffect  = errors.New("admin queue external effect failed")
	errAdminQueueObserve = errors.New("admin queue external effect observation failed")
	errAdminQueueMissing = errors.New("admin queue retry target not found")
)

func applyAdminQueueExternalEffect(ctx context.Context, deps AdminQueueOperationDeps, row sqlc.AdminQueueOperation) error {
	info, err := deps.Inspector.GetTaskInfo(row.QueueName, row.TaskID)
	if errors.Is(err, asynq.ErrTaskNotFound) {
		if row.Action == "discard" || row.EffectStartedAt.Valid {
			return nil
		}
		return errAdminQueueMissing
	}
	if err != nil {
		return fmt.Errorf("%w: %v", errAdminQueueInspect, err)
	}
	// A prior worker may have completed the effect and crashed before saving
	// the PostgreSQL observation. Never delete a pending/active retry target.
	if info.State != asynq.TaskStateArchived {
		return nil
	}
	if !row.EffectStartedAt.Valid {
		row, err = deps.Queries.MarkAdminQueueOperationEffectStarted(ctx, row.ID)
		if err != nil {
			return fmt.Errorf("persist admin queue effect phase: %w", err)
		}
	}

	switch row.Action {
	case "retry":
		err = deps.Inspector.RunTask(row.QueueName, row.TaskID)
	case "discard":
		err = deps.Inspector.DeleteTask(row.QueueName, row.TaskID)
	default:
		return fmt.Errorf("unsupported admin queue action %q: %w", row.Action, asynq.SkipRetry)
	}
	if err != nil && !errors.Is(err, asynq.ErrTaskNotFound) {
		return fmt.Errorf("%w: %v", errAdminQueueEffect, err)
	}

	observed, observeErr := deps.Inspector.GetTaskInfo(row.QueueName, row.TaskID)
	if errors.Is(observeErr, asynq.ErrTaskNotFound) {
		return nil // discarded, or a retried task was consumed immediately.
	}
	if observeErr != nil {
		return fmt.Errorf("%w: %v", errAdminQueueObserve, observeErr)
	}
	if row.Action == "retry" && observed.State != asynq.TaskStateArchived {
		return nil
	}
	return errAdminQueueObserve
}

func adminQueueOperationErrorCode(err error) string {
	switch {
	case errors.Is(err, errAdminQueueMissing):
		return "target_not_found"
	case errors.Is(err, errAdminQueueInspect):
		return "inspect_failed"
	case errors.Is(err, errAdminQueueEffect):
		return "effect_failed"
	case errors.Is(err, errAdminQueueObserve):
		return "observe_failed"
	default:
		return "operation_failed"
	}
}
