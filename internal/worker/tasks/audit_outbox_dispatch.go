package tasks

import (
	"context"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

const (
	AuditOutboxDispatchType      = "audit:outbox_dispatch"
	auditOutboxDispatchBatchSize = int32(100)
	auditOutboxLease             = 2 * time.Minute
)

type AuditOutboxQuerier interface {
	ResetExpiredAuditOutboxLeases(context.Context, time.Time) (int64, error)
	ClaimDueAuditOutbox(context.Context, sqlc.ClaimDueAuditOutboxParams) ([]sqlc.AuditOutbox, error)
	DeliverAuditOutbox(context.Context, sqlc.DeliverAuditOutboxParams) (sqlc.DeliverAuditOutboxRow, error)
	MarkAuditOutboxFailed(context.Context, sqlc.MarkAuditOutboxFailedParams) (sqlc.AuditOutbox, error)
}

type AuditOutboxDispatchDeps struct {
	Queries AuditOutboxQuerier
	Now     func() time.Time
}

func (runtime DispatchRuntime) HandleAuditOutboxDispatch(ctx context.Context, _ *asynq.Task) error {
	return runPeriodicTaskWithLeader(ctx, AuditOutboxDispatchType, func() error {
		return DispatchAuditOutboxOnce(ctx, runtime.AuditOutbox)
	})
}

func DispatchAuditOutboxOnce(ctx context.Context, deps AuditOutboxDispatchDeps) error {
	if deps.Queries == nil {
		return fmt.Errorf("audit outbox dispatch runtime is not configured")
	}
	nowFn := deps.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	now := nowFn().UTC()
	if _, err := deps.Queries.ResetExpiredAuditOutboxLeases(ctx, now); err != nil {
		return fmt.Errorf("reset expired audit outbox leases: %w", err)
	}
	rows, err := deps.Queries.ClaimDueAuditOutbox(ctx, sqlc.ClaimDueAuditOutboxParams{
		LockedUntil: pgtype.Timestamptz{Time: now.Add(auditOutboxLease), Valid: true},
		Now:         now,
		BatchLimit:  auditOutboxDispatchBatchSize,
	})
	if err != nil {
		return fmt.Errorf("claim due audit outbox rows: %w", err)
	}
	for _, row := range rows {
		if err := dispatchAuditOutboxRow(ctx, deps, row, nowFn); err != nil {
			runtimeLogger(ctx).WarnContext(ctx, "audit outbox row dispatch failed",
				"id", row.ID.String(), "action", row.Action, "attempt", row.AttemptCount,
				"error", err)
		}
	}
	return nil
}

func dispatchAuditOutboxRow(ctx context.Context, deps AuditOutboxDispatchDeps, row sqlc.AuditOutbox, nowFn func() time.Time) error {
	deliveredAt := nowFn().UTC()
	delivered, err := deps.Queries.DeliverAuditOutbox(ctx, sqlc.DeliverAuditOutboxParams{
		OutboxID: row.ID, DeliveredAt: pgtype.Timestamptz{Time: deliveredAt, Valid: true},
	})
	if err == nil {
		audit.PublishOutboxDelivery(delivered)
		return nil
	}

	now := nowFn().UTC()
	nextAttempt := now.Add(auditOutboxBackoff(row.AttemptCount))
	failed, markErr := deps.Queries.MarkAuditOutboxFailed(ctx, sqlc.MarkAuditOutboxFailedParams{
		OutboxID: row.ID, NextAttemptAt: nextAttempt, LastError: err.Error(), Now: now,
	})
	if markErr != nil {
		return fmt.Errorf("deliver audit intent: %v; mark failed: %w", err, markErr)
	}
	if failed.Status == "dead" {
		runtimeLogger(ctx).ErrorContext(ctx, "mandatory audit intent exhausted delivery attempts",
			"id", row.ID.String(), "action", row.Action, "attempts", failed.AttemptCount)
	}
	return err
}

func auditOutboxBackoff(attempt int32) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 8 {
		attempt = 8
	}
	return time.Duration(1<<attempt) * time.Second
}
