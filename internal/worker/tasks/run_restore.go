package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// RunRestoreType is the on-demand task identifier.
const RunRestoreType = "backups:run_restore"

// RunRestorePayload addresses a single restore_operations row by id.
type RunRestorePayload struct {
	RestoreID string `json:"restore_id"`
}

// NewRunRestoreTask serializes a RunRestorePayload into an Asynq task. The
// task's role in Phase B2 is to flip the restore row from `pending` to
// `running`. The server-side BackupHandler reconciler is responsible for
// applying the Velero Restore CR and converging status — only the server
// process holds the tunnel hub needed to reach the agent.
func NewRunRestoreTask(payload RunRestorePayload) (*asynq.Task, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal restore payload: %w", err)
	}
	return asynq.NewTask(RunRestoreType, data, asynq.MaxRetry(1), asynq.Timeout(5*time.Minute)), nil
}

// HandleRunRestore validates and starts a restore_operations row. Per the
// notes on HandleBackupExecution this is intentionally thin — the actual
// Velero CR round-trip happens in the server's reconciler.
func HandleRunRestore(ctx context.Context, t *asynq.Task) error {
	var p RunRestorePayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("unmarshal restore payload: %w", err)
	}
	if p.RestoreID == "" {
		return fmt.Errorf("restore_id is required")
	}
	if runtimeDeps.Queries == nil {
		runtimeLogger().InfoContext(ctx, "restore runtime not configured, skipping")
		return nil
	}

	restoreID, err := uuid.Parse(p.RestoreID)
	if err != nil {
		return fmt.Errorf("invalid restore_id: %w", err)
	}

	q := runtimeDeps.Queries
	op, err := q.GetRestoreOperationByID(ctx, restoreID)
	if err != nil {
		return fmt.Errorf("loading restore operation: %w", err)
	}
	switch op.Status {
	case "completed", "failed":
		runtimeLogger().InfoContext(ctx, "restore already terminal, nothing to do",
			"restore_id", op.ID.String(), "status", op.Status)
		return nil
	case "running":
		return nil
	}

	backup, err := q.GetBackupByID(ctx, op.BackupID)
	if err != nil {
		_ = q.UpdateRestoreOperationFailed(ctx, sqlc.UpdateRestoreOperationFailedParams{
			ID:           op.ID,
			ErrorMessage: fmt.Sprintf("loading source backup: %v", err),
		})
		return err
	}
	if backup.Status != "completed" {
		msg := fmt.Sprintf("source backup %s is not completed (status=%s)", backup.ID, backup.Status)
		_ = q.UpdateRestoreOperationFailed(ctx, sqlc.UpdateRestoreOperationFailedParams{
			ID:           op.ID,
			ErrorMessage: msg,
		})
		return fmt.Errorf("%s", msg)
	}

	n, err := q.UpdateRestoreOperationStarted(ctx, op.ID)
	if err != nil {
		return fmt.Errorf("marking restore started: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("restore %s not claimable (already running or terminal)", op.ID)
	}

	runtimeLogger().InfoContext(ctx, "restore row marked running; server-side reconciler will drive Velero CR",
		"restore_id", op.ID.String(),
		"backup_id", backup.ID.String(),
		"velero_restore_name", op.VeleroRestoreName,
	)
	return nil
}
