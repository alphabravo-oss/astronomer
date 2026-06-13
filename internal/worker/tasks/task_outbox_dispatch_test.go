package tasks

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type fakeTaskOutboxQuerier struct {
	rows      []sqlc.TaskOutbox
	delivered []sqlc.MarkTaskOutboxDeliveredParams
	failed    []sqlc.MarkTaskOutboxFailedParams
}

func (f *fakeTaskOutboxQuerier) ClaimDueTaskOutbox(_ context.Context, _ sqlc.ClaimDueTaskOutboxParams) ([]sqlc.TaskOutbox, error) {
	return f.rows, nil
}

func (f *fakeTaskOutboxQuerier) MarkTaskOutboxDelivered(_ context.Context, arg sqlc.MarkTaskOutboxDeliveredParams) error {
	f.delivered = append(f.delivered, arg)
	return nil
}

func (f *fakeTaskOutboxQuerier) MarkTaskOutboxFailed(_ context.Context, arg sqlc.MarkTaskOutboxFailedParams) error {
	f.failed = append(f.failed, arg)
	return nil
}

type fakeTaskOutboxEnqueuer struct {
	err   error
	tasks []*asynq.Task
	opts  [][]asynq.Option
}

func (f *fakeTaskOutboxEnqueuer) EnqueueContext(_ context.Context, task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.tasks = append(f.tasks, task)
	f.opts = append(f.opts, opts)
	return &asynq.TaskInfo{ID: "queued", Type: task.Type()}, nil
}

func makeTaskOutboxRow() sqlc.TaskOutbox {
	return sqlc.TaskOutbox{
		ID:                  uuid.New(),
		TaskType:            "example:work",
		Payload:             []byte(`{"id":"one"}`),
		QueueName:           "critical",
		MaxRetry:            5,
		TimeoutSeconds:      30,
		UniqueSeconds:       60,
		MaxDeliveryAttempts: 3,
		AttemptCount:        1,
	}
}

func TestDispatchTaskOutboxOnceDeliversRows(t *testing.T) {
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	q := &fakeTaskOutboxQuerier{rows: []sqlc.TaskOutbox{makeTaskOutboxRow()}}
	e := &fakeTaskOutboxEnqueuer{}

	if err := DispatchTaskOutboxOnce(context.Background(), TaskOutboxDispatchDeps{
		Queries:  q,
		Enqueuer: e,
		Now:      func() time.Time { return now },
	}); err != nil {
		t.Fatalf("DispatchTaskOutboxOnce: %v", err)
	}

	if got := len(e.tasks); got != 1 {
		t.Fatalf("enqueued tasks = %d, want 1", got)
	}
	if got := e.tasks[0].Type(); got != "example:work" {
		t.Fatalf("task type = %q, want example:work", got)
	}
	if got := len(q.delivered); got != 1 {
		t.Fatalf("delivered rows = %d, want 1", got)
	}
	if q.delivered[0].DeliveredAt.Time != now {
		t.Fatalf("delivered_at = %s, want %s", q.delivered[0].DeliveredAt.Time, now)
	}
}

func TestDispatchTaskOutboxOnceRetriesEnqueueFailure(t *testing.T) {
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	q := &fakeTaskOutboxQuerier{rows: []sqlc.TaskOutbox{makeTaskOutboxRow()}}
	e := &fakeTaskOutboxEnqueuer{err: errors.New("redis down")}

	if err := DispatchTaskOutboxOnce(context.Background(), TaskOutboxDispatchDeps{
		Queries:  q,
		Enqueuer: e,
		Now:      func() time.Time { return now },
	}); err != nil {
		t.Fatalf("DispatchTaskOutboxOnce: %v", err)
	}

	if got := len(q.failed); got != 1 {
		t.Fatalf("failed rows = %d, want 1", got)
	}
	if q.failed[0].Status != "failed" {
		t.Fatalf("status = %q, want failed", q.failed[0].Status)
	}
	if !q.failed[0].NextAttemptAt.Time.After(now) {
		t.Fatalf("next_attempt_at = %s, want after %s", q.failed[0].NextAttemptAt.Time, now)
	}
}

func TestDispatchTaskOutboxOnceMarksDuplicateDeliveryAsDelivered(t *testing.T) {
	q := &fakeTaskOutboxQuerier{rows: []sqlc.TaskOutbox{makeTaskOutboxRow()}}
	e := &fakeTaskOutboxEnqueuer{err: asynq.ErrTaskIDConflict}

	if err := DispatchTaskOutboxOnce(context.Background(), TaskOutboxDispatchDeps{
		Queries:  q,
		Enqueuer: e,
		Now:      time.Now,
	}); err != nil {
		t.Fatalf("DispatchTaskOutboxOnce: %v", err)
	}

	if got := len(q.delivered); got != 1 {
		t.Fatalf("delivered rows = %d, want 1", got)
	}
	if got := len(q.failed); got != 0 {
		t.Fatalf("failed rows = %d, want 0", got)
	}
}

func TestDispatchTaskOutboxOnceMovesExhaustedRowsToDead(t *testing.T) {
	row := makeTaskOutboxRow()
	row.AttemptCount = row.MaxDeliveryAttempts
	q := &fakeTaskOutboxQuerier{rows: []sqlc.TaskOutbox{row}}
	e := &fakeTaskOutboxEnqueuer{err: errors.New("redis down")}

	if err := DispatchTaskOutboxOnce(context.Background(), TaskOutboxDispatchDeps{
		Queries:  q,
		Enqueuer: e,
		Now:      time.Now,
	}); err != nil {
		t.Fatalf("DispatchTaskOutboxOnce: %v", err)
	}

	if got := len(q.failed); got != 1 {
		t.Fatalf("failed rows = %d, want 1", got)
	}
	if q.failed[0].Status != "dead" {
		t.Fatalf("status = %q, want dead", q.failed[0].Status)
	}
}

func TestDispatchTaskOutboxOnceSkipsWhenUnconfigured(t *testing.T) {
	if err := DispatchTaskOutboxOnce(context.Background(), TaskOutboxDispatchDeps{}); err != nil {
		t.Fatalf("DispatchTaskOutboxOnce unconfigured: %v", err)
	}
}

func TestTaskOutboxBackoffCaps(t *testing.T) {
	if got := taskOutboxBackoff(1); got != 2*time.Second {
		t.Fatalf("backoff(1) = %s, want 2s", got)
	}
	if got := taskOutboxBackoff(99); got != 256*time.Second {
		t.Fatalf("backoff(99) = %s, want 256s", got)
	}
}

func TestTaskOutboxOptionDefaults(t *testing.T) {
	row := makeTaskOutboxRow()
	row.TimeoutSeconds = 0
	row.UniqueSeconds = 0
	row.NextAttemptAt = pgtype.Timestamptz{}

	opts := taskOutboxOptions(row)
	if len(opts) != 3 {
		t.Fatalf("options = %d, want 3 base options", len(opts))
	}
}
