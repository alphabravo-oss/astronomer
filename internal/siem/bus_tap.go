package siem

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/events"
)

// DefaultMaxQueueSize is the per-forwarder bounded-queue cap. Chart-
// tunable via the BusTap.SetMaxQueueSize hook the server wires from a
// platform_settings row when present. 10K is enough to absorb a
// minute-scale outage at most realistic stack event rates without
// running siem_forward_queue to multi-million rows.
const DefaultMaxQueueSize = 10000

// SubscriptionCacheTTL is how long the tap caches the enabled-forwarders
// list before refetching. Short enough that an operator's "enable +
// trigger an event" interaction feels live; long enough that a busy
// stack (thousands of events per minute) doesn't hammer the SELECT.
const SubscriptionCacheTTL = 15 * time.Second

// EnqueueBufferSize is the size of the in-memory channel between the
// bus subscriber goroutine and the DB INSERTer. The channel lets the
// HandleEvent hot path (which the bus calls from the publishing
// goroutine) return immediately without waiting on Postgres — that's
// the constraint "DON'T block the event-firing goroutine" from the
// sprint brief. When the channel is full we drop the newest event +
// bump dropped_total{reason=tap_buffer_full}.
const EnqueueBufferSize = 2048

// TapQuerier is the database surface the bus tap needs.
type TapQuerier interface {
	ListEnabledSIEMForwarders(ctx context.Context) ([]sqlc.SiemForwarder, error)
	EnqueueSIEMEvent(ctx context.Context, arg sqlc.EnqueueSIEMEventParams) (sqlc.SiemForwardQueue, error)
	CountSIEMQueueByForwarder(ctx context.Context, forwarderID uuid.UUID) (int64, error)
	ListOldestSIEMQueue(ctx context.Context, arg sqlc.ListOldestSIEMQueueParams) ([]int64, error)
	DeleteSIEMQueueByIDs(ctx context.Context, ids []int64) error
	UpsertSIEMForwarderStatus(ctx context.Context, arg sqlc.UpsertSIEMForwarderStatusParams) error
}

// FilterMatcher is the glob-matching dependency the tap uses on each
// event. Wired by the server to webhook.MatchFilters so both pipelines
// share one matcher implementation.
type FilterMatcher func(eventName string, filters []string) bool

// BusTap subscribes to an *events.Bus and inserts siem_forward_queue
// rows for every event that matches at least one enabled forwarder's
// filter list. The tap is a singleton per process — Start launches a
// background goroutine that consumes the bus channel until ctx cancels.
type BusTap struct {
	q        TapQuerier
	bus      *events.Bus
	log      *slog.Logger
	matcher  FilterMatcher
	maxQueue int

	mu       sync.Mutex
	cache    []sqlc.SiemForwarder
	cachedAt time.Time
	cacheTTL time.Duration

	enqueue chan enqueueJob
	// dropLogCount throttles the "queue full" warn log. We emit the
	// first drop and then every 1000th drop so log volume stays sane
	// during sustained pressure.
	dropLogCount atomic.Uint64
}

// enqueueJob is the in-memory hand-off between the bus subscriber
// (synchronous on the publisher's goroutine) and the DB INSERTer
// (separate goroutine started by Start).
type enqueueJob struct {
	forwarderID uuid.UUID
	forwarder   sqlc.SiemForwarder
	eventName   string
	payload     []byte
	severity    string
}

// NewBusTap wires the dependencies. matcher is required (webhook.MatchFilters
// or a test fake); the other dependencies are optional with sensible
// defaults.
func NewBusTap(q TapQuerier, bus *events.Bus, matcher FilterMatcher, log *slog.Logger) *BusTap {
	if log == nil {
		log = slog.Default()
	}
	return &BusTap{
		q:        q,
		bus:      bus,
		log:      log,
		matcher:  matcher,
		maxQueue: DefaultMaxQueueSize,
		cacheTTL: SubscriptionCacheTTL,
		enqueue:  make(chan enqueueJob, EnqueueBufferSize),
	}
}

