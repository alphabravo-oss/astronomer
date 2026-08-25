package tasks

import (
	"context"
	"errors"

	"github.com/hibiken/asynq"

	deliverymetrics "github.com/alphabravocompany/astronomer-go/internal/delivery/metrics"
)

const DeliverySystemRolloutReconcileType = "delivery:system_rollout_reconcile"

type DeliverySystemRolloutReconciler interface {
	Sweep(context.Context, int) error
}

func (runtime DeliveryRuntime) HandleDeliverySystemRolloutReconcile(ctx context.Context, task *asynq.Task) (finalErr error) {
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
	reconciler := runtime.SystemRolloutReconciler
	if reconciler == nil {
		return errors.New("delivery system rollout reconciler is not configured")
	}
	if len(task.Payload()) != 0 {
		return errors.New("delivery system rollout sweep does not accept a payload")
	}
	return reconciler.Sweep(ctx, 16)
}
