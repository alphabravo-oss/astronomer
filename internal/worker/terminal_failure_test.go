package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/hibiken/asynq"
)

type terminalPublisherFake struct{ calls [][3]string }

func (f *terminalPublisherFake) PublishQueueTerminalFailure(_ context.Context, taskType, taskID, class string) error {
	f.calls = append(f.calls, [3]string{taskType, taskID, class})
	return nil
}

func TestQueueTerminalFailurePublishesOnlyTerminalAllowlistedWork(t *testing.T) {
	tests := []struct {
		name, taskType string
		retried, max   int
		err            error
		want           string
	}{
		{name: "retry remains", taskType: TypeCatalogSync, retried: 1, max: 2, err: errors.New("failed")},
		{name: "retry exhausted", taskType: TypeCatalogSync, retried: 2, max: 2, err: errors.New("failed"), want: "retry_exhausted"},
		{name: "non retryable", taskType: TypeCatalogSync, retried: 0, max: 25, err: asynq.SkipRetry, want: "non_retryable"},
		{name: "revoked", taskType: TypeCatalogSync, retried: 25, max: 25, err: asynq.RevokeTask},
		{name: "recursive Charlie task", taskType: TypeCharlieTriggerDispatch, retried: 25, max: 25, err: errors.New("failed")},
		{name: "unknown task", taskType: "attacker:forged", retried: 0, max: 0, err: errors.New("failed")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			publisher := &terminalPublisherFake{}
			if err := publishQueueTerminalFailure(context.Background(), publisher, test.taskType, "task-safe-id", test.retried, test.max, test.err); err != nil {
				t.Fatal(err)
			}
			if test.want == "" && len(publisher.calls) != 0 {
				t.Fatalf("unexpected publication=%v", publisher.calls)
			}
			if test.want != "" && (len(publisher.calls) != 1 || publisher.calls[0] != [3]string{test.taskType, "task-safe-id", test.want}) {
				t.Fatalf("publication=%v want class %s", publisher.calls, test.want)
			}
		})
	}
}
