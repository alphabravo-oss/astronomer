package worker

import (
	"context"
	"errors"
	"log/slog"

	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
	"github.com/hibiken/asynq"
)

// TerminalFailurePublisher is intentionally payload-free. The queue adapter
// passes only Asynq-owned opaque identity and a closed failure class, never the
// task payload or returned error text.
type TerminalFailurePublisher interface {
	PublishQueueTerminalFailure(context.Context, string, string, string) error
}

func NewTerminalFailureErrorHandler(publisher TerminalFailurePublisher, log *slog.Logger) asynq.ErrorHandler {
	return asynq.ErrorHandlerFunc(func(ctx context.Context, task *asynq.Task, taskErr error) {
		if publisher == nil || task == nil {
			return
		}
		retried, retryOK := asynq.GetRetryCount(ctx)
		maximum, maximumOK := asynq.GetMaxRetry(ctx)
		taskID, ok := asynq.GetTaskID(ctx)
		if !retryOK || !maximumOK || !ok {
			return
		}
		if err := publishQueueTerminalFailure(ctx, publisher, task.Type(), taskID, retried, maximum, taskErr); err != nil && log != nil {
			log.Warn("queue terminal failure event was not persisted", "task_type", task.Type())
		}
	})
}

func publishQueueTerminalFailure(ctx context.Context, publisher TerminalFailurePublisher, taskType, taskID string, retried, maximum int, taskErr error) error {
	if publisher == nil || taskID == "" || errors.Is(taskErr, asynq.RevokeTask) || !queueTerminalFailureAllowed(taskType) {
		return nil
	}
	terminal, failureClass := retried >= maximum, "retry_exhausted"
	if errors.Is(taskErr, asynq.SkipRetry) {
		terminal, failureClass = true, "non_retryable"
	} else if asynq.IsPanicError(taskErr) && terminal {
		failureClass = "panic_retry_exhausted"
	}
	if !terminal {
		return nil
	}
	return publisher.PublishQueueTerminalFailure(ctx, taskType, taskID, failureClass)
}

// This closed catalog excludes Charlie dispatch/outbox/alert tasks so a
// Charlie outage can never recursively manufacture more Charlie work.
func queueTerminalFailureAllowed(taskType string) bool {
	_, allowed := map[string]struct{}{
		TypeHealthCheck: {}, TypeAlertEvaluation: {}, TypeCatalogSync: {}, TypeMetricsAggregation: {}, TypeMonitoringReconcile: {},
		TypeBackupExecution: {}, TypeSecurityScan: {}, TypeSecurityIngest: {}, TypeNotificationSend: {}, TypeAgentManifest: {},
		TypeRunScheduledBackups: {}, TypeRunRestore: {}, TypeProjectReconcile: {}, TypeClusterDecommission: {},
		TypeClusterTemplateApply: {}, TypeClusterApplyRegistrySecret: {}, TypeClusterSnapshotPoll: {}, TypeCloudCredentialMaterialize: {},
		TypeDispatchDeferred: {}, TypeToolDriftSweep: {},
		tasks.GitOpsSyncType: {}, tasks.NetworkPolicyApplyType: {}, tasks.ApiserverAllowlistReconcileType: {}, tasks.ClusterConditionReconcileType: {},
	}[taskType]
	return allowed
}
