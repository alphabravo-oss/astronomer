package audit

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

// fakeBatchQuerier records every BatchInsertAuditLog call as a separate
// batch. The mutex guards against concurrent appends from the writer's
// background goroutine.
type fakeBatchQuerier struct {
	mu      sync.Mutex
	batches [][]sqlc.CreateAuditLogV1Params
	err     error
	// block forces BatchInsertAuditLog to wait on this channel before
	// returning, letting tests pin the writer mid-flush.
	block chan struct{}
}

func (f *fakeBatchQuerier) BatchInsertAuditLog(_ context.Context, rows []sqlc.CreateAuditLogV1Params) error {
	if f.block != nil {
		<-f.block
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	// Copy so the slice we record is stable even if the writer reuses the
	// backing array.
	copyRows := make([]sqlc.CreateAuditLogV1Params, len(rows))
	copy(copyRows, rows)
	f.batches = append(f.batches, copyRows)
	return f.err
}

func (f *fakeBatchQuerier) totalRows() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, b := range f.batches {
		n += len(b)
	}
	return n
}

func (f *fakeBatchQuerier) batchCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.batches)
}

// makeEvent builds a minimal sqlc params row tagged with a unique action
// so tests can assert ordering / dedup if needed.
func makeRow(i int) sqlc.CreateAuditLogV1Params {
	return sqlc.CreateAuditLogV1Params{
		Source: "test",
		Action: fmt.Sprintf("test.action.%d", i),
	}
}

// waitUntil polls fn at 5 ms intervals until it returns true or the
// deadline fires. Tests use this instead of a fixed sleep so they stay
// fast on a healthy machine and don't flake on a loaded CI runner.
func waitUntil(t *testing.T, deadline time.Duration, fn func() bool) {
	t.Helper()
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		if fn() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("condition not met within %v", deadline)
}

func TestWriter_BatchesEvents(t *testing.T) {
	q := &fakeBatchQuerier{}
	w := NewWriter(q, nil,
		WithBatchSize(50),
		WithFlushInterval(50*time.Millisecond),
		WithBufferSize(256),
	)
	w.Start(context.Background())
	t.Cleanup(func() {
		_ = w.Shutdown(context.Background())
	})

	const total = 100
	for i := 0; i < total; i++ {
		if !w.Enqueue(makeRow(i)) {
			t.Fatalf("enqueue %d unexpectedly dropped", i)
		}
	}

	// At batchSize=50 the writer should flush in size-triggered batches
	// well before the 250ms ticker. We still allow some time because the
	// goroutine schedule isn't deterministic.
	waitUntil(t, time.Second, func() bool {
		return q.totalRows() >= total
	})

	if got := q.totalRows(); got != total {
		t.Fatalf("totalRows = %d, want %d", got, total)
	}
	// Expect the size threshold to dominate — at 50 events per batch
	// and 100 events total we should see exactly 2 batches if the
	// scheduler cooperates; allow 1-3 since ticker-driven flushes of
	// the trailing partial batch are possible if the test runs slowly.
	if got := q.batchCount(); got < 1 || got > 3 {
		t.Fatalf("batchCount = %d, want 1..3", got)
	}
}

func TestWriter_FlushesOnShutdown(t *testing.T) {
	q := &fakeBatchQuerier{}
	// Very long flush interval so the size threshold is the only thing
	// that could otherwise trigger a flush. We enqueue fewer than
	// batchSize events, so the only way they land is via the shutdown
	// drain.
	w := NewWriter(q, nil,
		WithBatchSize(50),
		WithFlushInterval(10*time.Second),
		WithBufferSize(256),
	)
	w.Start(context.Background())

	const total = 5
	for i := 0; i < total; i++ {
		if !w.Enqueue(makeRow(i)) {
			t.Fatalf("enqueue %d unexpectedly dropped", i)
		}
	}

	// Sanity: nothing has been flushed yet (the ticker is 10s out and we
	// haven't hit the batch threshold).
	if q.batchCount() != 0 {
		t.Fatalf("pre-shutdown batchCount = %d, want 0", q.batchCount())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := w.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown returned %v", err)
	}

	if got := q.totalRows(); got != total {
		t.Fatalf("post-shutdown totalRows = %d, want %d", got, total)
	}
	if got := q.batchCount(); got != 1 {
		t.Fatalf("post-shutdown batchCount = %d, want 1", got)
	}
}

