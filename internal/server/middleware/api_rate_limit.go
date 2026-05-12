// Package middleware — API rate limiting.
//
// A generic per-(user, endpoint-class) token-bucket
// limiter. Three endpoint classes carry meaningful DoS surface today:
//
//   - search: the cross-cluster resource search fan-out can hammer every
//     tunnel in the fleet simultaneously
//   - k8s-proxy: the /clusters/{cluster_id}/k8s/* passthrough is the most
//     common path a misbehaving caller would loop on
//   - exec / logs: WebSocket upgrades that hold a goroutine per session
//
// We deliberately do NOT rate-limit basic CRUD endpoints — they're cheap
// enough that the per-handler timeout middleware is sufficient and
// limiting them would make UI lists feel slow under burst usage.
//
// Keying: authenticated user ID when present, falls back to client IP.
// Token-bucket from golang.org/x/time/rate; one bucket per (key, class).
// Idle buckets are evicted by a background janitor (same pattern as the
// login limiter — eviction is what stops the map from leaking).
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

	"golang.org/x/time/rate"
)

// APIRateLimitClass names a rate-limit bucket. New classes can be added
// here as needed; the chart's rateLimit.<class>.{rate,burst} values plug
// in directly.
type APIRateLimitClass string

const (
	// ClassSearch covers cross-cluster fan-out search endpoints.
	ClassSearch APIRateLimitClass = "search"
	// ClassK8sProxy covers the /clusters/{id}/k8s/* passthrough.
	ClassK8sProxy APIRateLimitClass = "k8s-proxy"
	// ClassExecLogs covers the WebSocket upgrades for exec + logs.
	// Limit is on NEW sessions per window; in-flight sessions are not
	// metered (they hold a goroutine, not a request quota).
	ClassExecLogs APIRateLimitClass = "exec-logs"
	// ClassHelm covers helm install/upgrade/uninstall write endpoints.
	ClassHelm APIRateLimitClass = "helm"
)

// APIRateLimitConfig is the per-class tunable. Rate is the long-term
// average requests per second; Burst is the size of the token bucket
// (the most a caller can briefly do before backpressure kicks in).
type APIRateLimitConfig struct {
	RatePerSecond float64
	Burst         int
}

// Sensible defaults — tuned so a human-paced UI can refresh aggressively
// without hitting the limit, but a runaway loop trips within seconds.
// Operators override via chart values.
var defaultLimits = map[APIRateLimitClass]APIRateLimitConfig{
	ClassSearch:   {RatePerSecond: 10.0 / 60.0, Burst: 5}, // 10/min sustained, brief burst of 5
	ClassK8sProxy: {RatePerSecond: 60.0 / 60.0, Burst: 20},
	ClassExecLogs: {RatePerSecond: 30.0 / 60.0, Burst: 5}, // 30 new sessions/min
	ClassHelm:     {RatePerSecond: 5.0 / 60.0, Burst: 2},
}

// apiBucket pairs a limiter with the last-access time for eviction.
type apiBucket struct {
	lim      *rate.Limiter
	lastSeen time.Time
}

// apiRateLimiter is the shared bucket store. Keys are
// "<class>:<user-or-ip>" so multiple classes for one user evict
// independently. The mutex is coarse — fine-grained sharding only buys
// performance if Allow() is on the hot path of a 1000+ RPS endpoint,
// which none of the rate-limited classes are.
type apiRateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*apiBucket
	configs map[APIRateLimitClass]APIRateLimitConfig
	now     func() time.Time
	// evictAfter is the idle-bucket TTL — anything not touched in this
	// long gets dropped. Default 10× the longest fill window so a brief
	// pause doesn't lose the carry-over tokens.
	evictAfter time.Duration
}

func newAPIRateLimiter(configs map[APIRateLimitClass]APIRateLimitConfig, now func() time.Time) *apiRateLimiter {
	if configs == nil {
		configs = defaultLimits
	}
	if now == nil {
		now = time.Now
	}
	return &apiRateLimiter{
		buckets:    make(map[string]*apiBucket),
		configs:    configs,
		now:        now,
		evictAfter: 10 * time.Minute,
	}
}

// allow consults (and creates) the bucket for (class, key). Returns
// allowed + the wait suggested by the limiter when blocked.
func (l *apiRateLimiter) allow(class APIRateLimitClass, key string) (bool, time.Duration) {
	cfg, ok := l.configs[class]
	if !ok {
		// Unknown class: allow. Better to fail open on misconfig than
		// to lock the whole API.
		return true, 0
	}
	mapKey := string(class) + ":" + key

	l.mu.Lock()
	b, found := l.buckets[mapKey]
	if !found {
		b = &apiBucket{
			lim: rate.NewLimiter(rate.Limit(cfg.RatePerSecond), cfg.Burst),
		}
		l.buckets[mapKey] = b
	}
	b.lastSeen = l.now()
	l.mu.Unlock()

	if !b.lim.Allow() {
		// Reserve to learn the wait. Cancel immediately so the reserved
		// token returns to the bucket — the caller is being rejected, not
		// throttled-and-served.
		res := b.lim.Reserve()
		wait := res.Delay()
		res.Cancel()
		return false, wait
	}
	return true, 0
}

// evictExpired drops idle buckets. Same shape as the login limiter's
// janitor. Returns the count for tests.
func (l *apiRateLimiter) evictExpired() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := l.now().Add(-l.evictAfter)
	evicted := 0
	for k, b := range l.buckets {
		if b.lastSeen.Before(cutoff) {
			delete(l.buckets, k)
			evicted++
		}
	}
	return evicted
}

// startJanitor runs evictExpired on a ticker. Bound to ctx for clean
// shutdown.
func (l *apiRateLimiter) startJanitor(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = l.evictAfter / 2
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

// APIRateLimit returns chi middleware that enforces the named class.
// Bucket store and janitor live for the lifetime of the supplied ctx.
// Multiple calls (one per class) share state through the closure-bound
// limiter; in practice operators want a single shared limiter so
// passing the same configs map keeps wiring trivial.
func APIRateLimit(ctx context.Context, class APIRateLimitClass, configs map[APIRateLimitClass]APIRateLimitConfig) func(http.Handler) http.Handler {
	return apiRateLimitWith(ctx, class, configs, time.Now)
}

func apiRateLimitWith(ctx context.Context, class APIRateLimitClass, configs map[APIRateLimitClass]APIRateLimitConfig, now func() time.Time) func(http.Handler) http.Handler {
	limiter := newAPIRateLimiter(configs, now)
	limiter.startJanitor(ctx, 0)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := apiRateLimitKey(r)
			allowed, wait := limiter.allow(class, key)
			if allowed {
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", formatAPIRetryAfter(wait))
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"code":    "rate_limited",
				"message": "Too many requests for this endpoint class",
			})
		})
	}
}

// apiRateLimitKey derives a stable per-caller key. Authenticated user ID
// is preferred so distinct sessions for one user share a bucket; falls
// back to the client IP for anonymous calls (which only matters for
// pre-auth endpoints — most of the rate-limited classes are gated by
// requireAuth anyway).
func apiRateLimitKey(r *http.Request) string {
	if r == nil {
		return "unknown"
	}
	if u, ok := GetAuthenticatedUser(r.Context()); ok && u != nil {
		return "u:" + u.ID
	}
	addr := strings.TrimSpace(r.RemoteAddr)
	host, _, err := net.SplitHostPort(addr)
	if err == nil {
		return "ip:" + host
	}
	if addr != "" {
		return "ip:" + addr
	}
	return "unknown"
}

func formatAPIRetryAfter(d time.Duration) string {
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
