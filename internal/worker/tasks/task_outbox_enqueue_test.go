package tasks

import (
	"context"
	"testing"
	"time"

	"github.com/hibiken/asynq"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type fakeTaskOutboxWriter struct {
	arg sqlc.UpsertTaskOutboxParams
}

func (f *fakeTaskOutboxWriter) UpsertTaskOutbox(_ context.Context, arg sqlc.UpsertTaskOutboxParams) (sqlc.TaskOutbox, error) {
	f.arg = arg
	return sqlc.TaskOutbox{}, nil
}

func TestEnqueueTaskOutboxAppliesDefaults(t *testing.T) {
	w := &fakeTaskOutboxWriter{}
	task := asynq.NewTask("example:work", []byte(`{"ok":true}`))

	if _, err := EnqueueTaskOutbox(context.Background(), w, task, TaskOutboxOptions{}); err != nil {
		t.Fatalf("EnqueueTaskOutbox: %v", err)
	}

	if w.arg.TaskType != "example:work" {
		t.Fatalf("task type = %q", w.arg.TaskType)
	}
	if string(w.arg.Payload) != `{"ok":true}` {
		t.Fatalf("payload = %q", string(w.arg.Payload))
	}
	if w.arg.QueueName != "default" {
		t.Fatalf("queue = %q, want default", w.arg.QueueName)
	}
	if w.arg.MaxDeliveryAttempts != 20 {
		t.Fatalf("max delivery attempts = %d, want 20", w.arg.MaxDeliveryAttempts)
	}
}

func TestEnqueueTaskOutboxPreservesOptions(t *testing.T) {
	next := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	w := &fakeTaskOutboxWriter{}
	task := asynq.NewTask("example:work", nil)

	if _, err := EnqueueTaskOutbox(context.Background(), w, task, TaskOutboxOptions{
		DedupeKey:           "cluster:one",
		QueueName:           "critical",
		MaxRetry:            7,
		Timeout:             30 * time.Second,
		Unique:              time.Minute,
		MaxDeliveryAttempts: 5,
		NextAttemptAt:       next,
	}); err != nil {
		t.Fatalf("EnqueueTaskOutbox: %v", err)
	}

	if !w.arg.DedupeKey.Valid || w.arg.DedupeKey.String != "cluster:one" {
		t.Fatalf("dedupe key = %#v", w.arg.DedupeKey)
	}
	if w.arg.QueueName != "critical" || w.arg.MaxRetry != 7 {
		t.Fatalf("queue/max_retry = %s/%d", w.arg.QueueName, w.arg.MaxRetry)
	}
	if w.arg.TimeoutSeconds != 30 || w.arg.UniqueSeconds != 60 {
		t.Fatalf("timeout/unique seconds = %d/%d", w.arg.TimeoutSeconds, w.arg.UniqueSeconds)
	}
	if !w.arg.NextAttemptAt.Time.Equal(next) {
		t.Fatalf("next_attempt_at = %s, want %s", w.arg.NextAttemptAt.Time, next)
	}
}