// SetMaxQueueSize is the chart-tunable cap hook. 0 leaves the default.
func (t *BusTap) SetMaxQueueSize(n int) {
	if n > 0 {
		t.maxQueue = n
	}
}

// SetCacheTTL is the test seam.
func (t *BusTap) SetCacheTTL(d time.Duration) {
	if d > 0 {
		t.cacheTTL = d
	}
}

// Invalidate drops the cached enabled-forwarders list. The admin
// handler calls this on CREATE/UPDATE/DELETE so config changes are
// reflected on the very next event.
func (t *BusTap) Invalidate() {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.cache = nil
	t.cachedAt = time.Time{}
	t.mu.Unlock()
}

// Start launches the bus-consumer + the DB-insert worker goroutine.
// The goroutines run until ctx cancels — the server lifecycle ctx is
// the canonical caller.
func (t *BusTap) Start(ctx context.Context) {
	if t == nil || t.q == nil {
		return
	}
	if t.bus != nil {
		ch := t.bus.Subscribe(ctx)
		go t.runBus(ctx, ch)
	}
	go t.runInserter(ctx)
}

// HandleEvent is the per-event hot path. Public so tests can drive the
// tap without spinning up a real bus. MUST NOT block the caller — every
// branch returns promptly even when the channel is full.
func (t *BusTap) HandleEvent(ctx context.Context, ev events.Event) {
	// CORR-R02: only the publishing pod enqueues SIEM deliveries.
	if ev.Remote {
		return
	}
	subs, err := t.subscriptions(ctx)
	if err != nil {
		t.log.WarnContext(ctx, "siem tap: list forwarders failed",
			"event", ev.Type, "error", err)
		return
	}
	if len(subs) == 0 || t.matcher == nil {
		return
	}
	eventName := string(ev.Type)
	payload, err := json.Marshal(eventEnvelope{
		EventName: eventName,
		EventID:   fmt.Sprintf("%d", ev.ID),
		Timestamp: ev.Time,
		Detail:    events.RawJSON(ev.Data),
	})
	if err != nil {
		t.log.WarnContext(ctx, "siem tap: marshal event failed",
			"event", eventName, "error", err)
		return
	}
	for _, sub := range subs {
		filters, ok := events.DecodeFilterGlobs(sub.EventFilters)
		if !ok {
			t.log.WarnContext(ctx, "siem tap: malformed event_filters; skipping",
				"forwarder_id", sub.ID.String(), "name", sub.Name)
			continue
		}
		if !t.matcher(eventName, filters) {
			continue
		}
		// Best-effort hand-off. If the channel is full the event is
		// dropped — we'd rather lose one event than block the audit
		// hot path.
		select {
		case t.enqueue <- enqueueJob{
			forwarderID: sub.ID,
			forwarder:   sub,
			eventName:   eventName,
			payload:     payload,
			severity:    severityForEvent(eventName, ev.Data),
		}:
		default:
			RecordDropped(sub.Name, "tap_buffer_full", 1)
			t.maybeLogDrop(sub.Name, "tap_buffer_full")
		}
	}
}

// runBus consumes the bus channel and hands events to HandleEvent.
func (t *BusTap) runBus(ctx context.Context, ch <-chan events.Event) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			t.HandleEvent(ctx, ev)
		}
	}
}

// runInserter drains the in-memory channel into Postgres. One goroutine
// keeps the DB write-side serial per process, which keeps the
// max_queue_size enforcement simple (no read-modify-write races).
func (t *BusTap) runInserter(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case job, ok := <-t.enqueue:
			if !ok {
				return
			}
			t.insertOne(ctx, job)
		}
	}
}

