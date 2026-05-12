package middleware

import (
	"container/list"
	"sync"
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
	return &RBACCache{
		ttl:      ttl,
		capacity: capacity,
		now:      time.Now,
		entries:  make(map[string]*rbacCacheEntry, capacity),
		lru:      list.New(),
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
	if c.now().After(entry.expiresAt) {
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
	c.mu.RUnlock()
	// Touch the LRU under a write lock so the entry survives eviction.
	// We swallow the rare race where the entry was evicted between RUnlock
	// and Lock; the next request will repopulate it.
	c.mu.Lock()
	if cur, still := c.entries[userID]; still && cur == entry {
		c.lru.MoveToFront(entry.elem)
	}
	c.mu.Unlock()
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

	if existing, ok := c.entries[userID]; ok {
		existing.bindings = bindings
		existing.expiresAt = c.now().Add(c.ttl)
		c.lru.MoveToFront(existing.elem)
		return
	}

	entry := &rbacCacheEntry{
		userID:    userID,
		bindings:  bindings,
		expiresAt: c.now().Add(c.ttl),
	}
	entry.elem = c.lru.PushFront(entry)
	c.entries[userID] = entry

	// Evict oldest if we exceeded capacity. There is no soft-eviction (no
	// timed sweep): the next access of a stale entry expires it, and the LRU
	// trims under pressure.
	for c.lru.Len() > c.capacity {
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
	delete(c.entries, entry.userID)
}
