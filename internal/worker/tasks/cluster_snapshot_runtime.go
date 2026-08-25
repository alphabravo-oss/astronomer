package tasks

import (
	"fmt"
	"log/slog"

	"github.com/hibiken/asynq"
)

type ClusterSnapshotRuntime struct{ Deps ClusterSnapshotDeps }

func (runtime ClusterSnapshotRuntime) normalized() ClusterSnapshotRuntime {
	if runtime.Deps.Log == nil {
		runtime.Deps.Log = slog.Default()
	}
	return runtime
}

func (runtime ClusterSnapshotRuntime) Validate() error {
	runtime = runtime.normalized()
	return validateRequiredRuntimeDependencies("cluster snapshot", []requiredRuntimeDependency{
		{name: "queries", value: runtime.Deps.Queries},
		{name: "driver", value: runtime.Deps.Driver},
		{name: "logger", value: runtime.Deps.Log},
	})
}

func (runtime ClusterSnapshotRuntime) HandlerBindings() (map[string]asynq.HandlerFunc, error) {
	runtime = runtime.normalized()
	if err := runtime.Validate(); err != nil {
		return nil, fmt.Errorf("validate cluster snapshot runtime: %w", err)
	}
	return map[string]asynq.HandlerFunc{
		ClusterSnapshotApplyOperationType:    runtime.HandleClusterSnapshotApplyOperation,
		ClusterSnapshotPollType:              runtime.HandleClusterSnapshotPoll,
		ClusterSnapshotDispatchScheduledType: runtime.HandleClusterSnapshotDispatchScheduled,
		ClusterSnapshotCleanupExpiredType:    runtime.HandleClusterSnapshotCleanupExpired,
	}, nil
}
