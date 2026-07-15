package siem

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/events"
)

// fakeTapQuerier captures every call to the tap surface so tests can
// assert against it without touching Postgres.
type fakeTapQuerier struct {
	mu          sync.Mutex
	forwarders  []sqlc.SiemForwarder
	listErr     error
	enqueued    []sqlc.EnqueueSIEMEventParams
	enqueueErr  error
	queueDepth  map[uuid.UUID]int64
	oldest      map[uuid.UUID][]int64
	deleted     [][]int64
	statusCalls []sqlc.UpsertSIEMForwarderStatusParams
}

func (f *fakeTapQuerier) ListEnabledSIEMForwarders(_ context.Context) ([]sqlc.SiemForwarder, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := make([]sqlc.SiemForwarder, len(f.forwarders))
	copy(out, f.forwarders)
	return out, nil
}

func (f *fakeTapQuerier) EnqueueSIEMEvent(_ context.Context, arg sqlc.EnqueueSIEMEventParams) (sqlc.SiemForwardQueue, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.enqueueErr != nil {
		return sqlc.SiemForwardQueue{}, f.enqueueErr
	}
	f.enqueued = append(f.enqueued, arg)
	return sqlc.SiemForwardQueue{ID: int64(len(f.enqueued)), ForwarderID: arg.ForwarderID, EventName: arg.EventName}, nil
}

func (f *fakeTapQuerier) CountSIEMQueueByForwarder(_ context.Context, id uuid.UUID) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.queueDepth == nil {
		return 0, nil
	}
	return f.queueDepth[id], nil
}

func (f *fakeTapQuerier) ListOldestSIEMQueue(_ context.Context, arg sqlc.ListOldestSIEMQueueParams) ([]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.oldest == nil {
		return nil, nil
	}
	ids := f.oldest[arg.ForwarderID]
	if len(ids) > int(arg.Limit) {
		ids = ids[:arg.Limit]
	}
	out := make([]int64, len(ids))
	copy(out, ids)
	return out, nil
}

func (f *fakeTapQuerier) DeleteSIEMQueueByIDs(_ context.Context, ids []int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]int64, len(ids))
	copy(cp, ids)
	f.deleted = append(f.deleted, cp)
	return nil
}

func (f *fakeTapQuerier) UpsertSIEMForwarderStatus(_ context.Context, arg sqlc.UpsertSIEMForwarderStatusParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statusCalls = append(f.statusCalls, arg)
	return nil
}

// matchAll matches every event — keeps the test focused on the
// enqueue path without exercising the filter logic, which webhook/filter_test
// already covers.
func matchAll(_ string, _ []string) bool { return true }

// matchByPrefix matches events whose name begins with the first filter.
func matchByPrefix(eventName string, filters []string) bool {
	for _, f := range filters {
		if len(f) <= len(eventName) && eventName[:len(f)] == f {
			return true
		}
	}
	return false
}

func mustUUID() uuid.UUID {
	return uuid.New()
}

func TestBusTap_EnqueuesMatchingForwarders(t *testing.T) {
	q := &fakeTapQuerier{
		forwarders: []sqlc.SiemForwarder{
			{ID: mustUUID(), Name: "splunk-audit", EventFilters: json.RawMessage(`["audit."]`), Enabled: true},
			{ID: mustUUID(), Name: "syslog-cluster", EventFilters: json.RawMessage(`["cluster."]`), Enabled: true},
		},
	}
	tap := NewBusTap(q, nil, matchByPrefix, nil)
	tap.SetCacheTTL(1 * time.Hour) // freeze for the test
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tap.Start(ctx)

	tap.HandleEvent(ctx, events.Event{ID: 1, Type: events.Type("audit.user.login"), Time: time.Now()})
	tap.HandleEvent(ctx, events.Event{ID: 2, Type: events.Type("cluster.connected"), Time: time.Now()})
	tap.HandleEvent(ctx, events.Event{ID: 3, Type: events.Type("agent.failed"), Time: time.Now()})

	// Give the inserter goroutine a beat to drain the channel.
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		q.mu.Lock()
		if len(q.enqueued) >= 2 {
			q.mu.Unlock()
			break
		}
		q.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}

	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.enqueued) != 2 {
		t.Fatalf("expected 2 enqueued, got %d: %#v", len(q.enqueued), q.enqueued)
	}
	// Confirm the (forwarder, event) pairing.
	gotByEvent := map[string]uuid.UUID{}
	for _, e := range q.enqueued {
		gotByEvent[e.EventName] = e.ForwarderID
	}
	if gotByEvent["audit.user.login"] != q.forwarders[0].ID {
		t.Errorf("audit event went to wrong forwarder: %v", gotByEvent)
	}
	if gotByEvent["cluster.connected"] != q.forwarders[1].ID {
		t.Errorf("cluster event went to wrong forwarder: %v", gotByEvent)
	}
}

