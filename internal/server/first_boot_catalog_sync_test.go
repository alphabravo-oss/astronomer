package server

// Sprint 075 — first-boot catalog:sync kick.
//
// kickFirstBootCatalogSync is the small helper NewApp calls after the
// CRD controller is started. The unit tests below cover its full
// behavior matrix without needing Redis or a Postgres pool:
//
//   - TestCatalogSeed_FirstBootSync_EnqueuesWhenChartsEmpty — the happy
//     path that closes the gap. helm_charts is empty → exactly one
//     catalog:sync task is enqueued with the canonical task type and
//     an empty CatalogSyncPayload.
//   - TestCatalogSeed_FirstBootSync_NoOpWhenChartsExist — catalog has
//     rows (subsequent server start, or scheduler already ran) → no
//     task is enqueued. Steady-state must stay quiet.
//   - TestCatalogSeed_FirstBootSync_NilSafe — when queue or queries is
//     nil (worker-only process / test rigs) the helper must not panic
//     or attempt an enqueue.
//   - TestCatalogSeed_FirstBootSync_EnqueueFailureIsBestEffort — a
//     Redis enqueue failure must NOT fail server startup; the helper
//     just logs and returns.
//   - TestCatalogSeed_FirstBootSync_CountFailureIsBestEffort — a DB
//     count failure must NOT fail server startup either.

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/hibiken/asynq"
)

type fakeChartCounter struct {
	count int64
	err   error
	calls int
}

func (f *fakeChartCounter) CountHelmCharts(_ context.Context) (int64, error) {
	f.calls++
	return f.count, f.err
}

type fakeFirstBootEnqueuer struct {
	enqueued []*asynq.Task
	err      error
}

func (f *fakeFirstBootEnqueuer) Enqueue(task *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.enqueued = append(f.enqueued, task)
	return &asynq.TaskInfo{ID: "test"}, nil
}

func newDiscardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestCatalogSeed_FirstBootSync_EnqueuesWhenChartsEmpty(t *testing.T) {
	counter := &fakeChartCounter{count: 0}
	queue := &fakeFirstBootEnqueuer{}
	kickFirstBootCatalogSync(context.Background(), newDiscardLogger(), counter, queue)

	if counter.calls != 1 {
		t.Fatalf("CountHelmCharts calls = %d, want 1", counter.calls)
	}
	if got, want := len(queue.enqueued), 1; got != want {
		t.Fatalf("enqueued tasks = %d, want %d", got, want)
	}
	if got, want := queue.enqueued[0].Type(), "catalog:sync"; got != want {
		t.Errorf("task type = %q, want %q", got, want)
	}
	// The kick always sends an empty payload — sync-all-repos.
	if got := string(queue.enqueued[0].Payload()); got != `{}` {
		t.Errorf("payload = %q, want %q", got, `{}`)
	}
}

func TestCatalogSeed_FirstBootSync_NoOpWhenChartsExist(t *testing.T) {
	counter := &fakeChartCounter{count: 42}
	queue := &fakeFirstBootEnqueuer{}
	kickFirstBootCatalogSync(context.Background(), newDiscardLogger(), counter, queue)

	if counter.calls != 1 {
		t.Fatalf("CountHelmCharts calls = %d, want 1", counter.calls)
	}
	if got, want := len(queue.enqueued), 0; got != want {
		t.Fatalf("enqueued tasks = %d, want %d (no-op when catalog populated)", got, want)
	}
}

func TestCatalogSeed_FirstBootSync_NilSafe(t *testing.T) {
	// Nil queries: helper short-circuits before any Enqueue.
	queue := &fakeFirstBootEnqueuer{}
	kickFirstBootCatalogSync(context.Background(), newDiscardLogger(), nil, queue)
	if len(queue.enqueued) != 0 {
		t.Errorf("nil-queries path enqueued %d tasks, want 0", len(queue.enqueued))
	}

	// Nil queue: helper short-circuits before CountHelmCharts. The
	// counter MUST NOT be called (a DB query when the queue isn't even
	// wired would be wasted work + a misleading log line).
	counter := &fakeChartCounter{count: 0}
	kickFirstBootCatalogSync(context.Background(), newDiscardLogger(), counter, nil)
	if counter.calls != 0 {
		t.Errorf("nil-queue path called CountHelmCharts %d times, want 0", counter.calls)
	}
}

func TestCatalogSeed_FirstBootSync_EnqueueFailureIsBestEffort(t *testing.T) {
	counter := &fakeChartCounter{count: 0}
	queue := &fakeFirstBootEnqueuer{err: errors.New("redis down")}
	// MUST NOT panic, MUST NOT propagate the error. The test passes by
	// the helper returning cleanly.
	kickFirstBootCatalogSync(context.Background(), newDiscardLogger(), counter, queue)
	if len(queue.enqueued) != 0 {
		t.Fatalf("enqueued = %d, want 0 (Enqueue failed)", len(queue.enqueued))
	}
}

func TestCatalogSeed_FirstBootSync_CountFailureIsBestEffort(t *testing.T) {
	counter := &fakeChartCounter{err: errors.New("db hiccup")}
	queue := &fakeFirstBootEnqueuer{}
	kickFirstBootCatalogSync(context.Background(), newDiscardLogger(), counter, queue)
	if len(queue.enqueued) != 0 {
		t.Errorf("enqueued = %d, want 0 (count failed, must not enqueue)", len(queue.enqueued))
	}
}
