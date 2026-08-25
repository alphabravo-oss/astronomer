package tasks

import (
	"fmt"

	"github.com/hibiken/asynq"
)

type ClusterDecommissionRuntime struct{ Deps ClusterDecommissionDeps }

func (runtime ClusterDecommissionRuntime) Validate() error {
	return validateRequiredRuntimeDependencies("cluster decommission", []requiredRuntimeDependency{
		{name: "queries", value: runtime.Deps.Queries},
		{name: "tunnel", value: runtime.Deps.Tunnel},
		{name: "rbac_cache", value: runtime.Deps.RBACCache},
	})
}

func (runtime ClusterDecommissionRuntime) HandlerBindings() (map[string]asynq.HandlerFunc, error) {
	if err := runtime.Validate(); err != nil {
		return nil, fmt.Errorf("validate cluster decommission runtime: %w", err)
	}
	return map[string]asynq.HandlerFunc{
		ClusterDecommissionType:    runtime.HandleClusterDecommission,
		ClusterDecommissionAllType: runtime.HandleClusterDecommissionAll,
	}, nil
}
