package tasks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"

	deliverymetrics "github.com/alphabravocompany/astronomer-go/internal/delivery/metrics"
)

// DeliveryRolloutReconcileType is deliberately a literal in the worker task
// package. The operation/task inventory statically resolves worker registry and
// scheduler expressions, and keeping the wire identifier visible here makes
// the registered Flux delivery task part of that checked contract.
const DeliveryRolloutReconcileType = "delivery:rollout_reconcile"
const deliveryRolloutSweepLimit = 16

type DeliveryRolloutReconciler interface {
	ReconcileOne(context.Context, uuid.UUID) error
	Sweep(context.Context, int) error
}

var deliveryRolloutRuntime struct {
	sync.RWMutex
	reconciler DeliveryRolloutReconciler
}

func ConfigureDeliveryRolloutReconciler(reconciler DeliveryRolloutReconciler) {
	deliveryRolloutRuntime.Lock()
	deliveryRolloutRuntime.reconciler = reconciler
	deliveryRolloutRuntime.Unlock()
}

func NewDeliveryRolloutTask(rolloutID uuid.UUID) (*asynq.Task, error) {
	if rolloutID == uuid.Nil {
		return nil, errors.New("delivery rollout task requires a rollout ID")
	}
	payload, err := json.Marshal(struct {
		RolloutID uuid.UUID `json:"rollout_id"`
	}{rolloutID})
	if err != nil {
		return nil, err
	}
	return asynq.NewTask(DeliveryRolloutReconcileType, payload), nil
}

func HandleDeliveryRolloutReconcile(ctx context.Context, task *asynq.Task) (finalErr error) {
	defer func() {
		result := "success"
		if finalErr != nil {
			result = "failure"
		}
		deliverymetrics.ObserveWorker("rollout", result)
	}()
	if task == nil {
		return errors.New("delivery rollout task is required")
	}
	deliveryRolloutRuntime.RLock()
	reconciler := deliveryRolloutRuntime.reconciler
	deliveryRolloutRuntime.RUnlock()
	if reconciler == nil {
		return errors.New("delivery rollout reconciler is not configured")
	}
	var payload struct {
		RolloutID uuid.UUID `json:"rollout_id"`
	}
	if len(task.Payload()) != 0 && string(task.Payload()) != "{}" {
		decoder := json.NewDecoder(bytes.NewReader(task.Payload()))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&payload); err != nil {
			return fmt.Errorf("decode delivery rollout task: %w", err)
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			if err == nil {
				err = errors.New("multiple JSON values")
			}
			return fmt.Errorf("decode delivery rollout task: %w", err)
		}
	}
	if payload.RolloutID == uuid.Nil {
		return reconciler.Sweep(ctx, deliveryRolloutSweepLimit)
	}
	return reconciler.ReconcileOne(ctx, payload.RolloutID)
}
