package audit

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// Default batching parameters. Chosen for the typical 1-50 mutating
// req/s steady state of the dashboard. At 50 req/s a 50-row batch
// fires roughly every 1s, well below the 250ms ticker — meaning the
// ticker path dominates in steady state (low coalescing) and the
// size threshold dominates only under load spikes.
const (
	defaultBufferSize    = 1024
	defaultBatchSize     = 50
	defaultFlushInterval = 250 * time.Millisecond
)

// BatchQuerier is the database surface the async Writer needs. Implemented by
// *sqlc.Queries.
type BatchQuerier interface {
	BatchInsertAuditLog(ctx context.Context, rows []sqlc.CreateAuditLogV1Params) error
}

// Writer is the async batched audit-log writer. It owns a single bounded
// channel of pending events and a single background goroutine that drains
// the channel into multi-row INSERTs.
//
// Crash-safety: events live in an in-process channel; if the process exits
// uncleanly between Enqueue and the next flush, those events are LOST.
// The trade-off vs the per-request synchronous insert it replaces is:
//
//   - per-request latency: one DB round-trip removed from every mutating
//     handler's critical path.
//   - delivery window: at-most-once within ~250 ms (the flush interval) or
//     ~50 events (the batch size), whichever fires first.
//   - shutdown drain: when the lifecycle ctx cancels, the writer flushes
//     its current batch before returning. The server's 10 s graceful
//     shutdown ctx bounds this.
//   - sustained overload: when the buffer is full, events are DROPPED
//     and counted via the astronomer_audit_dropped_total counter; the
//     first drop is logged in full and every 1000th drop thereafter
//     to avoid log spam.
//
// This is a deliberate weakening of audit durability — auditing what an
// HTTP request did is valuable, but blocking the request on an extra DB
// round-trip just to record the fact is not. Operators who need stronger
// durability should either turn the writer off (Record falls back to the
// per-request sync insert) or shrink the flush interval/batch size.
type Writer struct {
	q   BatchQuerier
	log *slog.Logger

	bufferSize    int
	batchSize     int
	flushInterval time.Duration

	in chan sqlc.CreateAuditLogV1Params

	// drops is the total drop count across the process lifetime. Used for
	// the "log every 1000th drop" throttle. Atomic so Enqueue stays
	// lock-free.
	drops atomic.Uint64

	// startOnce / stopOnce keep Start and Shutdown idempotent.
	startOnce sync.Once
	stopOnce  sync.Once

	// done closes when the writer goroutine has exited (after the final
	// flush). Shutdown blocks on this.
	done chan struct{}

	// started is true once Start has been called.
	started atomic.Bool
}

// WriterOption tunes a Writer.
type WriterOption func(*Writer)

// WithBufferSize overrides the default 1024-event buffer capacity.
func WithBufferSize(n int) WriterOption {
	return func(w *Writer) {
		if n > 0 {
			w.bufferSize = n
		}
	}
}

// WithBatchSize overrides the default 50-row batch threshold.
func WithBatchSize(n int) WriterOption {
	return func(w *Writer) {
		if n > 0 {
			w.batchSize = n
		}
	}
}

// WithFlushInterval overrides the default 250ms flush ticker.
func WithFlushInterval(d time.Duration) WriterOption {
	return func(w *Writer) {
		if d > 0 {
			w.flushInterval = d
		}
	}
}

// NewWriter constructs an unstarted Writer. Call Start(ctx) to launch the
// background goroutine; until then Enqueue still buffers up to bufferSize
// events (so early-boot Record() calls don't lose data).
func NewWriter(q BatchQuerier, log *slog.Logger, opts ...WriterOption) *Writer {
	if log == nil {
		log = slog.Default()
	}
	w := &Writer{
		q:             q,
		log:           log,
		bufferSize:    defaultBufferSize,
		batchSize:     defaultBatchSize,
		flushInterval: defaultFlushInterval,
		done:          make(chan struct{}),
	}
	for _, opt := range opts {
		opt(w)
	}
	w.in = make(chan sqlc.CreateAuditLogV1Params, w.bufferSize)
	return w
}

// Start launches the background drain goroutine. The goroutine runs until
// ctx is cancelled or Shutdown is called, then performs one final flush
// before signalling done.
//
// Start is idempotent — subsequent calls are no-ops.
func (w *Writer) Start(ctx context.Context) {
	w.startOnce.Do(func() {
		w.started.Store(true)
		go w.run(ctx)
	})
}

// Enqueue submits an event for asynchronous insertion. Returns true if the
// event was enqueued and false if the buffer was full (in which case the
// drop counter has been incremented and the drop may have been logged).
//
// Enqueue never blocks the caller.
func (w *Writer) Enqueue(row sqlc.CreateAuditLogV1Params) bool {
	if w == nil || w.in == nil {
		return false
	}
	select {
	case w.in <- row:
		return true
	default:
		w.onDrop(row)
		return false
	}
}

