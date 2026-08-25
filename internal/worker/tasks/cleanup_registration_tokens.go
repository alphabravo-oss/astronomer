package tasks

import (
	"context"
	"fmt"

	"github.com/hibiken/asynq"
)

// CleanupExpiredRegistrationTokensType is the periodic task identifier.
const CleanupExpiredRegistrationTokensType = "cluster_registration_tokens:cleanup"

// NewCleanupRegistrationTokensTask returns a fresh task suitable for both
// the scheduler and ad-hoc enqueues. Tokens with expired or used+stale rows
// are deleted by the underlying SQL query.
func NewCleanupRegistrationTokensTask() *asynq.Task {
	return asynq.NewTask(CleanupExpiredRegistrationTokensType, nil, asynq.MaxRetry(2))
}

// HandleCleanupRegistrationTokens removes expired cluster registration tokens.
// Cron: every 6h (matches Python “cleanup_expired_registration_tokens“).
func HandleCleanupRegistrationTokens(ctx context.Context, _ *asynq.Task) error {
	return runPeriodicTaskWithLeader(ctx, CleanupExpiredRegistrationTokensType, func() error {
		if runtimeDependencies(ctx).Queries == nil {
			return fmt.Errorf("registration token cleanup runtime is not configured")
		}
		q, ok := runtimeDependencies(ctx).Queries.(interface {
			DeleteExpiredRegistrationTokens(ctx context.Context) (int64, error)
		})
		if !ok {
			return fmt.Errorf("registration token cleanup not supported by runtime querier")
		}
		rows, err := q.DeleteExpiredRegistrationTokens(ctx)
		if err != nil {
			return fmt.Errorf("delete expired registration tokens: %w", err)
		}
		runtimeLogger(ctx).InfoContext(ctx, "removed expired registration tokens", "rows", rows)
		return nil
	})
}
