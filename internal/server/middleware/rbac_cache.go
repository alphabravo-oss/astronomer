package middleware

import (
	"container/list"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

// RBACCacheDefaultTTL is the default time-to-live for a cached set of bindings.
// Kept short so a role/binding change shows up within seconds even on the
// (rare) code path that forgets to call Invalidate. Mutation handlers should
// still invalidate explicitly so changes propagate immediately, not 15s later.
const RBACCacheDefaultTTL = 15 * time.Second

// RBACCacheDefaultCapacity bounds the number of distinct user IDs held in the
// cache. Each entry is a slice of bindings (usually 1-10 elements) plus the
// expiry timestamp; 5000 users ≈ a few MB worst-case. The cache evicts
// least-recently-used entries when the bound is exceeded.
const RBACCacheDefaultCapacity = 5000

// RBACCacheDefaultMaxBindings bounds the TOTAL number of role bindings held
// across all cached users, independent of the user-count capacity. With
// namespace-scoped RBAC enabled a single project member can expand into
// hundreds/thousands of synthetic per-namespace bindings, so a per-user count
// bound alone no longer approximates memory: a handful of large-project users
// would blow the "few MB" assumption. Eviction trims LRU entries until BOTH the
// user-count and this binding-count bound hold. 0 disables the binding bound.
const RBACCacheDefaultMaxBindings = 100000

var (
	rbacCacheMetricsOnce sync.Once

	rbacCacheHits = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Name:      "rbac_cache_hits_total",
			Help:      "Total RBAC binding lookups served from the per-user cache (no DB round-trip).",
		},
		observability.MetricLabels(),
	)
	rbacCacheMisses = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Name:      "rbac_cache_misses_total",
			Help:      "Total RBAC binding lookups that bypassed the per-user cache and hit the DB.",
		},
		observability.MetricLabels(),
	)
)

// registerRBACCacheMetrics is invoked exactly once per process the first time
// an RBACCache is constructed. Tests construct multiple caches; we must not
// double-register with Prometheus or it will panic.
func registerRBACCacheMetrics() {
	rbacCacheMetricsOnce.Do(func() {
		// MustRegister panics on duplicate registration; AlreadyRegisteredError
		// is the only expected failure mode and signals a re-entry from another
		// init path, which we tolerate.
		if err := prometheus.Register(rbacCacheHits); err != nil {
			if _, ok := err.(prometheus.AlreadyRegisteredError); !ok {
				panic(err)
			}
		}
		if err := prometheus.Register(rbacCacheMisses); err != nil {
			if _, ok := err.(prometheus.AlreadyRegisteredError); !ok {
				panic(err)
			}
		}
	})
}

type rbacCacheEntry struct {
	userID    string
	bindings  []rbac.RoleBinding
	expiresAt time.Time
	elem      *list.Element // back-pointer into the LRU list for O(1) moves
	// lastBumpNanos is the unix-nano timestamp of the last LRU MoveToFront for
	// this entry, read/written atomically WITHOUT the cache mutex. It drives the
	// amortized-LRU touch on the read path: a cache hit reorders the entry at
	// most once per bumpInterval, so steady-state hits never take the exclusive
	// lock. Accessed lock-free so a hot entry with many concurrent hits does not
	// serialize every request through c.mu.
	lastBumpNanos atomic.Int64
}

// RBACCache is a TTL+LRU cache mapping user_id → role bindings. The hot path
// is the authenticated-HTTP middleware so the design is RWMutex-guarded with
// a fast read-path: lookup uses RLock, hits update the LRU position under
// Lock (rare, only after a successful read), misses populate under Lock.
//
// Invalidation is explicit: every handler that writes to *_role_bindings or
// *_roles must call Invalidate(userID) for each affected user after the DB
// commit, so the next request sees the new bindings without waiting for the
// TTL to expire.
type RBACCache struct {
	mu       sync.RWMutex
	ttl      time.Duration
	capacity int
	// maxBindings bounds the total number of bindings across all entries (0 =
	// unbounded). bindingCount tracks the current running total, maintained
	// under c.mu on every insert/replace/evict.
	maxBindings  int
	bindingCount int
	// bumpInterval throttles the amortized-LRU MoveToFront on the read path: an
	// entry is reordered at most once per interval, so a stream of hits does not
	// hammer the write lock. Derived from the TTL in the constructor.
	bumpInterval time.Duration
	// now is overridable for tests (so we can fast-forward past the TTL
	// deterministically). Production callers leave it as time.Now.
	now func() time.Time

	entries map[string]*rbacCacheEntry
	lru     *list.List // front = most recently used; back = LRU
}

// NewRBACCache constructs a cache with sensible defaults
// (RBACCacheDefaultTTL / RBACCacheDefaultCapacity).
func NewRBACCache() *RBACCache {
	return NewRBACCacheWithOptions(RBACCacheDefaultTTL, RBACCacheDefaultCapacity)
}

// NewRBACCacheWithOptions allows callers (typically tests) to override the
// TTL and capacity. Non-positive values fall back to the defaults.
func NewRBACCacheWithOptions(ttl time.Duration, capacity int) *RBACCache {
	if ttl <= 0 {
		ttl = RBACCacheDefaultTTL
	}
	if capacity <= 0 {
		capacity = RBACCacheDefaultCapacity
	}
	registerRBACCacheMetrics()
	// Reorder a hit at most ~8 times over an entry's TTL: frequent enough that a
	// hot entry stays near the front (so it survives eviction), rare enough that
	// the exclusive lock is off the steady-state hot path.
	bumpInterval := ttl / 8
	if bumpInterval <= 0 {
		bumpInterval = ttl
	}
	return &RBACCache{
		ttl:          ttl,
		capacity:     capacity,
		maxBindings:  RBACCacheDefaultMaxBindings,
		bumpInterval: bumpInterval,
		now:          time.Now,
		entries:      make(map[string]*rbacCacheEntry, capacity),
		lru:          list.New(),
	}
}