// insertOne enforces the bounded-queue cap and INSERTs.
func (t *BusTap) insertOne(ctx context.Context, job enqueueJob) {
	// Cap-check first. CountSIEMQueueByForwarder is cheap thanks to
	// the forwarder_id partial index.
	depth, err := t.q.CountSIEMQueueByForwarder(ctx, job.forwarderID)
	if err == nil && int(depth) >= t.maxQueue {
		// Drop oldest rows to make headroom for the new event plus a
		// 10% slack so we don't bounce on every insert.
		toDrop := int(depth) - t.maxQueue + 1 + (t.maxQueue / 10)
		if toDrop > 1000 {
			toDrop = 1000
		}
		ids, lerr := t.q.ListOldestSIEMQueue(ctx, sqlc.ListOldestSIEMQueueParams{
			ForwarderID: job.forwarderID,
			Limit:       int32(toDrop),
		})
		if lerr == nil && len(ids) > 0 {
			if derr := t.q.DeleteSIEMQueueByIDs(ctx, ids); derr == nil {
				RecordDropped(job.forwarder.Name, "queue_full", len(ids))
				t.maybeLogDrop(job.forwarder.Name, "queue_full")
				// Bump dropped_total on the status row so the admin
				// status endpoint surfaces it without a metrics
				// scrape.
				_ = t.q.UpsertSIEMForwarderStatus(ctx, sqlc.UpsertSIEMForwarderStatusParams{
					ForwarderID:  job.forwarderID,
					LastError:    "queue full; oldest rows dropped",
					QueueDepth:   int32(t.maxQueue),
					DroppedTotal: int64(len(ids)),
				})
			}
		}
	}
	if _, err := t.q.EnqueueSIEMEvent(ctx, sqlc.EnqueueSIEMEventParams{
		ForwarderID: job.forwarderID,
		EventName:   job.eventName,
		Payload:     job.payload,
		Severity:    job.severity,
	}); err != nil {
		t.log.WarnContext(ctx, "siem tap: enqueue failed",
			"forwarder_id", job.forwarderID.String(), "event", job.eventName, "error", err)
	}
}

// subscriptions returns the cached enabled-forwarders list, refetching
// when older than cacheTTL.
func (t *BusTap) subscriptions(ctx context.Context) ([]sqlc.SiemForwarder, error) {
	t.mu.Lock()
	if t.cache != nil && time.Since(t.cachedAt) < t.cacheTTL {
		out := t.cache
		t.mu.Unlock()
		return out, nil
	}
	t.mu.Unlock()

	fresh, err := t.q.ListEnabledSIEMForwarders(ctx)
	if err != nil {
		return nil, err
	}
	t.mu.Lock()
	t.cache = fresh
	t.cachedAt = time.Now()
	t.mu.Unlock()
	return fresh, nil
}

// maybeLogDrop emits a warn log on the first drop and every 1000th
// drop thereafter. Throttles a hot log on sustained pressure.
func (t *BusTap) maybeLogDrop(forwarder, reason string) {
	n := t.dropLogCount.Add(1)
	if n == 1 || n%1000 == 0 {
		t.log.Warn("siem tap: dropped event",
			"forwarder", forwarder, "reason", reason, "drops_seen", n)
	}
}

// severityForEvent infers a syslog-style severity from the event name +
// payload. The mapping is conservative: anything matching "*.failed" /
// "*.error" / "*.deleted" climbs to "warn", security events climb to
// "err", and the audit.* default is "notice".
func severityForEvent(eventName string, _ any) string {
	switch {
	case eventName == "":
		return "info"
	case stringContainsAny(eventName, []string{".failed", ".error", ".rejected"}):
		return "err"
	case stringContainsAny(eventName, []string{".deleted", ".disconnected", ".disabled"}):
		return "warn"
	case stringHasPrefix(eventName, "audit."):
		return "notice"
	default:
		return "info"
	}
}

func stringContainsAny(s string, needles []string) bool {
	for _, n := range needles {
		if n != "" && stringContains(s, n) {
			return true
		}
	}
	return false
}

func stringContains(s, sub string) bool {
	return len(sub) > 0 && len(s) >= len(sub) && stringIndex(s, sub) >= 0
}

func stringIndex(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func stringHasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

// eventEnvelope is the JSON shape the tap persists onto
// siem_forward_queue.payload. The dispatcher rehydrates this into a
// SIEMEvent before formatting.
type eventEnvelope struct {
	EventName string          `json:"event_name"`
	EventID   string          `json:"event_id"`
	Timestamp time.Time       `json:"timestamp"`
	Detail    json.RawMessage `json:"detail,omitempty"`
}
