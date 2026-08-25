package tasks

import (
	"log/slog"

	"github.com/hibiken/asynq"
)

type ControlPlaneSnapshotRuntime struct {
	Deps         ControlPlaneSnapshotSweepDeps
	Applier      ControlPlaneSnapshotApplier
	StatusReader ControlPlaneSnapshotStatusReader
}

func (runtime ControlPlaneSnapshotRuntime) normalized() ControlPlaneSnapshotRuntime {
	if runtime.Deps.Log == nil {
		runtime.Deps.Log = slog.Default()
	}
	return runtime
}

func (runtime ControlPlaneSnapshotRuntime) Validate() error {
	runtime = runtime.normalized()
	return validateRequiredRuntimeDependencies("control-plane snapshot", []requiredRuntimeDependency{
		{name: "queries", value: runtime.Deps.Queries},
		{name: "applier", value: runtime.Applier},
		{name: "status_reader", value: runtime.StatusReader},
		{name: "logger", value: runtime.Deps.Log},
	})
}

// HandlerBindings binds this optional task even while disabled. Feature-aware
// startup validation requires its dependencies only when the feature is on.
func (runtime ControlPlaneSnapshotRuntime) HandlerBindings() (map[string]asynq.HandlerFunc, error) {
	runtime = runtime.normalized()
	return map[string]asynq.HandlerFunc{
		ControlPlaneSnapshotApplyType: runtime.HandleControlPlaneSnapshotApply,
		ControlPlaneSnapshotSweepType: runtime.HandleControlPlaneSnapshotSweep,
	}, nil
}
