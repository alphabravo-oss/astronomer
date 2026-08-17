package tasks

import (
	"context"
	"errors"
	"sync"

	"github.com/hibiken/asynq"

	deliverymetrics "github.com/alphabravocompany/astronomer-go/internal/delivery/metrics"
)

const DeliverySystemRolloutReconcileType = "delivery:system_rollout_reconcile"

type DeliverySystemRolloutReconciler interface {
	Sweep(context.Context, int) error
}

var deliverySystemRolloutRuntime struct {
	sync.RWMutex
	reconciler DeliverySystemRolloutReconciler
}

func ConfigureDeliverySystemRolloutReconciler(reconciler DeliverySystemRolloutReconciler) {
	deliverySystemRolloutRuntime.Lock()
	deliverySystemRolloutRuntime.reconciler = reconciler
	deliverySystemRolloutRuntime.Unlock()
}

func HandleDeliverySystemRolloutReconcile(ctx context.Context, task *asynq.Task) (finalErr error) {
	defer func() {
		result := "success"
		if finalErr != nil {
			result = "failure"
		}
		deliverymetrics.ObserveWorker("system_rollout", result)
	}()
	if task == nil {
		return errors.New("delivery system rollout task is required")
	}
	deliverySystemRolloutRuntime.RLock()
	reconciler := deliverySystemRolloutRuntime.reconciler
	deliverySystemRolloutRuntime.RUnlock()
	if reconciler == nil {
		return errors.New("delivery system rollout reconciler is not configured")
	}
	if len(task.Payload()) != 0 {
		return errors.New("delivery system rollout sweep does not accept a payload")
	}
	return reconciler.Sweep(ctx, 16)
}