// Get returns the cached bindings for userID along with `ok=true` when the
// entry is present AND not expired. Stale entries are treated as a miss and
// pruned. Callers must populate after a miss via Put.
func (c *RBACCache) Get(userID string) ([]rbac.RoleBinding, bool) {
	if c == nil || userID == "" {
		return nil, false
	}
	c.mu.RLock()
	entry, ok := c.entries[userID]
	if !ok {
		c.mu.RUnlock()
		rbacCacheMisses.WithLabelValues(observability.MetricValues()...).Inc()
		return nil, false
	}
	now := c.now()
	if now.After(entry.expiresAt) {
		c.mu.RUnlock()
		// Expired — promote to write lock to evict so subsequent gets miss
		// cleanly without re-entering the expiry branch every time.
		c.mu.Lock()
		if cur, still := c.entries[userID]; still && cur == entry {
			c.removeLocked(entry)
		}
		c.mu.Unlock()
		rbacCacheMisses.WithLabelValues(observability.MetricValues()...).Inc()
		return nil, false
	}
	bindings := entry.bindings
	last := entry.lastBumpNanos.Load()
	c.mu.RUnlock()

	// Amortized-LRU touch. Taking the exclusive lock on EVERY hit just to
	// MoveToFront serializes every authenticated request (and every /k8s/*
	// proxy request) through one mutex, defeating the RWMutex. Instead reorder
	// at most once per bumpInterval per entry, and let a single goroutine claim
	// that window via CompareAndSwap so concurrent hits don't stampede the write
	// lock — the rest return straight from the read snapshot with no exclusive
	// lock at all. A slightly stale LRU position is harmless: entries expire on
	// the TTL regardless, and a hot entry still bumps often enough to survive
	// eviction. We swallow the rare race where the entry was evicted between
	// RUnlock and Lock; the next request repopulates it.
	if nowNanos := now.UnixNano(); nowNanos-last >= int64(c.bumpInterval) &&
		entry.lastBumpNanos.CompareAndSwap(last, nowNanos) {
		c.mu.Lock()
		if cur, still := c.entries[userID]; still && cur == entry {
			c.lru.MoveToFront(entry.elem)
		}
		c.mu.Unlock()
	}
	rbacCacheHits.WithLabelValues(observability.MetricValues()...).Inc()
	return bindings, true
}

// Put stores `bindings` for userID with a fresh TTL window. Safe to call
// concurrently. Evicts the oldest entry when the cache is over capacity.
func (c *RBACCache) Put(userID string, bindings []rbac.RoleBinding) {
	if c == nil || userID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	if existing, ok := c.entries[userID]; ok {
		c.bindingCount += len(bindings) - len(existing.bindings)
		existing.bindings = bindings
		existing.expiresAt = now.Add(c.ttl)
		existing.lastBumpNanos.Store(now.UnixNano())
		c.lru.MoveToFront(existing.elem)
		c.evictLocked()
		return
	}

	entry := &rbacCacheEntry{
		userID:    userID,
		bindings:  bindings,
		expiresAt: now.Add(c.ttl),
	}
	entry.lastBumpNanos.Store(now.UnixNano())
	entry.elem = c.lru.PushFront(entry)
	c.entries[userID] = entry
	c.bindingCount += len(bindings)
	c.evictLocked()
}

// evictLocked trims LRU entries until BOTH the user-count capacity and the
// total-binding bound hold. There is no soft-eviction (no timed sweep): the
// next access of a stale entry expires it, and the LRU trims under pressure.
// Caller must hold the write lock. The lru.Len() > 1 guard on the binding bound
// prevents an infinite loop (and evict-self) when a single entry's bindings
// alone exceed maxBindings.
func (c *RBACCache) evictLocked() {
	for c.lru.Len() > c.capacity ||
		(c.maxBindings > 0 && c.bindingCount > c.maxBindings && c.lru.Len() > 1) {
		oldest := c.lru.Back()
		if oldest == nil {
			break
		}
		c.removeLocked(oldest.Value.(*rbacCacheEntry))
	}
}

// Invalidate drops the cached entry for userID. Call this immediately after
// any write to *_role_bindings or *_roles for the user, so the next request
// re-fetches and observes the change without waiting for the TTL. No-op when
// userID is empty (anonymous) or has no entry.
func (c *RBACCache) Invalidate(userID string) {
	if c == nil || userID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry, ok := c.entries[userID]; ok {
		c.removeLocked(entry)
	}
}

// InvalidateAll drops every cached entry. Used by tests and by callers that
// just edited a role definition (which affects every user bound to it).
func (c *RBACCache) InvalidateAll() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]*rbacCacheEntry, c.capacity)
	c.lru.Init()
	c.bindingCount = 0
}

// Len returns the number of entries currently cached. Test-only.
func (c *RBACCache) Len() int {
	if c == nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lru.Len()
}

// removeLocked drops the entry from both the map and the LRU list. The caller
// must hold the write lock.
func (c *RBACCache) removeLocked(entry *rbacCacheEntry) {
	if entry == nil {
		return
	}
	if entry.elem != nil {
		c.lru.Remove(entry.elem)
		entry.elem = nil
	}
	c.bindingCount -= len(entry.bindings)
	if c.bindingCount < 0 {
		c.bindingCount = 0
	}
	delete(c.entries, entry.userID)
}
