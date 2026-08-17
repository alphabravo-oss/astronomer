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
	ConfigureDeliverySourceResolver(recorder)
	t.Cleanup(func() { ConfigureDeliverySourceResolver(nil) })
	id := uuid.New()
	task, err := NewDeliverySourceResolutionTask(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := HandleDeliverySourceResolution(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if recorder.one != id {
		t.Fatalf("resolved %s, want %s", recorder.one, id)
	}
	if err := HandleDeliverySourceResolution(context.Background(), asynq.NewTask(DeliverySourceResolutionType, []byte(`{}`))); err != nil {
		t.Fatal(err)
	}
	if recorder.sweep != deliverySourceSweepLimit {
		t.Fatalf("sweep limit = %d", recorder.sweep)
	}
}

func TestDeliverySourceResolutionTaskRejectsUnknownFields(t *testing.T) {
	ConfigureDeliverySourceResolver(&sourceResolverRecorder{})
	t.Cleanup(func() { ConfigureDeliverySourceResolver(nil) })
	err := HandleDeliverySourceResolution(context.Background(), asynq.NewTask(DeliverySourceResolutionType, []byte(`{"resolution_id":"`+uuid.NewString()+`","secret":"no"}`)))
	if err == nil {
		t.Fatal("unknown payload field was accepted")
	}
}
