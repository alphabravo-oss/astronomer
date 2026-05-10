package tasks

import (
	"context"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
)

// CleanupOldAlertEventsType is the periodic task identifier.
const CleanupOldAlertEventsType = "alert_events:cleanup"

// alertEventRetention is the default age beyond which alert events are deleted
// by the cleanup worker. Matches the Python “cleanup_old_alert_events“ task.
const alertEventRetention = 30 * 24 * time.Hour

// NewCleanupAlertEventsTask returns a task that deletes alert events older
// than alertEventRetention. The retry budget is intentionally low; missed
// runs are picked up by the next scheduled invocation.
func NewCleanupAlertEventsTask() *asynq.Task {
	return asynq.NewTask(CleanupOldAlertEventsType, nil, asynq.MaxRetry(2))
}

// HandleCleanupAlertEvents deletes alert events older than the retention
// window. Cron: daily at 02:00 UTC.
func HandleCleanupAlertEvents(ctx context.Context, _ *asynq.Task) error {
	return runPeriodicTaskWithLeader(ctx, CleanupOldAlertEventsType, func() error {
		if runtimeDeps.Queries == nil {
			runtimeLogger().InfoContext(ctx, "alert event cleanup runtime not configured, skipping")
			return nil
		}
		q, ok := runtimeDeps.Queries.(interface {
			DeleteAlertEventsOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
		})
		if !ok {
			return fmt.Errorf("alert event cleanup not supported by runtime querier")
		}
		cutoff := time.Now().Add(-alertEventRetention)
		rows, err := q.DeleteAlertEventsOlderThan(ctx, cutoff)
		if err != nil {
			return fmt.Errorf("delete alert events: %w", err)
		}
		runtimeLogger().InfoContext(ctx, "removed expired alert events", "rows", rows, "cutoff", cutoff.Format(time.RFC3339))
		return nil
	})
}
