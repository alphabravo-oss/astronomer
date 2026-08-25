package tasks

import (
	"fmt"

	"github.com/hibiken/asynq"
)

// DeliveryRuntime is the immutable dependency container for every standalone
// Flux delivery task. A worker receives one value during construction and
// binds its method values into that worker's mux; no process-global mutation is
// required, and two workers in one process cannot overwrite each other's
// delivery dependencies.
type DeliveryRuntime struct {
	SourceResolver          DeliverySourceResolver
	RolloutReconciler       DeliveryRolloutReconciler
	SystemRolloutReconciler DeliverySystemRolloutReconciler
}

// Validate rejects an incomplete delivery graph before a worker can subscribe
// to Redis. runtimeDependencyAvailable also rejects interfaces containing a
// typed nil pointer.
func (runtime DeliveryRuntime) Validate() error {
	return validateRequiredRuntimeDependencies("delivery task", []requiredRuntimeDependency{
		{name: "source_resolver", value: runtime.SourceResolver},
		{name: "rollout_reconciler", value: runtime.RolloutReconciler},
		{name: "system_rollout_reconciler", value: runtime.SystemRolloutReconciler},
	})
}

// HandlerBindings returns the exact task-type-to-handler graph consumed by a
// standalone worker. The returned map is a new value so callers can clone it
// into their own immutable composition state.
func (runtime DeliveryRuntime) HandlerBindings() (map[string]asynq.HandlerFunc, error) {
	if err := runtime.Validate(); err != nil {
		return nil, fmt.Errorf("validate delivery runtime: %w", err)
	}
	return map[string]asynq.HandlerFunc{
		DeliverySourceResolutionType:       runtime.HandleDeliverySourceResolution,
		DeliveryRolloutReconcileType:       runtime.HandleDeliveryRolloutReconcile,
		DeliverySystemRolloutReconcileType: runtime.HandleDeliverySystemRolloutReconcile,
	}, nil
}
