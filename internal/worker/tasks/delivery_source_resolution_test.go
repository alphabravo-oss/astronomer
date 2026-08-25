package tasks

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

type sourceResolverRecorder struct {
	one   uuid.UUID
	sweep int
}

func (r *sourceResolverRecorder) ResolveOne(_ context.Context, id uuid.UUID) error {
	r.one = id
	return nil
}
func (r *sourceResolverRecorder) Sweep(_ context.Context, limit int) error {
	r.sweep = limit
	return nil
}

func TestDeliverySourceResolutionTaskRoutesExactAndSweepWakeups(t *testing.T) {
	recorder := &sourceResolverRecorder{}
	runtime := DeliveryRuntime{SourceResolver: recorder}
	id := uuid.New()
	task, err := NewDeliverySourceResolutionTask(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.HandleDeliverySourceResolution(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if recorder.one != id {
		t.Fatalf("resolved %s, want %s", recorder.one, id)
	}
	if err := runtime.HandleDeliverySourceResolution(context.Background(), asynq.NewTask(DeliverySourceResolutionType, []byte(`{}`))); err != nil {
		t.Fatal(err)
	}
	if recorder.sweep != deliverySourceSweepLimit {
		t.Fatalf("sweep limit = %d", recorder.sweep)
	}
}

func TestDeliverySourceResolutionTaskRejectsUnknownFields(t *testing.T) {
	runtime := DeliveryRuntime{SourceResolver: &sourceResolverRecorder{}}
	err := runtime.HandleDeliverySourceResolution(context.Background(), asynq.NewTask(DeliverySourceResolutionType, []byte(`{"resolution_id":"`+uuid.NewString()+`","secret":"no"}`)))
	if err == nil {
		t.Fatal("unknown payload field was accepted")
	}
}

func TestDeliverySourceResolutionTaskRejectsMissingRuntime(t *testing.T) {
	if err := (DeliveryRuntime{}).HandleDeliverySourceResolution(context.Background(), asynq.NewTask(DeliverySourceResolutionType, nil)); err == nil {
		t.Fatal("missing source resolver was accepted")
	}
}
