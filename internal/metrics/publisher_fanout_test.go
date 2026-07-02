package metrics

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/events"
	"github.com/alphabravocompany/astronomer-go/internal/handler/clustermetrics"
)

// fakeClusterQuerier serves a fixed cluster list on the first page.
type fakeClusterQuerier struct {
	clusters []sqlc.Cluster
}

func (q *fakeClusterQuerier) ListClusters(_ context.Context, arg sqlc.ListClustersParams) ([]sqlc.Cluster, error) {
	if arg.Offset > 0 {
		return nil, nil
	}
	return q.clusters, nil
}

func (q *fakeClusterQuerier) UpdateClusterStatus(context.Context, sqlc.UpdateClusterStatusParams) error {
	return nil
}

// fakeMetricsProvider records which clusters Get was called for and tracks the
// peak concurrent in-flight Get calls, so a test can prove the publisher fans
// out instead of fetching serially.
type fakeMetricsProvider struct {
	sleep time.Duration

	mu       sync.Mutex
	calledID map[string]int
	inFlight int32
	maxSeen  int32
}

func (p *fakeMetricsProvider) Get(_ context.Context, clusterID string, _ bool) clustermetrics.Snapshot {
	cur := atomic.AddInt32(&p.inFlight, 1)
	for {
		m := atomic.LoadInt32(&p.maxSeen)
		if cur <= m || atomic.CompareAndSwapInt32(&p.maxSeen, m, cur) {
			break
		}
	}
	p.mu.Lock()
	p.calledID[clusterID]++
	p.mu.Unlock()
	time.Sleep(p.sleep)
	atomic.AddInt32(&p.inFlight, -1)
	return clustermetrics.Snapshot{CPUPercentage: 1, MemoryPercentage: 2, PodCount: 3}
}

func activeCluster(fresh bool) sqlc.Cluster {
	hb := time.Now()
	if !fresh {
		hb = hb.Add(-5 * time.Minute) // well past the 2m staleness threshold
	}
	return sqlc.Cluster{
		ID:            uuid.New(),
		Status:        "active",
		IsLocal:       false,
		LastHeartbeat: pgtype.Timestamptz{Time: hb, Valid: true},
	}
}

func drainEvents(ch <-chan events.Event) int {
	n := 0
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return n
			}
			n++
		case <-time.After(200 * time.Millisecond):
			return n
		}
	}
}

// The publisher must fetch per-cluster snapshots in parallel: a serial loop paid
// the full per-cluster stall one at a time, so a stalled agent (or a partition
// stalling many) made a single pass take far longer than the cadence and the
// cache never stayed warm. Peak concurrent Get calls > 1 proves the fan-out.
func TestPublishMetrics_FansOutConcurrently(t *testing.T) {
	const n = 8
	clusters := make([]sqlc.Cluster, n)
	for i := range clusters {
		clusters[i] = activeCluster(true)
	}
	prov := &fakeMetricsProvider{sleep: 80 * time.Millisecond, calledID: map[string]int{}}
	bus := events.NewBus()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := bus.Subscribe(ctx)

	p := New(bus, &fakeClusterQuerier{clusters: clusters}, prov, nil)

	start := time.Now()
	p.publishMetrics(ctx)
	elapsed := time.Since(start)

	if prov.maxSeen < 2 {
		t.Fatalf("peak concurrent Get = %d, want > 1 (fan-out); a serial loop would show 1", prov.maxSeen)
	}
	// Serial would be n*80ms = 640ms; a bounded fan-out is a couple of windows.
	if elapsed >= time.Duration(n)*prov.sleep {
		t.Fatalf("publishMetrics took %s, expected well under serial %s", elapsed, time.Duration(n)*prov.sleep)
	}
	if got := drainEvents(ch); got != n {
		t.Fatalf("published %d metrics events, want %d", got, n)
	}
}

// A remote agent whose heartbeat is already stale must NOT cost a 4s snapshot
// round-trip: the provider is skipped for it (a zero snapshot is published
// instead), so one dead agent can't slow the whole pass.
func TestPublishMetrics_SkipsStaleHeartbeatAgents(t *testing.T) {
	fresh1 := activeCluster(true)
	fresh2 := activeCluster(true)
	stale := activeCluster(false)
	clusters := []sqlc.Cluster{fresh1, stale, fresh2}

	prov := &fakeMetricsProvider{sleep: 0, calledID: map[string]int{}}
	bus := events.NewBus()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := bus.Subscribe(ctx)

	p := New(bus, &fakeClusterQuerier{clusters: clusters}, prov, nil)
	p.publishMetrics(ctx)

	prov.mu.Lock()
	_, staleCalled := prov.calledID[stale.ID.String()]
	freshCalls := len(prov.calledID)
	prov.mu.Unlock()

	if staleCalled {
		t.Fatal("provider.Get was called for a stale-heartbeat cluster; it should be skipped to avoid the 4s stall")
	}
	if freshCalls != 2 {
		t.Fatalf("provider.Get called for %d clusters, want 2 (only the fresh ones)", freshCalls)
	}
	// All three clusters still emit an event (the stale one gets zeros).
	if got := drainEvents(ch); got != 3 {
		t.Fatalf("published %d metrics events, want 3 (stale cluster still emits a zero event)", got)
	}
}
