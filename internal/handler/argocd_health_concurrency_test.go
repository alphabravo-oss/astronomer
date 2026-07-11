package handler

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// reconcileInstanceHealth previously probed every instance serially on the
// single reconciler goroutine, so N unreachable instances stalled the loop for
// N×timeout. After the fix each instance's probe is dispatched through the
// bounded pool outside the lock.
//
// This proves the fan-out DETERMINISTICALLY, not by wall-clock timing (which is
// flaky under CPU contention): each probe handler blocks until `barrier` probes
// are concurrently in flight. A serial loop can never get a second probe in
// flight, so the barrier is never reached and reconcileInstanceHealth never
// returns — caught by the watchdog. The fanned-out version reaches the barrier
// immediately and completes fast.
func TestReconcileInstanceHealth_FansOutConcurrently(t *testing.T) {
	const n = 8
	const barrier = 4 // require at least this many probes concurrently in flight

	var inFlight, maxSeen int32
	release := make(chan struct{})
	var releaseOnce sync.Once

	h, rec, srv := newArgoCDFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		cur := atomic.AddInt32(&inFlight, 1)
		for { // track the peak concurrency observed
			m := atomic.LoadInt32(&maxSeen)
			if cur <= m || atomic.CompareAndSwapInt32(&maxSeen, m, cur) {
				break
			}
		}
		if cur >= barrier {
			releaseOnce.Do(func() { close(release) })
		}
		// Block until enough probes overlap (proves concurrency). The timeout is
		// only a safety valve so a serial loop unwinds instead of hanging forever
		// — the watchdog below still fails the test in that case.
		select {
		case <-release:
		case <-time.After(3 * time.Second):
		}
		atomic.AddInt32(&inFlight, -1)
		w.WriteHeader(http.StatusOK)
	})
	h.helmConcurrency = n

	instances := make([]sqlc.ArgocdInstance, n)
	for i := range instances {
		instances[i] = sqlc.ArgocdInstance{
			ID:                 uuid.New(),
			Name:               "argocd",
			ApiUrl:             srv.URL,
			AuthTokenEncrypted: "test-token",
			IsHealthy:          true,
		}
	}
	rec.instances = instances

	done := make(chan struct{})
	go func() {
		h.reconcileInstanceHealth(context.Background())
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatalf("reconcileInstanceHealth did not complete: only %d probes ever overlapped (want >= %d) — health probes are serialized, not fanned out", atomic.LoadInt32(&maxSeen), barrier)
	}

	if maxSeen < barrier {
		t.Fatalf("peak concurrent probes = %d, want >= %d (fan-out); a serial loop would show 1", maxSeen, barrier)
	}
}
