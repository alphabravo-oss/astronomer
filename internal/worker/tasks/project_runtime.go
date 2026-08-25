package tasks

import (
	"fmt"

	"github.com/hibiken/asynq"
)

type ProjectRuntime struct{ Deps ProjectReconcileDeps }

func (runtime ProjectRuntime) Validate() error {
	return validateRequiredRuntimeDependencies("project reconcile", []requiredRuntimeDependency{
		{name: "queries", value: runtime.Deps.Queries},
		{name: "requester", value: runtime.Deps.Requester},
		{name: "encryptor", value: runtime.Deps.Encryptor},
	})
}

func (runtime ProjectRuntime) HandlerBindings() (map[string]asynq.HandlerFunc, error) {
	if err := runtime.Validate(); err != nil {
		return nil, fmt.Errorf("validate project runtime: %w", err)
	}
	return map[string]asynq.HandlerFunc{
		ProjectReconcileType:    runtime.HandleProjectReconcile,
		ProjectReconcileAllType: runtime.HandleProjectReconcileAll,
	}, nil
}
