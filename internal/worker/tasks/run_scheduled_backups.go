package tasks

import (
	"context"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
)

// RunScheduledBackupsType is the periodic task identifier kept for
// backwards-compatibility with the scheduler registration.
const RunScheduledBackupsType = "backups:run_scheduled"

// NewRunScheduledBackupsTask returns a fresh task. Phase B2 reduced this to
// a no-op watchdog: Velero Schedule CRs handle cron-driven backup creation
// upstream, so the worker no longer fans out backup rows on cron.
func NewRunScheduledBackupsTask() *asynq.Task {
	return asynq.NewTask(RunScheduledBackupsType, nil, asynq.MaxRetry(2))
}

// HandleRunScheduledBackups is intentionally a watchdog-only compatibility
// consumer in the Velero engine.
// Velero's own controller in each cluster watches Schedule CRs and creates
// Backup CRs on cron — our server-side reconciler ingests those into our
// `backups` table. The registry retains this handler without a schedule so
// tasks left in Redis by an older release are drained cleanly after upgrade.
//
// A future iteration may use this hook for a watchdog that re-applies
// missing Velero Schedule CRs (e.g. after a cluster reconnect drops the
// resource); the data needed for that lives entirely server-side, however,
// so it is the BackupHandler reconciler's job, not ours.
func HandleRunScheduledBackups(ctx context.Context, _ *asynq.Task) error {
	return runPeriodicTaskWithLeader(ctx, RunScheduledBackupsType, func() error {
		if runtimeDependencies(ctx).Queries == nil {
			return fmt.Errorf("scheduled backups runtime is not configured")
		}
		// Touch the schedules table so an out-of-band sanity check in the test
		// suite can verify this handler still runs without error.
		schedules, err := runtimeDependencies(ctx).Queries.GetActiveSchedules(ctx)
		if err != nil {
			return fmt.Errorf("listing active backup schedules: %w", err)
		}
		if len(schedules) == 0 {
			return nil
		}
		runtimeLogger(ctx).DebugContext(ctx, "scheduled backups watchdog complete",
			"active_schedules", len(schedules),
			"sampled_at", time.Now().UTC().Format(time.RFC3339),
		)
		return nil
	})
}
