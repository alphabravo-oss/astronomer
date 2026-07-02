package tunnel

import (
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"
)

// TestStateLimiterDoubleCheckedLocking verifies stateLimiter() is race-free
// and constructs exactly one shared limiter under concurrency. The fix moved
// the hot path off the hub-wide EXCLUSIVE write lock (which serialized every
// STATE_UPDATE frame across all clusters and blocked RLock readers) onto a
// double-checked read-locked fast path. Run under -race: a broken DCL would
// either build multiple limiters (distinct pointers below) or trip the race
// detector on h.stateLim.
func TestStateLimiterDoubleCheckedLocking(t *testing.T) {
	h := NewHub(slog.Default())

	const n = 64
	ptrs := make([]*stateUpdateLimiter, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			<-start // maximize contention on the first (constructing) call
			ptrs[i] = h.stateLimiter()
		}(i)
	}
	close(start)
	wg.Wait()

	first := ptrs[0]
	if first == nil {
		t.Fatal("stateLimiter returned nil")
	}
	for i := 1; i < n; i++ {
		if ptrs[i] != first {
			t.Fatalf("stateLimiter returned distinct instances (i=%d): double-checked locking must construct exactly one limiter", i)
		}
	}
	// A subsequent call takes the read-locked fast path and returns the same
	// instance.
	if again := h.stateLimiter(); again != first {
		t.Fatal("stateLimiter fast path returned a different instance")
	}
}

// TestStateUpdateLimiterEvictionAmortized proves the O(N) eviction sweep is
// amortized to once per evictSampleEvery calls rather than running on every
// call once the map is large. Before the fix, evictLocked swept whenever
// len(last) >= 256 — with a churny key set that stays above the threshold the
// sweep never got amortized and every state frame paid a full map scan.
func TestStateUpdateLimiterEvictionAmortized(t *testing.T) {
	r := newStateUpdateLimiter(time.Millisecond)
	base := time.Unix(10_000, 0)
	r.now = func() time.Time { return base }

	// Pre-populate >threshold stale entries (timestamps well past the evict
	// cutoff) so a sweep, if it runs, would delete all of them.
	const stale = 300
	staleAt := base.Add(-2 * stateLimiterEvictAfter)
	for i := 0; i < stale; i++ {
		r.last[fmt.Sprintf("stale-%d", i)] = staleAt
	}

	// A single allow() must NOT trigger the sweep. Before the fix this ran an
	// immediate O(N) scan (len >= threshold), collapsing the map to ~1 entry.
	r.allow("probe-0")
	if len(r.last) < stale {
		t.Fatalf("single allow() ran a full O(N) sweep (len=%d, want >=%d): eviction is not amortized", len(r.last), stale)
	}

	// Drive to the evictSampleEvery-th call: the amortized sweep now runs once
	// and clears the stale entries.
	for i := 1; i < evictSampleEvery; i++ {
		r.allow(fmt.Sprintf("probe-%d", i))
	}
	for i := 0; i < stale; i++ {
		if _, ok := r.last[fmt.Sprintf("stale-%d", i)]; ok {
			t.Fatalf("stale entry stale-%d survived the amortized sweep at call %d", i, evictSampleEvery)
		}
	}
}
