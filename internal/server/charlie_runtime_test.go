package server

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

type runtimeDispatcherFake struct{ calls atomic.Int32 }

func (f *runtimeDispatcherFake) Dispatch(context.Context, uuid.UUID) error {
	f.calls.Add(1)
	return nil
}

func TestCharlieRuntimeGenerationCannotRegisterAfterShutdown(t *testing.T) {
	tasks.ConfigureCharlieTriggerDispatcher(nil)
	t.Cleanup(func() { tasks.ConfigureCharlieTriggerDispatcher(nil) })
	dispatcher := &runtimeDispatcherFake{}
	generation := &charlieRuntimeGeneration{dispatcher: dispatcher}
	if err := generation.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	generation.Run(t.Context())
	task := asynq.NewTask(tasks.CharlieTriggerDispatchType, []byte(`{"event_id":"`+uuid.NewString()+`"}`))
	if err := tasks.HandleCharlieTriggerDispatch(t.Context(), task); err == nil {
		t.Fatal("stopped generation registered its trigger dispatcher")
	}
}

func TestCharlieRuntimeGenerationDeregistersDispatcherOnShutdown(t *testing.T) {
	tasks.ConfigureCharlieTriggerDispatcher(nil)
	t.Cleanup(func() { tasks.ConfigureCharlieTriggerDispatcher(nil) })
	dispatcher := &runtimeDispatcherFake{}
	generation := &charlieRuntimeGeneration{dispatcher: dispatcher}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		generation.Run(ctx)
		close(done)
	}()
	task := asynq.NewTask(tasks.CharlieTriggerDispatchType, []byte(`{"event_id":"`+uuid.NewString()+`"}`))
	deadline := time.Now().Add(time.Second)
	for {
		if err := tasks.HandleCharlieTriggerDispatch(t.Context(), task); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("running generation did not register its trigger dispatcher")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := generation.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	<-done
	if err := tasks.HandleCharlieTriggerDispatch(t.Context(), task); err == nil {
		t.Fatal("stopped generation retained its trigger dispatcher")
	}
}
