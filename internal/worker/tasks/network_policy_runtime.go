package tasks

import (
	"fmt"

	"github.com/hibiken/asynq"
)

type NetworkPolicyRuntime struct{ Deps NetworkPolicyApplyDeps }

func (runtime NetworkPolicyRuntime) Validate() error {
	return validateRequiredRuntimeDependencies("network policy", []requiredRuntimeDependency{
		{name: "queries", value: runtime.Deps.Queries},
		{name: "requester", value: runtime.Deps.Requester},
	})
}

func (runtime NetworkPolicyRuntime) HandlerBindings() (map[string]asynq.HandlerFunc, error) {
	if err := runtime.Validate(); err != nil {
		return nil, fmt.Errorf("validate network policy runtime: %w", err)
	}
	return map[string]asynq.HandlerFunc{
		NetworkPolicyApplyType:      runtime.HandleNetworkPolicyApply,
		NetworkPolicyDriftCheckType: runtime.HandleNetworkPolicyDriftCheck,
	}, nil
}
