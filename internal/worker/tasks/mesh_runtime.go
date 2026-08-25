package tasks

import (
	"fmt"

	"github.com/hibiken/asynq"
)

type MeshRuntime struct{ Deps MeshDetectDeps }

func (runtime MeshRuntime) Validate() error {
	return validateRequiredRuntimeDependencies("service mesh", []requiredRuntimeDependency{
		{name: "queries", value: runtime.Deps.Queries},
		{name: "requester", value: runtime.Deps.Requester},
	})
}

func (runtime MeshRuntime) HandlerBindings() (map[string]asynq.HandlerFunc, error) {
	if err := runtime.Validate(); err != nil {
		return nil, fmt.Errorf("validate service mesh runtime: %w", err)
	}
	return map[string]asynq.HandlerFunc{MeshDetectType: runtime.HandleMeshDetect}, nil
}