func TestWriter_DropsOnOverflow(t *testing.T) {
	oldInstance := observability.InstanceID()
	observability.SetInstanceID("test-audit-drops")
	t.Cleanup(func() {
		observability.SetInstanceID(oldInstance)
	})

	before := auditDroppedCounterValue(t, "buffer_full")

	// Block the writer mid-flush so the buffer can fill: the writer pulls
	// the first event off the channel and stalls inside BatchInsertAuditLog
	// until we close `block`. With buffer=4 and one event in-flight that's
	// at most 5 events accepted; the 6th onwards is dropped.
	q := &fakeBatchQuerier{block: make(chan struct{})}
	w := NewWriter(q, nil,
		WithBatchSize(1), // flush every event so the in-flight event blocks immediately
		WithFlushInterval(10*time.Second),
		WithBufferSize(4),
	)
	w.Start(context.Background())
	t.Cleanup(func() {
		close(q.block)
		_ = w.Shutdown(context.Background())
	})

	// Push the first event and wait for the writer to pick it up — that
	// event is now stuck inside the (blocked) BatchInsertAuditLog call.
	if !w.Enqueue(makeRow(0)) {
		t.Fatal("first enqueue should not drop")
	}
	waitUntil(t, time.Second, func() bool {
		// The writer has pulled the event off the channel once Enqueue
		// can re-fill the channel up to its full capacity. We can't
		// observe the in-flight state directly, so we just give the
		// goroutine a beat to wake up.
		return true
	})
	// Brief pause to let the goroutine actually enter BatchInsertAuditLog
	// (where it blocks on `block`).
	time.Sleep(50 * time.Millisecond)

	// Fill the buffer (capacity=4).
	accepted := 0
	for i := 1; i <= 4; i++ {
		if w.Enqueue(makeRow(i)) {
			accepted++
		}
	}
	if accepted != 4 {
		t.Fatalf("expected 4 events to fit in the buffer, got %d", accepted)
	}

	// All subsequent events should drop.
	dropped := 0
	for i := 5; i < 15; i++ {
		if !w.Enqueue(makeRow(i)) {
			dropped++
		}
	}
	if dropped < 10 {
		t.Fatalf("expected at least 10 drops, got %d (writer drops counter = %d)", dropped, w.DropCount())
	}

	// The DropCount on the writer should match the drops we observed.
	if got := w.DropCount(); int(got) < dropped {
		t.Fatalf("DropCount = %d, want >= %d", got, dropped)
	}

	// And the Prometheus counter should have advanced by the same delta.
	after := auditDroppedCounterValue(t, "buffer_full")
	if after < before+float64(dropped) {
		t.Fatalf("dropped_total counter = %v, want >= %v", after, before+float64(dropped))
	}
}

// TestWriter_SyncFallback verifies that when no async Writer is installed
// (SetWriter never called, or explicitly cleared), Record(...) falls back
// to the synchronous CreateAuditLogV1 call through the supplied Querier.
// This is the test-and-bootstrap path; production wires a Writer in
// cmd/server/main.go.
func TestWriter_SyncFallback(t *testing.T) {
	// Defensive: ensure no leftover Writer from another test in this
	// package is still installed (the package-level var is shared).
	previous := getDefaultWriter()
	SetWriter(nil)
	t.Cleanup(func() { SetWriter(previous) })

	sync := &syncFakeQuerier{}
	Record(context.Background(), sync, Event{
		Action:       "fallback.test",
		ResourceType: "thing",
		RequestID:    "req-fallback",
	})

	if sync.calls != 1 {
		t.Fatalf("sync Querier.CreateAuditLogV1 calls = %d, want 1", sync.calls)
	}
	if sync.last.Action != "fallback.test" {
		t.Fatalf("Action = %q, want fallback.test", sync.last.Action)
	}
	if sync.last.CorrelationID != "req-fallback" {
		t.Fatalf("CorrelationID = %q, want req-fallback (default from RequestID)", sync.last.CorrelationID)
	}
}

// TestWriter_RecordUsesAsyncWriter verifies the happy path: when a Writer
// is installed, Record enqueues rather than touching the supplied Querier.
func TestWriter_RecordUsesAsyncWriter(t *testing.T) {
	q := &fakeBatchQuerier{}
	w := NewWriter(q, nil,
		WithBatchSize(2),
		WithFlushInterval(50*time.Millisecond),
		WithBufferSize(16),
	)
	w.Start(context.Background())

	previous := getDefaultWriter()
	SetWriter(w)
	t.Cleanup(func() {
		_ = w.Shutdown(context.Background())
		SetWriter(previous)
	})

	sync := &syncFakeQuerier{}
	Record(context.Background(), sync, Event{
		Action:       "async.path",
		ResourceType: "thing",
		RequestID:    "req-async",
	})

	if sync.calls != 0 {
		t.Fatalf("sync Querier should not be called when Writer is installed (calls=%d)", sync.calls)
	}

	waitUntil(t, time.Second, func() bool {
		return q.totalRows() >= 1
	})
}

// TestWriter_SyncFallbackOnInsertError is a sanity check that the sync
// path tolerates a DB error without panicking.
func TestWriter_SyncFallbackOnInsertError(t *testing.T) {
	previous := getDefaultWriter()
	SetWriter(nil)
	t.Cleanup(func() { SetWriter(previous) })

	sync := &syncFakeQuerier{err: errors.New("boom")}
	Record(context.Background(), sync, Event{
		Action:       "err.test",
		ResourceType: "thing",
	})

	if sync.calls != 1 {
		t.Fatalf("calls = %d, want 1", sync.calls)
	}
}

// syncFakeQuerier satisfies Querier for the sync-fallback tests.
type syncFakeQuerier struct {
	calls int
	last  sqlc.CreateAuditLogV1Params
	err   error
}

func (s *syncFakeQuerier) CreateAuditLogV1(_ context.Context, arg sqlc.CreateAuditLogV1Params) error {
	s.calls++
	s.last = arg
	return s.err
}

// auditDroppedCounterValue reads the current value of
// astronomer_audit_dropped_total for the given reason label.
func auditDroppedCounterValue(t *testing.T, reason string) float64 {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	want := map[string]string{
		"astronomer_instance_id": observability.InstanceID(),
		"reason":                 reason,
	}
	for _, family := range families {
		if family.GetName() != "astronomer_audit_dropped_total" {
			continue
		}
		for _, m := range family.GetMetric() {
			if labelsMatch(m.GetLabel(), want) && m.Counter != nil {
				return m.GetCounter().GetValue()
			}
		}
	}
	return 0
}

func labelsMatch(labels []*dto.LabelPair, want map[string]string) bool {
	if len(labels) != len(want) {
		return false
	}
	for _, l := range labels {
		if want[l.GetName()] != l.GetValue() {
			return false
		}
	}
	return true
}
