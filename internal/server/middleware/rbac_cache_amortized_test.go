package middleware

import (
	"sync"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

// nBindings returns a slice of n dummy bindings so tests can drive the
// total-binding bound deterministically.
func nBindings(n int) []rbac.RoleBinding {
	out := make([]rbac.RoleBinding, n)
	for i := range out {
		out[i] = rbac.RoleBinding{UserID: "u"}
	}
	return out
}

// fixedClock returns a now() closure whose value can be advanced from the test.
func fixedClock(start time.Time) (func() time.Time, func(time.Duration)) {
	var mu sync.Mutex
	cur := start
	now := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return cur
	}
	advance := func(d time.Duration) {
		mu.Lock()
		defer mu.Unlock()
		cur = cur.Add(d)
	}
	return now, advance
}

// TestRBACCache_AmortizedBumpPromotesHotEntry (finding: rbac_cache.go MoveToFront
// on every hit). After the bumpInterval elapses, a cache HIT must still reorder
// the entry to the front so a frequently-accessed entry survives eviction.
func TestRBACCache_AmortizedBumpPromotesHotEntry(t *testing.T) {
	t.Parallel()
	cache := NewRBACCacheWithOptions(15*time.Second, 2)
	t0 := time.Now()
	now, advance := fixedClock(t0)
	cache.now = now

	cache.Put("A", nBindings(1))
	cache.Put("B", nBindings(1)) // LRU front->back: [B, A]

	// Advance past the bump interval (ttl/8) so the next hit is eligible to
	// reorder, then hit A. A must move to the front.
	advance(cache.bumpInterval + time.Millisecond)
	if _, ok := cache.Get("A"); !ok {
		t.Fatalf("Get(A) within TTL: want hit")
	}

	// Insert C: capacity is 2, so the LRU tail is evicted. Because A was bumped
	// to the front, B is now the tail and must be the one evicted, not A.
	cache.Put("C", nBindings(1))

	if _, ok := cache.Get("A"); !ok {
		t.Fatalf("hot entry A was evicted despite an amortized bump")
	}
	if _, ok := cache.Get("B"); ok {
		t.Fatalf("cold entry B survived; expected it to be evicted")
	}
}

// TestRBACCache_HitWithinBumpIntervalDoesNotReorder verifies the amortization
// actually skips the exclusive-lock MoveToFront on hits inside the interval:
// a hit within the window must NOT promote the entry, so it can still be evicted
// as the LRU tail (proving the write lock was avoided, not merely optimized).
func TestRBACCache_HitWithinBumpIntervalDoesNotReorder(t *testing.T) {
	t.Parallel()
	cache := NewRBACCacheWithOptions(15*time.Second, 2)
	t0 := time.Now()
	now, _ := fixedClock(t0)
	cache.now = now

	cache.Put("A", nBindings(1))
	cache.Put("B", nBindings(1)) // LRU front->back: [B, A]

	// Hit A WITHOUT advancing the clock: still inside A's bump window (A was just
	// inserted at t0), so no reorder happens and A stays the tail.
	if _, ok := cache.Get("A"); !ok {
		t.Fatalf("Get(A): want hit")
	}

	cache.Put("C", nBindings(1)) // evicts the tail, which must still be A

	if _, ok := cache.Get("A"); ok {
		t.Fatalf("A was promoted by a within-interval hit; amortization did not skip the bump")
	}
	if _, ok := cache.Get("B"); !ok {
		t.Fatalf("B should have survived (A was the tail)")
	}
}

// TestRBACCache_BindingCountBoundEvicts (finding: project->namespace expansion
// blows the memory bound). Eviction must honor the total-binding bound even when
// the user-count capacity is nowhere near full.
func TestRBACCache_BindingCountBoundEvicts(t *testing.T) {
	t.Parallel()
	cache := NewRBACCacheWithOptions(15*time.Second, 100)
	cache.maxBindings = 5 // small enough that two 3-binding users exceed it

	cache.Put("A", nBindings(3))
	cache.Put("B", nBindings(3)) // total 6 > 5 -> LRU tail (A) evicted

	if got := cache.Len(); got != 1 {
		t.Fatalf("cache size = %d, want 1 (binding bound should evict despite user capacity)", got)
	}
	if _, ok := cache.Get("A"); ok {
		t.Fatalf("A should have been evicted by the binding bound")
	}
	if _, ok := cache.Get("B"); !ok {
		t.Fatalf("B (most recent) should be retained")
	}
	if cache.bindingCount != 3 {
		t.Fatalf("bindingCount = %d, want 3", cache.bindingCount)
	}
}

// TestRBACCache_BindingBoundNeverEvictsSoleEntry guards the evict-self / infinite
// loop case: a single entry whose bindings alone exceed the bound must be kept
// (there is nothing older to evict) and must not spin.
func TestRBACCache_BindingBoundNeverEvictsSoleEntry(t *testing.T) {
	t.Parallel()
	cache := NewRBACCacheWithOptions(15*time.Second, 100)
	cache.maxBindings = 2

	cache.Put("A", nBindings(5)) // 5 > 2, but it's the only entry

	if got := cache.Len(); got != 1 {
		t.Fatalf("cache size = %d, want 1 (sole over-bound entry must be kept)", got)
	}
	if _, ok := cache.Get("A"); !ok {
		t.Fatalf("sole entry A must remain cached")
	}
}

// TestRBACCache_ConcurrentHitsNoRace drives many concurrent hits through the
// read path with -race intent: the amortized bump uses atomics + a re-checked
// write lock and must not race or lose the entry.
func TestRBACCache_ConcurrentHitsNoRace(t *testing.T) {
	t.Parallel()
	cache := NewRBACCacheWithOptions(15*time.Second, 100)
	cache.Put("hot", nBindings(3))

	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				if _, ok := cache.Get("hot"); !ok {
					t.Errorf("Get(hot): want hit")
					return
				}
			}
		}()
	}
	wg.Wait()

	if _, ok := cache.Get("hot"); !ok {
		t.Fatalf("hot entry lost after concurrent hits")
	}
}
