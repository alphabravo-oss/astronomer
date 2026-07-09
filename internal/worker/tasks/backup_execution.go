package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// BackupExecutionPayload addresses one backup row by its database id. The
// optional cluster_id is informational; the real cluster scope lives on the
// row itself (or its storage config) and is resolved server-side.
type BackupExecutionPayload struct {
	ClusterID string `json:"cluster_id"`
	BackupID  string `json:"backup_id"`
}

// NewBackupExecutionTask creates a backup execution task. Phase B2 made this
// a thin shim: the actual Velero CR round-trip happens server-side in
// BackupHandler.StartReconciler (which has the tunnel hub the worker lacks).
// The task here exists so callers can still enqueue work synchronously and
// so that retries / backoffs survive a server restart.
func NewBackupExecutionTask(payload BackupExecutionPayload) (*asynq.Task, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal backup execution payload: %w", err)
	}
	return asynq.NewTask("backup:execute", data, asynq.MaxRetry(2), asynq.Timeout(5*time.Minute)), nil
}

// HandleBackupExecution drives the worker-side portion of a Velero-managed
// backup run. We do exactly two things and trust the server-side reconciler
// for everything else:
//
//  1. Validate the backup row exists and is not already terminal.
//  2. Stamp it as `running` so the UI reflects intent immediately, and so
//     the server-side reconciler picks it up on its next poll tick.
//
// We deliberately do NOT apply the Velero Backup CR here — that requires a
// tunnel connection to the cluster which only the server process owns. If
// the row was created via POST /api/v1/backups/, the server already issued
// a best-effort CR apply at row-creation time; the reconciler then converges
// status from Velero into our row.
func HandleBackupExecution(ctx context.Context, t *asynq.Task) error {
	var p BackupExecutionPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("unmarshal backup execution payload: %w", err)
	}
	if p.BackupID == "" {
		return fmt.Errorf("backup_id is required")
	}

	slog.InfoContext(ctx, "backup execution requested",
		"cluster_id", p.ClusterID,
		"backup_id", p.BackupID,
	)

	if runtimeDeps.Queries == nil {
		runtimeLogger().InfoContext(ctx, "backup runtime not configured, skipping DB-backed execution")
		return nil
	}

	backupID, err := uuid.Parse(p.BackupID)
	if err != nil {
		return fmt.Errorf("invalid backup_id: %w", err)
	}
	q := runtimeDeps.Queries
	backup, err := q.GetBackupByID(ctx, backupID)
	if err != nil {
		return fmt.Errorf("loading backup row: %w", err)
	}
	switch backup.Status {
	case "completed", "failed":
		runtimeLogger().InfoContext(ctx, "backup already terminal, nothing to do",
			"backup_id", backup.ID.String(), "status", backup.Status)
		return nil
	case "running":
		// Already running — do nothing. The server-side reconciler will
		// finish converging it.
		return nil
	}

	// Verify storage config is reachable. Failing here lets the row land in
	// `failed` immediately rather than waiting on the reconciler timeout.
	if _, err := q.GetBackupStorageConfigByID(ctx, backup.StorageID); err != nil {
		_ = q.UpdateBackupFailed(ctx, sqlc.UpdateBackupFailedParams{
			ID:           backup.ID,
			ErrorMessage: fmt.Sprintf("loading storage config: %v", err),
		})
		return err
	}

	n, err := q.UpdateBackupStarted(ctx, backup.ID)
	if err != nil {
		return fmt.Errorf("marking backup started: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("backup %s not claimable (already running or terminal)", backup.ID)
	}

	runtimeLogger().InfoContext(ctx, "backup row marked running; server-side reconciler will drive Velero CR",
		"backup_id", backup.ID.String(),
		"velero_backup_name", backup.VeleroBackupName,
	)
	return nil
}
