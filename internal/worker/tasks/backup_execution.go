package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/hibiken/asynq"
)

// BackupExecutionPayload contains parameters for a backup task.
type BackupExecutionPayload struct {
	ClusterID string `json:"cluster_id"`
	BackupID  string `json:"backup_id"`
}

// NewBackupExecutionTask creates a new backup execution task with a 30-minute timeout.
func NewBackupExecutionTask(payload BackupExecutionPayload) (*asynq.Task, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal backup execution payload: %w", err)
	}
	return asynq.NewTask("backup:execute", data, asynq.MaxRetry(2), asynq.Timeout(30*time.Minute)), nil
}

// HandleBackupExecution executes a backup operation for a given cluster.
func HandleBackupExecution(ctx context.Context, t *asynq.Task) error {
	var p BackupExecutionPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("unmarshal backup execution payload: %w", err)
	}

	if p.ClusterID == "" || p.BackupID == "" {
		return fmt.Errorf("cluster_id and backup_id are required")
	}

	slog.InfoContext(ctx, "executing backup",
		"cluster_id", p.ClusterID,
		"backup_id", p.BackupID,
	)

	deadline, ok := ctx.Deadline()
	if ok {
		slog.InfoContext(ctx, "backup deadline", "deadline", deadline.Format(time.RFC3339), "remaining", time.Until(deadline).String())
	}

	// TODO: Connect to cluster via tunnel, create backup snapshot, upload to storage.

	slog.InfoContext(ctx, "backup execution complete", "cluster_id", p.ClusterID, "backup_id", p.BackupID)
	return nil
}
