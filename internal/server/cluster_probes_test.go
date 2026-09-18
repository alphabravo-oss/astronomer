package server

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

type probeQuerierFake struct {
	mu       sync.Mutex
	clusters []uuid.UUID
	cursors  []uuid.UUID
	upserts  int
}

func (q *probeQuerierFake) ListClusterProbeTargets(_ context.Context, arg sqlc.ListClusterProbeTargetsParams) ([]uuid.UUID, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.cursors = append(q.cursors, arg.AfterID)
	var page []uuid.UUID
	for _, id := range q.clusters {
		if bytes.Compare(id[:], arg.AfterID[:]) > 0 {
			page = append(page, id)
			if len(page) == int(arg.PageSize) {
				break
			}
		}
	}
	return page, nil
}

func (q *probeQuerierFake) UpsertClusterCondition(_ context.Context, _ sqlc.UpsertClusterConditionParams) (sqlc.ClusterCondition, error) {
	q.mu.Lock()
	q.upserts++
	q.mu.Unlock()
	return sqlc.ClusterCondition{}, nil
}

type probeRequesterFake struct {
	calls   atomic.Int32
	current atomic.Int32
	peak    atomic.Int32
	delay   time.Duration
}

func (r *probeRequesterFake) Do(ctx context.Context, _ string, _ string, _ string, _ []byte, _ map[string]string) (*protocol.K8sResponsePayload, error) {
	r.calls.Add(1)
	current := r.current.Add(1)
	for {
		peak := r.peak.Load()
		if current <= peak || r.peak.CompareAndSwap(peak, current) {
			break
		}
	}
	defer r.current.Add(-1)
	if r.delay > 0 {
		timer := time.NewTimer(r.delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return &protocol.K8sResponsePayload{StatusCode: http.StatusOK}, nil
}

func TestSweepClusterProbesPagesEntireFleet(t *testing.T) {
	q := &probeQuerierFake{clusters: probeClusterIDs(int(clusterProbePageSize) + 1)}
	var cursor uuid.UUID
	if err := sweepClusterProbes(context.Background(), slog.Default(), q, &probeRequesterFake{}, &cursor); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.cursors) != 2 || q.cursors[0] != uuid.Nil || q.cursors[1] != q.clusters[499] {
		t.Fatalf("page cursors = %v, want zero then 500th identity", q.cursors)
	}
	if cursor != uuid.Nil {
		t.Fatal("completed sweep must wrap to the beginning")
	}
	if q.upserts != len(q.clusters)*2 {
		t.Fatalf("upserts = %d", q.upserts)
	}
}

func probeClusterIDs(count int) []uuid.UUID {
	ids := make([]uuid.UUID, count)
	for i := range ids {
		binary.BigEndian.PutUint64(ids[i][8:], uint64(i+1))
	}
	return ids
}

func TestFanOutClusterProbesIsBounded(t *testing.T) {
	clusters := probeClusterIDs(clusterProbeConcurrency * 2)
	q := &probeQuerierFake{}
	r := &probeRequesterFake{delay: 10 * time.Millisecond}
	var cursor uuid.UUID
	if err := fanOutClusterProbes(context.Background(), slog.Default(), q, r, clusters, &cursor); err != nil {
		t.Fatalf("fan out: %v", err)
	}
	if got, want := r.calls.Load(), int32(len(clusters)*2); got != want {
		t.Fatalf("probe calls = %d, want %d", got, want)
	}
	if peak := r.peak.Load(); peak <= 1 || peak > clusterProbeConcurrency {
		t.Fatalf("peak concurrency = %d, want 2..%d", peak, clusterProbeConcurrency)
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.upserts != len(clusters)*2 {
		t.Fatalf("condition upserts = %d, want %d", q.upserts, len(clusters)*2)
	}
}

// All probes block until the simulated tick deadline. Expire it only after
// every worker starts, so the fairness assertion is independent of timing.
type slowProbeRequester struct {
	mu      sync.Mutex
	seen    map[string]bool
	started chan struct{}
}

func (r *slowProbeRequester) Do(ctx context.Context, id, _, _ string, _ []byte, _ map[string]string) (*protocol.K8sResponsePayload, error) {
	r.mu.Lock()
	r.seen[id] = true
	r.mu.Unlock()
	r.started <- struct{}{}
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestClusterProbesResumeAfterSlowTickBeyondFirstPage(t *testing.T) {
	const total = 544 // Seventeen worker batches, crossing the 500-row page.
	q := &probeQuerierFake{clusters: probeClusterIDs(total)}
	r := &slowProbeRequester{seen: map[string]bool{}, started: make(chan struct{}, clusterProbeConcurrency)}
	var cursor uuid.UUID
	for tick := 0; tick < total/clusterProbeConcurrency; tick++ {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- sweepClusterProbes(ctx, slog.Default(), q, r, &cursor) }()
		for range clusterProbeConcurrency {
			select {
			case <-r.started:
			case <-time.After(5 * time.Second):
				cancel()
				t.Fatal("probe workers did not start")
			}
		}
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("tick error = %v", err)
		}
		if cursor != q.clusters[(tick+1)*clusterProbeConcurrency-1] {
			t.Fatalf("tick %d failed to resume: %s", tick, cursor)
		}
	}
	if len(r.seen) != total {
		t.Fatalf("visited %d/%d clusters", len(r.seen), total)
	}
	if err := sweepClusterProbes(context.Background(), slog.Default(), q, &probeRequesterFake{}, &cursor); err != nil {
		t.Fatal(err)
	}
	if cursor != uuid.Nil {
		t.Fatal("exhausted cursor must wrap for next tick")
	}
}
