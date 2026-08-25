package tasks

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

const TaskOutboxDispatchType = "task_outbox:dispatch"

const taskOutboxDispatchBatchSize = 100

type TaskOutboxQuerier interface {
	ClaimDueTaskOutbox(ctx context.Context, arg sqlc.ClaimDueTaskOutboxParams) ([]sqlc.TaskOutbox, error)
	MarkTaskOutboxDelivered(ctx context.Context, arg sqlc.MarkTaskOutboxDeliveredParams) error
	MarkTaskOutboxFailed(ctx context.Context, arg sqlc.MarkTaskOutboxFailedParams) error
}

type TaskOutboxEnqueuer interface {
	EnqueueContext(ctx context.Context, task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error)
}

type TaskOutboxDispatchDeps struct {
	Queries  TaskOutboxQuerier
	Enqueuer TaskOutboxEnqueuer
	Now      func() time.Time
}

func (runtime DispatchRuntime) HandleTaskOutboxDispatch(ctx context.Context, _ *asynq.Task) error {
	return runPeriodicTaskWithLeader(ctx, TaskOutboxDispatchType, func() error {
		return DispatchTaskOutboxOnce(ctx, runtime.TaskOutbox)
	})
}

func DispatchTaskOutboxOnce(ctx context.Context, deps TaskOutboxDispatchDeps) error {
	if deps.Queries == nil || deps.Enqueuer == nil {
		return fmt.Errorf("task outbox dispatch runtime is not configured")
	}
	nowFn := deps.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	now := nowFn().UTC()
	rows, err := deps.Queries.ClaimDueTaskOutbox(ctx, sqlc.ClaimDueTaskOutboxParams{
		Now:         pgtype.Timestamptz{Time: now, Valid: true},
		LockedUntil: pgtype.Timestamptz{Time: now.Add(2 * time.Minute), Valid: true},
		Limit:       taskOutboxDispatchBatchSize,
	})
	if err != nil {
		return fmt.Errorf("claim due task_outbox rows: %w", err)
	}
	for _, row := range rows {
		if err := dispatchTaskOutboxRow(ctx, deps, row, nowFn); err != nil {
			runtimeLogger(ctx).WarnContext(ctx, "task outbox row dispatch failed",
				"id", row.ID.String(),
				"task_type", row.TaskType,
				"error", err,
			)
		}
	}
	return nil
}

func dispatchTaskOutboxRow(ctx context.Context, deps TaskOutboxDispatchDeps, row sqlc.TaskOutbox, nowFn func() time.Time) error {
	task := asynq.NewTask(row.TaskType, row.Payload)
	opts := taskOutboxOptions(row)
	_, err := deps.Enqueuer.EnqueueContext(ctx, task, opts...)
	if err == nil || errors.Is(err, asynq.ErrTaskIDConflict) || errors.Is(err, asynq.ErrDuplicateTask) {
		return deps.Queries.MarkTaskOutboxDelivered(ctx, sqlc.MarkTaskOutboxDeliveredParams{
			ID:          row.ID,
			DeliveredAt: pgtype.Timestamptz{Time: nowFn().UTC(), Valid: true},
		})
	}

	nextStatus := "failed"
	if row.AttemptCount >= row.MaxDeliveryAttempts {
		nextStatus = "dead"
	}
	nextAttempt := nowFn().UTC().Add(taskOutboxBackoff(row.AttemptCount))
	if nextStatus == "dead" {
		nextAttempt = nowFn().UTC().Add(24 * time.Hour)
	}
	markErr := deps.Queries.MarkTaskOutboxFailed(ctx, sqlc.MarkTaskOutboxFailedParams{
		ID:            row.ID,
		Status:        nextStatus,
		NextAttemptAt: pgtype.Timestamptz{Time: nextAttempt, Valid: true},
		LastError:     err.Error(),
	})
	if markErr != nil {
		return fmt.Errorf("enqueue failed: %v; mark failed: %w", err, markErr)
	}
	return err
}

func taskOutboxOptions(row sqlc.TaskOutbox) []asynq.Option {
	opts := []asynq.Option{
		asynq.Queue(row.QueueName),
		asynq.MaxRetry(int(row.MaxRetry)),
		asynq.TaskID(row.ID.String()),
	}
	if row.TimeoutSeconds > 0 {
		opts = append(opts, asynq.Timeout(time.Duration(row.TimeoutSeconds)*time.Second))
	}
	if row.UniqueSeconds > 0 {
		opts = append(opts, asynq.Unique(time.Duration(row.UniqueSeconds)*time.Second))
	}
	return opts
}

func taskOutboxBackoff(attempt int32) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 8 {
		attempt = 8
	}
	return time.Duration(1<<attempt) * time.Second
}
