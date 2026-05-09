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

// HandleEnforceBackupRetention is a no-op under the Velero engine. Velero
// honours the `spec.ttl` we attach to every Schedule (and to one-off Backup
// CRs); on expiry it deletes both the in-cluster Backup object and the
// underlying object-storage bytes. Our DB rows are pruned by the server-side
// reconciler when it observes the upstream CR has been removed.
//
// We keep the handler registered so the existing scheduler entry continues
// to enqueue without error and so a future iteration can extend it (for
// example, to vacuum failed backup rows older than N days).
func HandleEnforceBackupRetention(ctx context.Context, _ *asynq.Task) error {
	if runtimeDeps.Queries == nil {
		runtimeLogger().DebugContext(ctx, "backup retention runtime not configured, skipping")
		return nil
	}
	runtimeLogger().DebugContext(ctx, "backup retention is delegated to Velero TTL; nothing to do")
	return nil
}