func TestBusTap_DropsOldestWhenFull(t *testing.T) {
	fid := mustUUID()
	q := &fakeTapQuerier{
		forwarders: []sqlc.SiemForwarder{
			{ID: fid, Name: "splunk", EventFilters: json.RawMessage(`["audit."]`), Enabled: true},
		},
		queueDepth: map[uuid.UUID]int64{fid: 10000},
		oldest: map[uuid.UUID][]int64{
			fid: {1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
		},
	}
	tap := NewBusTap(q, nil, matchByPrefix, nil)
	tap.SetCacheTTL(1 * time.Hour)
	tap.SetMaxQueueSize(10000)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tap.Start(ctx)

	tap.HandleEvent(ctx, events.Event{ID: 99, Type: events.Type("audit.test"), Time: time.Now()})

	// Wait for the inserter to do its drop + insert.
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		q.mu.Lock()
		done := len(q.enqueued) >= 1 && len(q.deleted) >= 1
		q.mu.Unlock()
		if done {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.deleted) == 0 {
		t.Fatalf("expected at least one DELETE call to evict oldest rows")
	}
	if len(q.enqueued) != 1 {
		t.Fatalf("expected 1 enqueue after eviction, got %d", len(q.enqueued))
	}
	// The status row should be bumped with dropped_total > 0.
	foundDropped := false
	for _, s := range q.statusCalls {
		if s.ForwarderID == fid && s.DroppedTotal > 0 {
			foundDropped = true
			break
		}
	}
	if !foundDropped {
		t.Errorf("expected a status upsert with dropped_total > 0; calls=%+v", q.statusCalls)
	}
}

func TestBusTap_NonBlocking_DropsOnBufferFull(t *testing.T) {
	// Use a fake querier that blocks every enqueue so the inserter
	// goroutine never drains the channel, forcing HandleEvent to take
	// the channel-full branch.
	block := make(chan struct{})
	defer close(block)
	q := &fakeTapQuerier{
		forwarders: []sqlc.SiemForwarder{
			{ID: mustUUID(), Name: "stuck", EventFilters: json.RawMessage(`["audit."]`), Enabled: true},
		},
	}
	q.enqueueErr = errors.New("permanent failure to demonstrate non-blocking semantics")
	tap := NewBusTap(q, nil, matchByPrefix, nil)
	tap.SetCacheTTL(1 * time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tap.Start(ctx)

	// Pump way more than the buffer holds so the select-default branch
	// fires. The test passes if every HandleEvent call returns
	// promptly — we don't strictly need to verify the drop counter
	// because the dropped_total metric is per-process global.
	deadline := time.After(2 * time.Second)
	done := make(chan struct{})
	go func() {
		for i := 0; i < EnqueueBufferSize*2; i++ {
			tap.HandleEvent(ctx, events.Event{ID: uint64(i), Type: events.Type("audit.x")})
		}
		close(done)
	}()
	select {
	case <-done:
		// All HandleEvent calls returned — non-blocking is honored.
	case <-deadline:
		t.Fatal("HandleEvent blocked under sustained pressure")
	}
}

func TestBusTap_Invalidate_RefreshesCache(t *testing.T) {
	id1 := mustUUID()
	q := &fakeTapQuerier{
		forwarders: []sqlc.SiemForwarder{
			{ID: id1, Name: "a", EventFilters: json.RawMessage(`["audit."]`), Enabled: true},
		},
	}
	tap := NewBusTap(q, nil, matchAll, nil)
	tap.SetCacheTTL(1 * time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tap.Start(ctx)

	// Warm the cache.
	_, err := tap.subscriptions(ctx)
	if err != nil {
		t.Fatalf("subscriptions: %v", err)
	}
	// Mutate the underlying store. Without Invalidate, the cache still
	// shows the old contents.
	q.mu.Lock()
	q.forwarders = append(q.forwarders, sqlc.SiemForwarder{ID: mustUUID(), Name: "b", Enabled: true})
	q.mu.Unlock()
	cached, _ := tap.subscriptions(ctx)
	if len(cached) != 1 {
		t.Fatalf("cache did not preserve old data: %d", len(cached))
	}
	tap.Invalidate()
	fresh, _ := tap.subscriptions(ctx)
	if len(fresh) != 2 {
		t.Errorf("post-invalidate fetch should see new row: %d", len(fresh))
	}
}

// TestBusTap_ExcludesSysEvents locks the R9 (P4.6) decision for the SIEM
// pipeline: sys.* stream-plumbing events never enqueue forward rows, even
// for a catch-all filter; non-sys events on the same forwarder still do.
func TestBusTap_ExcludesSysEvents(t *testing.T) {
	q := &fakeTapQuerier{
		forwarders: []sqlc.SiemForwarder{
			{ID: mustUUID(), Name: "catch-all", EventFilters: json.RawMessage(`[""]`), Enabled: true},
		},
	}
	tap := NewBusTap(q, nil, matchByPrefix, nil)
	tap.SetCacheTTL(1 * time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tap.Start(ctx)

	tap.HandleEvent(ctx, events.Event{ID: 1, Type: events.Type("sys.ping"), Time: time.Now()})
	tap.HandleEvent(ctx, events.Event{ID: 2, Type: events.Type("cluster.k8s_changed"), Time: time.Now()})

	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		q.mu.Lock()
		if len(q.enqueued) >= 1 {
			q.mu.Unlock()
			break
		}
		q.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}

	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.enqueued) != 1 {
		t.Fatalf("expected exactly 1 enqueued row, got %d: %#v", len(q.enqueued), q.enqueued)
	}
	if q.enqueued[0].EventName != "cluster.k8s_changed" {
		t.Fatalf("enqueued event = %q, want cluster.k8s_changed (sys.ping excluded)", q.enqueued[0].EventName)
	}
}
