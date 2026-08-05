package tasks

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

type charlieTriggerDispatcherFake struct {
	eventID uuid.UUID
	calls   int
}

func (f *charlieTriggerDispatcherFake) Dispatch(_ context.Context, eventID uuid.UUID) error {
	f.calls++
	f.eventID = eventID
	return nil
}

func TestCharlieTriggerTaskDispatchesExactDurableEvent(t *testing.T) {
	fake := &charlieTriggerDispatcherFake{}
	ConfigureCharlieTriggerDispatcher(fake)
	t.Cleanup(func() { ConfigureCharlieTriggerDispatcher(nil) })
	eventID := uuid.New()
	task := asynq.NewTask(CharlieTriggerDispatchType, []byte(`{"event_id":"`+eventID.String()+`"}`))
	if err := HandleCharlieTriggerDispatch(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if fake.calls != 1 || fake.eventID != eventID {
		t.Fatalf("dispatcher received calls=%d event=%s", fake.calls, fake.eventID)
	}
}

func TestCharlieTriggerTaskFailsClosedOnInvalidPayloadOrInactiveRuntime(t *testing.T) {
	ConfigureCharlieTriggerDispatcher(nil)
	if err := HandleCharlieTriggerDispatch(context.Background(), asynq.NewTask(CharlieTriggerDispatchType, []byte(`{"event_id":"`+uuid.NewString()+`"}`))); err == nil {
		t.Fatal("inactive Charlie dispatcher accepted task")
	}
	fake := &charlieTriggerDispatcherFake{}
	ConfigureCharlieTriggerDispatcher(fake)
	t.Cleanup(func() { ConfigureCharlieTriggerDispatcher(nil) })
	for _, payload := range [][]byte{
		[]byte(`{"event_id":"not-a-uuid"}`),
		[]byte(`{"event_id":"` + uuid.NewString() + `","prompt":"secret"}`),
		[]byte(`{"event_id":"` + uuid.NewString() + `"} {}`),
	} {
		if err := HandleCharlieTriggerDispatch(context.Background(), asynq.NewTask(CharlieTriggerDispatchType, payload)); err == nil {
			t.Fatalf("invalid payload accepted: %s", payload)
		}
	}
	if fake.calls != 0 {
		t.Fatal("invalid task reached dispatcher")
	}
}
