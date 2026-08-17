package tasks

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/rollout"
)

type fakeDeliveryRolloutReconciler struct {
	oneID      uuid.UUID
	oneCalls   int
	sweepLimit int
	sweepCalls int
	err        error
}

func TestDeliveryRolloutTaskTypeMatchesDurableOutboxContract(t *testing.T) {
	if DeliveryRolloutReconcileType != rollout.TaskType {
		t.Fatalf("worker task type %q does not match rollout outbox task type %q", DeliveryRolloutReconcileType, rollout.TaskType)
	}
}

func (f *fakeDeliveryRolloutReconciler) ReconcileOne(_ context.Context, id uuid.UUID) error {
	f.oneID = id
	f.oneCalls++
	return f.err
}

func (f *fakeDeliveryRolloutReconciler) Sweep(_ context.Context, limit int) error {
	f.sweepLimit = limit
	f.sweepCalls++
	return f.err
}

func TestDeliveryRolloutTaskDispatchesSingleAndSweep(t *testing.T) {
	configured := &fakeDeliveryRolloutReconciler{}
	ConfigureDeliveryRolloutReconciler(configured)
	t.Cleanup(func() { ConfigureDeliveryRolloutReconciler(nil) })

	id := uuid.New()
	task, err := NewDeliveryRolloutTask(id)
	if err != nil {
		t.Fatal(err)
	}
	if task.Type() != DeliveryRolloutReconcileType {
		t.Fatalf("task type = %q", task.Type())
	}
	if err := HandleDeliveryRolloutReconcile(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if configured.oneCalls != 1 || configured.oneID != id || configured.sweepCalls != 0 {
		t.Fatalf("single dispatch = %+v", configured)
	}

	if err := HandleDeliveryRolloutReconcile(context.Background(), asynq.NewTask(DeliveryRolloutReconcileType, []byte(`{}`))); err != nil {
		t.Fatal(err)
	}
	if configured.sweepCalls != 1 || configured.sweepLimit != deliveryRolloutSweepLimit {
		t.Fatalf("sweep dispatch = %+v", configured)
	}
}

func TestDeliveryRolloutTaskRejectsInvalidInputs(t *testing.T) {
	if _, err := NewDeliveryRolloutTask(uuid.Nil); err == nil {
		t.Fatal("zero rollout ID was accepted")
	}
	ConfigureDeliveryRolloutReconciler(nil)
	if err := HandleDeliveryRolloutReconcile(context.Background(), &asynq.Task{}); err == nil {
		t.Fatal("unconfigured reconciler was accepted")
	}

	configured := &fakeDeliveryRolloutReconciler{}
	ConfigureDeliveryRolloutReconciler(configured)
	t.Cleanup(func() { ConfigureDeliveryRolloutReconciler(nil) })
	for _, payload := range [][]byte{
		[]byte(`{"unknown":true}`),
		[]byte(`{"rollout_id":"not-a-uuid"}`),
		[]byte(`{} {}`),
	} {
		if err := HandleDeliveryRolloutReconcile(context.Background(), asynq.NewTask(DeliveryRolloutReconcileType, payload)); err == nil {
			t.Fatalf("invalid payload %q was accepted", payload)
		}
	}
	if err := HandleDeliveryRolloutReconcile(context.Background(), nil); err == nil {
		t.Fatal("nil task was accepted")
	}
	if configured.oneCalls != 0 || configured.sweepCalls != 0 {
		t.Fatalf("invalid tasks reached reconciler: %+v", configured)
	}

	configured.err = errors.New("database unavailable")
	if err := HandleDeliveryRolloutReconcile(context.Background(), asynq.NewTask(DeliveryRolloutReconcileType, nil)); !errors.Is(err, configured.err) {
		t.Fatalf("reconciler error = %v", err)
	}
}
