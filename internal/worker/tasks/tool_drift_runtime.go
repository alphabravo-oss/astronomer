package tasks

import (
	"fmt"

	"github.com/hibiken/asynq"
)

// ToolDriftRuntime owns the dependencies for the tunnel-bound Helm drift
// sweep. It is constructed before the tunnel worker subscribes to Redis.
type ToolDriftRuntime struct {
	Deps ToolDriftSweepDeps
}

func (runtime ToolDriftRuntime) Validate() error {
	return validateRequiredRuntimeDependencies("tool drift", []requiredRuntimeDependency{
		{name: "queries", value: runtime.Deps.Queries},
		{name: "helm", value: runtime.Deps.Helm},
	})
}

func (runtime ToolDriftRuntime) HandlerBindings() (map[string]asynq.HandlerFunc, error) {
	if err := runtime.Validate(); err != nil {
		return nil, fmt.Errorf("validate tool drift runtime: %w", err)
	}
	return map[string]asynq.HandlerFunc{ToolDriftSweepType: runtime.HandleToolDriftSweep}, nil
}
