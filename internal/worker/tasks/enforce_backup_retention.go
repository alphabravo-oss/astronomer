package tasks

import (
	"context"

	"github.com/hibiken/asynq"
)

// EnforceBackupRetentionType is the periodic task identifier.
const EnforceBackupRetentionType = "backups:enforce_retention"

// NewEnforceBackupRetentionTask returns a fresh task handle.
func NewEnforceBackupRetentionTask() *asynq.Task {
	return asynq.NewTask(EnforceBackupRetentionType, nil, asynq.MaxRetry(2))
}

// HandleEnforceBackupRetention is a no-op in the worker process. Retention is
// enforced elsewhere: TIME-based expiry is delegated to Velero via the
// `spec.ttl` attached to every Schedule/Backup CR, and COUNT-based retention
// ("keep N backups") is enforced by the server-side BackupHandler reconciler
// (enforceScheduleRetention), which has the tunnel K8s requester needed to
// issue Velero DeleteBackupRequest CRs. The standalone worker has no tunnel,
// so it can't do that work — hence the no-op here.
//
// We keep the handler registered so the existing scheduler entry continues to
// enqueue without error.
func HandleEnforceBackupRetention(ctx context.Context, _ *asynq.Task) error {
	return runPeriodicTaskWithLeader(ctx, EnforceBackupRetentionType, func() error {
		if runtimeDeps.Queries == nil {
			runtimeLogger().DebugContext(ctx, "backup retention runtime not configured, skipping")
			return nil
		}
		runtimeLogger().DebugContext(ctx, "backup retention is delegated to Velero TTL; nothing to do")
		return nil
	})
}
