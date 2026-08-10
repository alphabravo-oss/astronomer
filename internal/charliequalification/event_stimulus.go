package charliequalification

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

const qualificationQueueTaskType = "catalog:sync"

type EventStimulus interface {
	PublishTerminalQueueFailure(context.Context, string) error
}

type AsynqEventStimulus struct{ client *asynq.Client }

func NewAsynqEventStimulus(redisURL string) (*AsynqEventStimulus, error) {
	options, err := asynq.ParseRedisURI(redisURL)
	if err != nil {
		return nil, fmt.Errorf("qualification queue transport is invalid")
	}
	return &AsynqEventStimulus{client: asynq.NewClient(options)}, nil
}

func (s *AsynqEventStimulus) PublishTerminalQueueFailure(ctx context.Context, taskID string) error {
	parsed, err := uuid.Parse(taskID)
	if s == nil || s.client == nil || err != nil || parsed == uuid.Nil {
		return fmt.Errorf("qualification queue event identity is invalid")
	}
	// Malformed JSON fails before catalog code reads product state. MaxRetry(0)
	// makes that normal handler error enter Asynq's terminal ErrorHandler once.
	_, err = s.client.EnqueueContext(ctx, asynq.NewTask(qualificationQueueTaskType, []byte("{")),
		asynq.TaskID(taskID), asynq.Queue("default"), asynq.MaxRetry(0), asynq.Timeout(30*time.Second), asynq.Retention(10*time.Minute))
	if err != nil {
		return fmt.Errorf("qualification queue event could not be enqueued")
	}
	return nil
}

func (s *AsynqEventStimulus) Close() error {
	if s == nil || s.client == nil {
		return nil
	}
	return s.client.Close()
}