// onDrop records a drop in metrics and emits a throttled log line. We log
// the first drop in full and every 1000th drop thereafter; the metric
// captures the rest.
func (w *Writer) onDrop(row sqlc.CreateAuditLogV1Params) {
	recordDropped("buffer_full")
	n := w.drops.Add(1)
	if n == 1 || n%1000 == 0 {
		w.log.Warn("audit writer dropped event",
			"event", "audit_dropped",
			"drops_total", n,
			"reason", "buffer_full",
			"source", row.Source,
			"action", row.Action,
			"resource_type", row.ResourceType,
			"resource_id", row.ResourceID,
			"correlation_id", row.CorrelationID,
		)
	}
}

// DropCount returns the running total of dropped events. Useful for tests.
func (w *Writer) DropCount() uint64 {
	if w == nil {
		return 0
	}
	return w.drops.Load()
}

// Shutdown signals the background goroutine to drain and exit, then blocks
// up to ctx's deadline for the final flush to complete. Returns ctx.Err()
// if the deadline fired before the drain completed.
//
// Safe to call from a defer in main(). Idempotent.
func (w *Writer) Shutdown(ctx context.Context) error {
	if w == nil {
		return nil
	}
	w.stopOnce.Do(func() {
		// Closing the channel tells the goroutine "no more events". The
		// goroutine then drains whatever is buffered and exits.
		close(w.in)
	})
	if !w.started.Load() {
		// Never started; nothing to wait for.
		return nil
	}
	select {
	case <-w.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// run is the main drain loop. It coalesces events into batches and flushes
// when either:
//   - the batch reaches batchSize rows, OR
//   - the flush ticker fires (every flushInterval), OR
//   - the input channel is closed (final flush at shutdown).
//
// ctx cancellation is honoured the same way as channel close: we drain
// remaining events from the channel and flush before returning.
func (w *Writer) run(ctx context.Context) {
	defer close(w.done)

	ticker := time.NewTicker(w.flushInterval)
	defer ticker.Stop()

	batch := make([]sqlc.CreateAuditLogV1Params, 0, w.batchSize)

	flush := func(reason string) {
		if len(batch) == 0 {
			return
		}
		w.flushBatch(batch, reason)
		// Reset to a fresh slice so the underlying array can be GC'd
		// quickly when the batch was large.
		batch = make([]sqlc.CreateAuditLogV1Params, 0, w.batchSize)
	}

	for {
		select {
		case <-ctx.Done():
			// Drain remaining events without blocking — anything that
			// arrives after this point will hit the closed-channel
			// branch below.
			w.drainAndFlush(batch, "ctx_cancelled")
			return
		case row, ok := <-w.in:
			if !ok {
				flush("channel_closed")
				return
			}
			batch = append(batch, row)
			if len(batch) >= w.batchSize {
				flush("size_threshold")
			}
		case <-ticker.C:
			flush("interval")
		}
	}
}

// drainAndFlush is used on ctx cancellation: pull whatever is still in the
// channel without blocking, then flush. Bounded by the channel capacity so
// it can't loop forever even if a producer is hammering Enqueue.
func (w *Writer) drainAndFlush(batch []sqlc.CreateAuditLogV1Params, reason string) {
	// Best-effort drain. We cap iterations at bufferSize+batchSize to bound
	// the work; in practice the producer-side select-default ensures no
	// new events arrive once we've started draining.
	limit := w.bufferSize + w.batchSize
	for i := 0; i < limit; i++ {
		select {
		case row, ok := <-w.in:
			if !ok {
				goto flush
			}
			batch = append(batch, row)
		default:
			goto flush
		}
	}
flush:
	if len(batch) > 0 {
		w.flushBatch(batch, reason)
	}
}

// flushBatch issues a single multi-row INSERT for the given batch. On
// error it logs and increments the error counter; we do NOT retry — the
// caller cannot block on a retry storm, and replaying audit events out of
// order would corrupt the timeline anyway.
func (w *Writer) flushBatch(batch []sqlc.CreateAuditLogV1Params, reason string) {
	if w.q == nil || len(batch) == 0 {
		return
	}
	// Use a fresh context bounded by a generous timeout. We DON'T inherit
	// the request ctx because by the time we flush, the originating
	// request has long since returned its response.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := w.q.BatchInsertAuditLog(ctx, batch); err != nil {
		recordBatchInsert("error", len(batch))
		w.log.Warn("audit writer batch insert failed",
			"event", "audit_batch_insert_failed",
			"batch_size", len(batch),
			"flush_reason", reason,
			"error", err,
		)
		return
	}
	recordBatchInsert("ok", len(batch))
}

// Package-level "default" writer. Record() consults this to decide between
// the async enqueue path and the synchronous fallback. Set via SetWriter
// at server boot; tests can leave it nil to exercise the sync path.
var (
	defaultWriterMu sync.RWMutex
	defaultWriter   *Writer
)

// SetWriter installs the package-level Writer used by Record(...) when no
// async writer is passed in. Pass nil to clear (tests rely on this).
func SetWriter(w *Writer) {
	defaultWriterMu.Lock()
	defer defaultWriterMu.Unlock()
	defaultWriter = w
}

// getDefaultWriter returns the package-level Writer, or nil if unset.
func getDefaultWriter() *Writer {
	defaultWriterMu.RLock()
	defer defaultWriterMu.RUnlock()
	return defaultWriter
}
