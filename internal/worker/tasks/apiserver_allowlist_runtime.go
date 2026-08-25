package tasks

import (
	"fmt"

	"github.com/hibiken/asynq"
)

// ApiserverAllowlistRuntime owns the worker-side cloud allow-list reconcile
// graph and binds all three task handlers without package-global mutation.
type ApiserverAllowlistRuntime struct {
	Deps ApiserverAllowlistReconcileDeps
}

func (runtime ApiserverAllowlistRuntime) Validate() error {
	return validateRequiredRuntimeDependencies("API-server allow-list", []requiredRuntimeDependency{
		{name: "queries", value: runtime.Deps.Queries},
		{name: "registry", value: runtime.Deps.Registry},
		{name: "cluster_shaper", value: runtime.Deps.ClusterShaper},
		{name: "audit_writer", value: runtime.Deps.AuditWriter},
	})
}

func (runtime ApiserverAllowlistRuntime) HandlerBindings() (map[string]asynq.HandlerFunc, error) {
	if err := runtime.Validate(); err != nil {
		return nil, fmt.Errorf("validate API-server allow-list runtime: %w", err)
	}
	return map[string]asynq.HandlerFunc{
		ApiserverAllowlistReconcileType:        runtime.HandleApiserverAllowlistReconcile,
		ApiserverAllowlistReconcileAllType:     runtime.HandleApiserverAllowlistReconcileAll,
		ApiserverAllowlistCleanupSnapshotsType: runtime.HandleApiserverAllowlistCleanupSnapshots,
	}, nil
}
