package tasks

import (
	"context"
	"testing"
	"time"

	"github.com/hibiken/asynq"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type fakeTaskOutboxWriter struct {
	arg   sqlc.UpsertTaskOutboxParams
	calls int
}

func (f *fakeTaskOutboxWriter) UpsertTaskOutbox(_ context.Context, arg sqlc.UpsertTaskOutboxParams) (sqlc.TaskOutbox, error) {
	f.calls++
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

func TestEnqueueTaskOutboxRejectsInvalidJSONBeforeWrite(t *testing.T) {
	for _, raw := range []string{`null`, `[]`, `{"token":"private-fixture"} {}`, `{"_correlation_id":null}`, `{"_traceparent":42}`, `{"_tracestate":{}}`} {
		t.Run(raw, func(t *testing.T) {
			w := &fakeTaskOutboxWriter{}
			_, err := EnqueueTaskOutbox(t.Context(), w, asynq.NewTask("example:work", []byte(raw)), TaskOutboxOptions{})
			if err == nil || w.calls != 0 {
				t.Fatalf("invalid task accepted: error=%v writes=%d", err, w.calls)
			}
		})
	}
}

func TestEnqueueTaskOutboxLegacyEmptyObjectAdapter(t *testing.T) {
	w := &fakeTaskOutboxWriter{}
	if _, err := EnqueueTaskOutbox(t.Context(), w, asynq.NewTask("example:work", nil), TaskOutboxOptions{}); err != nil {
		t.Fatal(err)
	}
	if string(w.arg.Payload) != `{}` {
		t.Fatalf("legacy empty payload must write canonical object, got %q", w.arg.Payload)
	}
}

func TestEnqueueTaskOutboxRejectsUnrepresentableOptionsBeforeWrite(t *testing.T) {
	for _, opts := range []TaskOutboxOptions{
		{MaxRetry: int(1 << 31)}, {MaxDeliveryAttempts: int(1 << 31)},
		{Timeout: time.Duration(1<<31) * time.Second}, {Unique: time.Duration(1<<31) * time.Second},
		{Timeout: -time.Second}, {Unique: -time.Second},
	} {
		w := &fakeTaskOutboxWriter{}
		_, err := EnqueueTaskOutbox(t.Context(), w, asynq.NewTask("example:work", []byte(`{}`)), opts)
		if err == nil || w.calls != 0 {
			t.Errorf("unrepresentable task options reached SQL: err=%v writes=%d", err, w.calls)
		}
	}
}

func TestEnqueueTaskOutboxPreservesStorageBoundariesAndV1Defaults(t *testing.T) {
	w := &fakeTaskOutboxWriter{}
	cap := int(1<<31 - 1)
	_, err := EnqueueTaskOutbox(t.Context(), w, asynq.NewTask("example:work", []byte(`{}`)), TaskOutboxOptions{MaxRetry: cap, MaxDeliveryAttempts: cap, Timeout: time.Duration(cap)*time.Second + 400*time.Millisecond, Unique: time.Duration(cap) * time.Second})
	if err != nil || w.arg.MaxRetry != int32(cap) || w.arg.MaxDeliveryAttempts != int32(cap) || w.arg.TimeoutSeconds != int32(cap) || w.arg.UniqueSeconds != int32(cap) {
		t.Fatalf("valid storage boundary changed: err=%v params=%+v", err, w.arg)
	}
	w = &fakeTaskOutboxWriter{}
	_, err = EnqueueTaskOutbox(t.Context(), w, asynq.NewTask("example:work", nil), TaskOutboxOptions{MaxRetry: -1, MaxDeliveryAttempts: -1, Timeout: -400 * time.Millisecond, Unique: 400 * time.Millisecond})
	if err != nil || w.arg.MaxRetry != 0 || w.arg.MaxDeliveryAttempts != 20 || w.arg.TimeoutSeconds != 0 || w.arg.UniqueSeconds != 0 {
		t.Fatalf("v1 option defaults/rounding changed: err=%v params=%+v", err, w.arg)
	}
}
