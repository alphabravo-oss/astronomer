package tasks

import (
	"fmt"

	"github.com/hibiken/asynq"
)

type ClusterRegistryRuntime struct{ Deps ClusterRegistryApplyDeps }

func (runtime ClusterRegistryRuntime) Validate() error {
	return validateRequiredRuntimeDependencies("cluster registry", []requiredRuntimeDependency{
		{name: "queries", value: runtime.Deps.Queries},
		{name: "requester", value: runtime.Deps.Requester},
		{name: "encryptor", value: runtime.Deps.Encryptor},
	})
}

func (runtime ClusterRegistryRuntime) HandlerBindings() (map[string]asynq.HandlerFunc, error) {
	if err := runtime.Validate(); err != nil {
		return nil, fmt.Errorf("validate cluster registry runtime: %w", err)
	}
	return map[string]asynq.HandlerFunc{
		ClusterApplyRegistrySecretType:    runtime.HandleClusterApplyRegistrySecret,
		ClusterRegistryDriftReconcileType: runtime.HandleClusterRegistryDriftReconcile,
	}, nil
}
