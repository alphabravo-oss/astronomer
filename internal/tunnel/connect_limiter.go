package tunnel

import (
	"context"
	"sync"
	"time"
)

// ConnectFailureLimiter is a per-source-IP, fixed-window FAILURE counter that
// throttles tunnel CONNECT attempts (M5). It mirrors the proven
// internal/server/middleware loginRateLimiter rather than wrapping
// golang.org/x/time/rate: the success/failure verdict for a CONNECT is only
// known mid-handler (after validateAndMaybeRotateToken hits the DB), so a
// continuous req/sec token bucket is the wrong model — we count FAILURES and
// reset on success.
//
// HEALTHY-FLEET GUARANTEE: only failed CONNECT validations increment a bucket,
// and every successful connect calls Reset(ip) which deletes the entry. A real
// fleet behind one NAT/egress IP emits ~0 auth failures and resets to zero on
// each success, so it is mathematically un-throttleable; an attacker probing
// the DB token lookup is capped at `limit` lookups per IP per `window`.
type ConnectFailureLimiter struct {
	mu sync.Mutex
	// ponytail: bounded two ways — Reset(ip) deletes the entry on every
	// successful connect (healthy IPs never linger) and startJanitor reaps
	// expired windows. Mirrors loginRateLimiter rather than pulling in a
	// TTL-cache dep.
	buckets map[string]connectBucket
	limit   int
	window  time.Duration
	now     func() time.Time
}

type connectBucket struct {
	failures int
	resetAt  time.Time
}

// NewConnectFailureLimiter builds the limiter. limit<=0 defaults to 50,
// window<=0 to 5m, now==nil to time.Now (defensive defaulting in the same
// shape as newLoginRateLimiter so tests can inject a deterministic clock).
func NewConnectFailureLimiter(limit int, window time.Duration, now func() time.Time) *ConnectFailureLimiter {
	if limit <= 0 {
		limit = 50
	}
	if window <= 0 {
		window = 5 * time.Minute
	}
	if now == nil {
		now = time.Now
	}
	return &ConnectFailureLimiter{
		buckets: make(map[string]connectBucket),
		limit:   limit,
		window:  window,
		now:     now,
	}
}

// Blocked reports whether the given IP is currently over the failure threshold.
// Read-only: it never mutates the bucket (the failure count is bumped only by
// Fail). retryAfter is the time until the offending IP's window rolls over.
func (l *ConnectFailureLimiter) Blocked(key string) (bool, time.Duration) {
	if l == nil {
		return false, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	bucket, ok := l.buckets[key]
	if !ok || !now.Before(bucket.resetAt) {
		return false, 0
	}
	if bucket.failures >= l.limit {
		return true, bucket.resetAt.Sub(now)
	}
	return false, 0
}

// Fail records one failed CONNECT validation for the IP. This is the ONLY thing
// that increments a bucket. A fresh or expired window starts a new one.
func (l *ConnectFailureLimiter) Fail(key string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	bucket, ok := l.buckets[key]
	if !ok || !now.Before(bucket.resetAt) {
		l.buckets[key] = connectBucket{failures: 1, resetAt: now.Add(l.window)}
		return
	}
	bucket.failures++
	l.buckets[key] = bucket
}

// Reset clears the IP's failure history. Called on EVERY successful connect —
// the airtight healthy-agent guarantee.
func (l *ConnectFailureLimiter) Reset(key string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	delete(l.buckets, key)
	l.mu.Unlock()
}

// evictExpired drops buckets whose window has already passed so the map can't
// grow one entry per ever-seen IP. Returns the eviction count (tests use it;
// production callers ignore it).
func (l *ConnectFailureLimiter) evictExpired() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	evicted := 0
	for key, bucket := range l.buckets {
		if !now.Before(bucket.resetAt) {
			delete(l.buckets, key)
			evicted++
		}
	}
	return evicted
}

// StartJanitor runs evictExpired on a ticker for the lifetime of ctx. Defaults
// the interval to 2*window when non-positive (same heuristic as the login
// limiter). Exits cleanly on ctx cancel. Exported because the limiter is
// constructed in internal/server but its lifetime is tied to the server's
// reconcile context.
func (l *ConnectFailureLimiter) StartJanitor(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 2 * l.window
	}
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				l.evictExpired()
			}
		}
	}()
}
