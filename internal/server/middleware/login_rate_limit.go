package middleware

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type loginRateLimitBucket struct {
	count   int
	resetAt time.Time
}

type loginRateLimiter struct {
	mu      sync.Mutex
	buckets map[string]loginRateLimitBucket
	limit   int
	window  time.Duration
	now     func() time.Time
}

func newLoginRateLimiter(limit int, window time.Duration, now func() time.Time) *loginRateLimiter {
	if limit <= 0 {
		limit = 5
	}
	if window <= 0 {
		window = time.Minute
	}
	if now == nil {
		now = time.Now
	}
	return &loginRateLimiter{
		buckets: make(map[string]loginRateLimitBucket),
		limit:   limit,
		window:  window,
		now:     now,
	}
}

// loginRateDecision carries the standard rate-limit header inputs for the
// fixed-window login counter: Limit is the per-window cap, Remaining is the
// attempts left in the current window, Reset is the seconds until the window
// rolls over.
type loginRateDecision struct {
	allowed    bool
	retryAfter time.Duration
	limit      int
	remaining  int
	reset      int
}

func (l *loginRateLimiter) allow(key string) loginRateDecision {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	bucket, ok := l.buckets[key]
	if !ok || !now.Before(bucket.resetAt) {
		resetAt := now.Add(l.window)
		l.buckets[key] = loginRateLimitBucket{
			count:   1,
			resetAt: resetAt,
		}
		return loginRateDecision{
			allowed:   true,
			limit:     l.limit,
			remaining: l.limit - 1,
			reset:     loginResetSeconds(resetAt.Sub(now)),
		}
	}

	if bucket.count >= l.limit {
		return loginRateDecision{
			retryAfter: bucket.resetAt.Sub(now),
			limit:      l.limit,
			remaining:  0,
			reset:      loginResetSeconds(bucket.resetAt.Sub(now)),
		}
	}

	bucket.count++
	l.buckets[key] = bucket
	remaining := l.limit - bucket.count
	if remaining < 0 {
		remaining = 0
	}
	return loginRateDecision{
		allowed:   true,
		limit:     l.limit,
		remaining: remaining,
		reset:     loginResetSeconds(bucket.resetAt.Sub(now)),
	}
}

// loginResetSeconds renders a window-remaining duration as a whole-second
// count for the RateLimit-Reset header (ceil, min 1 while the window is open).
func loginResetSeconds(d time.Duration) int {
	if d <= 0 {
		return 0
	}
	secs := int(d.Seconds())
	if d > time.Duration(secs)*time.Second {
		secs++
	}
	if secs < 1 {
		secs = 1
	}
	return secs
}

// evictExpired removes buckets whose reset window has already passed.
// Without this the buckets map grows unboundedly: every distinct client IP
// that ever hits /auth/login leaves a row behind. At scale (and especially
// behind shared egress NAT where one "client" looks like many IPs over
// time) the map becomes a slow memory leak and a steadily-growing mutex
// hot spot. Returning the eviction count is useful for tests; production
// callers ignore it.
func (l *loginRateLimiter) evictExpired() int {
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

// startJanitor runs evictExpired on a ticker for the lifetime of ctx. The
// interval is the bucket window times 2 — sweeping more often is wasted
// work; sweeping less often defeats the purpose. Exits cleanly when ctx
// is cancelled (e.g. during server shutdown).
func (l *loginRateLimiter) startJanitor(ctx context.Context, interval time.Duration) {
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

// LoginRateLimit applies a small per-client cap to login attempts to slow
// brute-force attacks against the password endpoint. The returned middleware
// also starts a background goroutine that evicts expired buckets so the
// internal map doesn't grow indefinitely. The janitor runs for the lifetime
// of the process — callers that need scoped lifetime should use
// LoginRateLimitWithContext instead.
func LoginRateLimit(limit int, window time.Duration) func(http.Handler) http.Handler {
	return LoginRateLimitWithContext(context.Background(), limit, window)
}

// LoginRateLimitWithContext is the same as LoginRateLimit but ties the
// janitor goroutine to the supplied context. Useful in tests so the
// goroutine doesn't leak between runs.
func LoginRateLimitWithContext(ctx context.Context, limit int, window time.Duration) func(http.Handler) http.Handler {
	return newLoginRateLimitMiddleware(ctx, limit, window, time.Now)
}

func newLoginRateLimitMiddleware(ctx context.Context, limit int, window time.Duration, now func() time.Time) func(http.Handler) http.Handler {
	limiter := newLoginRateLimiter(limit, window, now)
	limiter.startJanitor(ctx, 0)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := clientKey(r)
			if key == "" {
				key = "unknown"
			}
			d := limiter.allow(key)
			setLoginRateLimitHeaders(w, d)
			if d.allowed {
				next.ServeHTTP(w, r)
				return
			}

			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", formatRetryAfter(d.retryAfter))
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"code":    "rate_limited",
				"message": "Too many login attempts",
			})
		})
	}
}

// setLoginRateLimitHeaders emits the IETF draft standard rate-limit headers
// for the login limiter on every response (allowed or 429) so a client can
// see how many attempts remain in the current window.
func setLoginRateLimitHeaders(w http.ResponseWriter, d loginRateDecision) {
	h := w.Header()
	h.Set("RateLimit-Limit", strconv.Itoa(d.limit))
	h.Set("RateLimit-Remaining", strconv.Itoa(d.remaining))
	h.Set("RateLimit-Reset", strconv.Itoa(d.reset))
}

func clientKey(r *http.Request) string {
	if r == nil {
		return ""
	}
	addr := strings.TrimSpace(r.RemoteAddr)
	if addr == "" {
		return ""
	}
	host, _, err := net.SplitHostPort(addr)
	if err == nil {
		return host
	}
	return addr
}

func formatRetryAfter(d time.Duration) string {
	if d <= 0 {
		return "1"
	}
	seconds := int(d.Seconds())
	if d > time.Duration(seconds)*time.Second {
		seconds++
	}
	if seconds < 1 {
		seconds = 1
	}
	return strconv.Itoa(seconds)
}
