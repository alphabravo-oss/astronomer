package tasks

import (
	"fmt"

	"github.com/hibiken/asynq"
)

type CloudCredentialRuntime struct {
	Deps CloudCredentialMaterializeDeps
}

func (runtime CloudCredentialRuntime) Validate() error {
	return validateRequiredRuntimeDependencies("cloud credential", []requiredRuntimeDependency{
		{name: "queries", value: runtime.Deps.Queries},
		{name: "requester", value: runtime.Deps.Requester},
		{name: "decryptor", value: runtime.Deps.Decryptor},
	})
}

func (runtime CloudCredentialRuntime) HandlerBindings() (map[string]asynq.HandlerFunc, error) {
	if err := runtime.Validate(); err != nil {
		return nil, fmt.Errorf("validate cloud credential runtime: %w", err)
	}
	return map[string]asynq.HandlerFunc{
		CloudCredentialMaterializeType:    runtime.HandleCloudCredentialMaterialize,
		CloudCredentialDriftReconcileType: runtime.HandleCloudCredentialDriftReconcile,
	}, nil
}
