package tasks

import (
	"fmt"

	"github.com/alphabravocompany/astronomer-go/internal/charlie"
	"github.com/hibiken/asynq"
)

// CharlieAlertRuntime owns the complete worker-side alert planning and
// delivery graph. It is bound into the worker mux at construction time.
type CharlieAlertRuntime struct {
	Queries    CharlieAlertDispatchQuerier
	WriteFence *charlie.WriteFence
	Reconciler CharlieAlertReconciler
}

func (runtime CharlieAlertRuntime) Validate() error {
	return validateRequiredRuntimeDependencies("Charlie alert", []requiredRuntimeDependency{
		{name: "queries", value: runtime.Queries},
		{name: "write_fence", value: runtime.WriteFence},
		{name: "reconciler", value: runtime.Reconciler},
	})
}

func (runtime CharlieAlertRuntime) HandlerBindings() (map[string]asynq.HandlerFunc, error) {
	if err := runtime.Validate(); err != nil {
		return nil, fmt.Errorf("validate Charlie alert runtime: %w", err)
	}
	return map[string]asynq.HandlerFunc{
		CharlieAlertDispatchTaskType: runtime.HandleCharlieAlertDispatch,
		CharlieAlertReconcileType:    runtime.HandleCharlieAlertReconcile,
	}, nil
}
