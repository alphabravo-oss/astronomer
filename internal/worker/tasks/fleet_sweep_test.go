package tasks

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

func makeClusters(n int) []sqlc.Cluster {
	out := make([]sqlc.Cluster, n)
	for i := range out {
		out[i] = sqlc.Cluster{ID: uuid.New(), Status: "active"}
	}
	return out
}

// TestFanOutClusters_BoundedConcurrency proves the fan-out (a) runs clusters in
// parallel rather than serially and (b) never exceeds fleetSweepConcurrency
// simultaneous in-flight per-cluster calls. Without the F6 fix the sweeps ran
// strictly serially (peak concurrency 1); an unbounded fan-out would blow past
// the pool/tunnel budget.
func TestFanOutClusters_BoundedConcurrency(t *testing.T) {
	const n = fleetSweepConcurrency * 4
	clusters := makeClusters(n)

	var inflight, maxInflight, processed int32
	release := make(chan struct{})

	go func() {
		fanOutClusters(context.Background(), clusters, time.Minute, func(_ context.Context, _ sqlc.Cluster) {
			cur := atomic.AddInt32(&inflight, 1)
			for {
				old := atomic.LoadInt32(&maxInflight)
				if cur <= old || atomic.CompareAndSwapInt32(&maxInflight, old, cur) {
					break
				}
			}
			<-release // hold the slot so the cap can be observed
			atomic.AddInt32(&inflight, -1)
			atomic.AddInt32(&processed, 1)
		})
	}()

	// Wait until the limiter has saturated at exactly the cap.
	deadline := time.After(2 * time.Second)
	for atomic.LoadInt32(&maxInflight) < fleetSweepConcurrency {
		select {
		case <-deadline:
			t.Fatalf("only reached %d concurrent, want %d (fan-out not parallel enough)",
				atomic.LoadInt32(&maxInflight), fleetSweepConcurrency)
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if got := atomic.LoadInt32(&inflight); got > fleetSweepConcurrency {
		t.Fatalf("observed %d concurrent, must never exceed cap %d", got, fleetSweepConcurrency)
	}
	close(release) // let every wave drain

	// The batch must fully complete (all n processed) and never breach the cap.
	waited := time.After(3 * time.Second)
	for atomic.LoadInt32(&processed) < int32(n) {
		select {
		case <-waited:
			t.Fatalf("processed %d/%d clusters", atomic.LoadInt32(&processed), n)
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if got := atomic.LoadInt32(&maxInflight); got != fleetSweepConcurrency {
		t.Fatalf("peak concurrency = %d, want exactly %d", got, fleetSweepConcurrency)
	}
}

// TestFanOutClusters_PerClusterTimeout proves one slow cluster's per-cluster
// context is cancelled at the per-cluster timeout so it cannot hold the slot
// (and the tick) open indefinitely — the F6 "one slow agent stalls the sweep"
// failure mode.
func TestFanOutClusters_PerClusterTimeout(t *testing.T) {
	clusters := makeClusters(1)
	var timedOut int32

	start := time.Now()
	fanOutClusters(context.Background(), clusters, 40*time.Millisecond, func(ctx context.Context, _ sqlc.Cluster) {
		<-ctx.Done() // simulate a hung tunnel/DB call
		if ctx.Err() == context.DeadlineExceeded {
			atomic.StoreInt32(&timedOut, 1)
		}
	})
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("fan-out took %s, per-cluster timeout should have released it near 40ms", elapsed)
	}
	if atomic.LoadInt32(&timedOut) != 1 {
		t.Fatal("slow cluster's context was not cancelled by the per-cluster timeout")
	}
}

// TestFanOutClusters_AggregateDeadline proves the whole sweep is abandoned when
// the aggregate deadline fires: in-flight clusters are cancelled and the
// remaining fleet is skipped rather than the sweep running for many minutes.
func TestFanOutClusters_AggregateDeadline(t *testing.T) {
	const n = 200
	clusters := makeClusters(n)
	var started int32

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()

	start := time.Now()
	fanOutClusters(ctx, clusters, 10*time.Second, func(cctx context.Context, _ sqlc.Cluster) {
		atomic.AddInt32(&started, 1)
		<-cctx.Done() // block until the aggregate deadline cancels us
	})
	elapsed := time.Since(start)
	if elapsed > 3*time.Second {
		t.Fatalf("fan-out ran %s; aggregate deadline should have abandoned it near 60ms", elapsed)
	}
	if got := atomic.LoadInt32(&started); got >= int32(n) {
		t.Fatalf("started %d/%d clusters; the aggregate deadline should have skipped the tail", got, n)
	}
}
