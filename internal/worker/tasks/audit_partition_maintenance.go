package tasks

import (
	"context"
	"fmt"

	"github.com/hibiken/asynq"
)

type auditPartitionQuerier interface {
	EnsureAuditLogPartitions(ctx context.Context) error
}

// EnsureAuditLogPartitionsType is the periodic task identifier for keeping the
// current and next month audit_log partitions materialized.
const EnsureAuditLogPartitionsType = "audit_log:ensure_partitions"

func NewEnsureAuditLogPartitionsTask() *asynq.Task {
	return asynq.NewTask(EnsureAuditLogPartitionsType, nil, asynq.MaxRetry(2))
}

// HandleEnsureAuditLogPartitions keeps the partitioned audit_log table ready
// for current and next month inserts. The task is leader-gated so scaled
// workers don't race to create the same partition DDL.
func HandleEnsureAuditLogPartitions(ctx context.Context, _ *asynq.Task) error {
	return runPeriodicTaskWithLeader(ctx, EnsureAuditLogPartitionsType, func() error {
		if runtimeDeps.Queries == nil {
			runtimeLogger().DebugContext(ctx, "audit partition runtime not configured, skipping")
			return nil
		}
		q, ok := runtimeDeps.Queries.(auditPartitionQuerier)
		if !ok {
			return fmt.Errorf("audit partition maintenance not supported by runtime querier")
		}
		return ensureAuditLogPartitions(ctx, q)
	})
}

func ensureAuditLogPartitions(ctx context.Context, q auditPartitionQuerier) error {
	if err := q.EnsureAuditLogPartitions(ctx); err != nil {
		return fmt.Errorf("ensure audit_log partitions: %w", err)
	}
	runtimeLogger().InfoContext(ctx, "ensured audit_log partitions for current and next month")
	return nil
}
