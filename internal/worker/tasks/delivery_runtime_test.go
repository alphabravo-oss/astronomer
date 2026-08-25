package tasks

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

type completeDeliveryRuntimeStub struct {
	systemSweeps int
}

func (stub *completeDeliveryRuntimeStub) ResolveOne(context.Context, uuid.UUID) error {
	return nil
}

func (stub *completeDeliveryRuntimeStub) ReconcileOne(context.Context, uuid.UUID) error {
	return nil
}

func (stub *completeDeliveryRuntimeStub) Sweep(context.Context, int) error {
	stub.systemSweeps++
	return nil
}

func TestDeliveryRuntimeRejectsEveryMissingDependency(t *testing.T) {
	stub := &completeDeliveryRuntimeStub{}
	tests := []struct {
		name    string
		runtime DeliveryRuntime
		missing string
	}{
		{
			name: "source resolver",
			runtime: DeliveryRuntime{
				RolloutReconciler:       stub,
				SystemRolloutReconciler: stub,
			},
			missing: "source_resolver",
		},
		{
			name: "rollout reconciler",
			runtime: DeliveryRuntime{
				SourceResolver:          stub,
				SystemRolloutReconciler: stub,
			},
			missing: "rollout_reconciler",
		},
		{
			name: "system rollout reconciler",
			runtime: DeliveryRuntime{
				SourceResolver:    stub,
				RolloutReconciler: stub,
			},
			missing: "system_rollout_reconciler",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.runtime.Validate()
			if err == nil || !strings.Contains(err.Error(), test.missing) {
				t.Fatalf("Validate() = %v, want missing %s", err, test.missing)
			}
		})
	}
}

func TestDeliveryRuntimeBuildsCompleteHandlerGraph(t *testing.T) {
	stub := &completeDeliveryRuntimeStub{}
	runtime := DeliveryRuntime{
		SourceResolver:          stub,
		RolloutReconciler:       stub,
		SystemRolloutReconciler: stub,
	}
	bindings, err := runtime.HandlerBindings()
	if err != nil {
		t.Fatal(err)
	}
	for _, taskType := range []string{
		DeliverySourceResolutionType,
		DeliveryRolloutReconcileType,
		DeliverySystemRolloutReconcileType,
	} {
		if bindings[taskType] == nil {
			t.Errorf("handler binding %q is nil", taskType)
		}
	}
	if err := bindings[DeliverySystemRolloutReconcileType](context.Background(), asynq.NewTask(DeliverySystemRolloutReconcileType, nil)); err != nil {
		t.Fatal(err)
	}
	if stub.systemSweeps != 1 {
		t.Fatalf("system sweeps = %d, want 1", stub.systemSweeps)
	}
}
