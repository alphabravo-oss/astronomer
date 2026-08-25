// Migration 057 — deferred maintenance operations dispatcher.
//
// Every 60s this task drains rows from deferred_operations whose
// deferred_until has elapsed. For each row the dispatcher either:
//
//   - calls the registered Replayer for that operation_type, which
//     translates the operation_spec back into the original handler /
//     task pipeline (e.g. an enqueue of cluster_decommission), OR
//   - if no replayer is registered for the op_type, marks the row
//     as failed with a clear "no replayer wired" message so the
//     operator sees the bug without losing the queued request.
//
// Rows whose expires_at has elapsed before they could be dispatched are
// marked 'expired' with an audit row instead of being replayed — the
// operator's intent ("queue this and run it later") doesn't include
// "run it next week", so we drop them after the 24h default TTL.
//
// The dispatcher is leader-elected via runPeriodicTaskWithLeader so
// multiple worker pods don't race on the same row.

package tasks

import (
	"context"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// DispatchDeferredType is the asynq task type for the dispatcher.
const DispatchDeferredType = "maintenance:dispatch_deferred"

// dispatchBatchSize caps the rows pulled per tick. Sized above the
// realistic queue depth (operators don't usually defer more than a
// handful of ops per window) while keeping a slow replayer from
// starving the next tick.
const dispatchBatchSize = 50

// DeferredReplayer is the per-op-type "play this row back into the
// system" callback. Returns the dispatched-at timestamp on success;
// an error implies the row should be marked failed (and retried on
// the next tick, until expires_at).
type DeferredReplayer func(ctx context.Context, row sqlc.DeferredOperation) error

// DeferredDispatchQuerier is the database surface the dispatcher
// needs. *sqlc.Queries satisfies it.
type DeferredDispatchQuerier interface {
	ListPendingDeferredOperations(ctx context.Context, arg sqlc.ListPendingDeferredOperationsParams) ([]sqlc.DeferredOperation, error)
	MarkDeferredDispatched(ctx context.Context, arg sqlc.MarkDeferredDispatchedParams) error
	MarkDeferredExpired(ctx context.Context, arg sqlc.MarkDeferredExpiredParams) error
	MarkDeferredFailed(ctx context.Context, arg sqlc.MarkDeferredFailedParams) error
}

// DeferredDispatchDeps is the dependency bag wired by NewApp at boot.
// nil-safe: when Queries is nil the task short-circuits to a clean
// info-level log rather than 500ing.
type DeferredDispatchDeps struct {
	Queries   DeferredDispatchQuerier
	Replayers map[string]DeferredReplayer
}

// HandleDispatchDeferred is the periodic task that drains the
// dispatchable rows. Same wrapper as the other periodic tasks so the
// reconciler metrics + leader election apply.
func (runtime DeferredRuntime) HandleDispatchDeferred(ctx context.Context, _ *asynq.Task) error {
	return runPeriodicTaskWithLeader(ctx, DispatchDeferredType, func() error {
		deps := runtime.normalized().Deps

		if deps.Queries == nil {
			return fmt.Errorf("deferred dispatch runtime is not configured")
		}

		now := time.Now().UTC()
		rows, err := deps.Queries.ListPendingDeferredOperations(ctx, sqlc.ListPendingDeferredOperationsParams{
			Now:        pgtype.Timestamptz{Time: now, Valid: true},
			BatchLimit: dispatchBatchSize,
		})
		if err != nil {
			return fmt.Errorf("list pending deferred operations: %w", err)
		}
		if len(rows) == 0 {
			return nil
		}

		for _, row := range rows {
			if err := ctx.Err(); err != nil {
				return nil
			}

			// Expired? Mark + audit + skip.
			if row.ExpiresAt.Valid && row.ExpiresAt.Time.Before(now) {
				_ = deps.Queries.MarkDeferredExpired(ctx, sqlc.MarkDeferredExpiredParams{
					ID:        row.ID,
					LastError: fmt.Sprintf("expired at %s before window opened", row.ExpiresAt.Time.Format(time.RFC3339)),
				})
				runtimeLogger(ctx).WarnContext(ctx, "deferred operation expired",
					"id", row.ID.String(),
					"op_type", row.OperationType,
					"window_id", row.WindowID.String(),
				)
				continue
			}

			replayer := deps.Replayers[row.OperationType]
			if replayer == nil {
				// No replayer wired — record + leave the row pending so
				// a later deploy with the replayer can pick it up. We
				// still stamp last_error so the operator can see the
				// stuck reason from the admin endpoint.
				_ = deps.Queries.MarkDeferredFailed(ctx, sqlc.MarkDeferredFailedParams{
					ID:        row.ID,
					LastError: fmt.Sprintf("no replayer registered for op_type %q", row.OperationType),
				})
				continue
			}

			if err := replayer(ctx, row); err != nil {
				_ = deps.Queries.MarkDeferredFailed(ctx, sqlc.MarkDeferredFailedParams{
					ID:        row.ID,
					LastError: fmt.Sprintf("replay failed: %s", err.Error()),
				})
				continue
			}

			_ = deps.Queries.MarkDeferredDispatched(ctx, sqlc.MarkDeferredDispatchedParams{
				ID:           row.ID,
				DispatchedAt: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
			})
		}
		return nil
	})
}
