package tasks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

const CharlieTriggerDispatchType = "charlie:trigger_dispatch"

type CharlieTriggerDispatcher interface {
	Dispatch(context.Context, uuid.UUID) error
}

// CharlieTriggerRuntime owns the dynamically activated Product Bridge
// dispatcher while keeping the queue handler itself explicitly bound.
type CharlieTriggerRuntime struct {
	mu         sync.RWMutex
	dispatcher CharlieTriggerDispatcher
}

func (runtime *CharlieTriggerRuntime) SetDispatcher(dispatcher CharlieTriggerDispatcher) {
	if runtime == nil {
		return
	}
	runtime.mu.Lock()
	runtime.dispatcher = dispatcher
	runtime.mu.Unlock()
}

func (runtime *CharlieTriggerRuntime) Dispatcher() CharlieTriggerDispatcher {
	if runtime == nil {
		return nil
	}
	runtime.mu.RLock()
	defer runtime.mu.RUnlock()
	return runtime.dispatcher
}

func (runtime *CharlieTriggerRuntime) HandlerBindings() (map[string]asynq.HandlerFunc, error) {
	if runtime == nil {
		return nil, fmt.Errorf("Charlie trigger runtime is nil")
	}
	return map[string]asynq.HandlerFunc{CharlieTriggerDispatchType: runtime.HandleCharlieTriggerDispatch}, nil
}

func (runtime *CharlieTriggerRuntime) HandleCharlieTriggerDispatch(ctx context.Context, task *asynq.Task) error {
	dispatcher := runtime.Dispatcher()
	if dispatcher == nil {
		return fmt.Errorf("Charlie trigger dispatcher is inactive")
	}
	var payload struct {
		EventID string `json:"event_id"`
	}
	decoder := json.NewDecoder(bytes.NewReader(task.Payload()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return fmt.Errorf("decode Charlie trigger task: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("Charlie trigger task contains trailing data")
	}
	eventID, err := uuid.Parse(payload.EventID)
	if err != nil {
		return fmt.Errorf("Charlie trigger task event ID is invalid")
	}
	return dispatcher.Dispatch(ctx, eventID)
}
