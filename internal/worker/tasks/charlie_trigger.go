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

var (
	charlieTriggerMu         sync.RWMutex
	charlieTriggerDispatcher CharlieTriggerDispatcher
)

func ConfigureCharlieTriggerDispatcher(dispatcher CharlieTriggerDispatcher) {
	charlieTriggerMu.Lock()
	defer charlieTriggerMu.Unlock()
	charlieTriggerDispatcher = dispatcher
}

func HandleCharlieTriggerDispatch(ctx context.Context, task *asynq.Task) error {
	charlieTriggerMu.RLock()
	dispatcher := charlieTriggerDispatcher
	charlieTriggerMu.RUnlock()
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
