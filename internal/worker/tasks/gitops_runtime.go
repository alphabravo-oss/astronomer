package tasks

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/hibiken/asynq"
)

// GitOpsRuntime owns source sync, preview, tombstone, and decommission
// delivery dependencies. The API and worker processes construct independent
// immutable values instead of sharing package-global state.
type GitOpsRuntime struct {
	Deps GitOpsDeps
}

func (runtime GitOpsRuntime) normalized() GitOpsRuntime {
	if runtime.Deps.Log == nil {
		runtime.Deps.Log = slog.Default()
	}
	if runtime.Deps.Now == nil {
		runtime.Deps.Now = time.Now
	}
	return runtime
}

func (runtime GitOpsRuntime) Validate() error {
	runtime = runtime.normalized()
	return validateRequiredRuntimeDependencies("GitOps", []requiredRuntimeDependency{
		{name: "queries", value: runtime.Deps.Queries},
		{name: "task_outbox", value: runtime.Deps.TaskOutbox},
		{name: "decryptor", value: runtime.Deps.Decryptor},
		{name: "logger", value: runtime.Deps.Log},
	})
}

func (runtime GitOpsRuntime) HandlerBindings() (map[string]asynq.HandlerFunc, error) {
	runtime = runtime.normalized()
	if err := runtime.Validate(); err != nil {
		return nil, fmt.Errorf("validate GitOps runtime: %w", err)
	}
	return map[string]asynq.HandlerFunc{GitOpsSyncType: runtime.HandleGitOpsSync}, nil
}
