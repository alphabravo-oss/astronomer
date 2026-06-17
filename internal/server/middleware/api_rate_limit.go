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

	"github.com/go-chi/chi/v5"
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
	// ClassArgoCDProxy covers the internal /internal/argocd/clusters/{id}/k8s/*
	// machine-to-machine front door the built-in ArgoCD uses to deploy baseline
	// components. ArgoCD's cluster-cache discovery + resource sync fire large
	// request bursts (one per API resource type), so this class is sized far
	// more generously than the user-facing proxy — it is trusted, cluster-
	// scoped, token-gated traffic — while still capping a runaway.
	ClassArgoCDProxy APIRateLimitClass = "argocd-proxy"
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
	ClassSearch:      {RatePerSecond: 10.0 / 60.0, Burst: 5}, // 10/min sustained, brief burst of 5
	ClassK8sProxy:    {RatePerSecond: 1.0, Burst: 20},
	ClassArgoCDProxy: {RatePerSecond: 50.0, Burst: 1000},     // ArgoCD discovery/sync bursts (trusted M2M)
	ClassExecLogs:    {RatePerSecond: 30.0 / 60.0, Burst: 5}, // 30 new sessions/min
	ClassHelm:        {RatePerSecond: 5.0 / 60.0, Burst: 2},
}

// defaultClusterCeilings is the aggregate (all-users-combined) per-cluster
// cap for classes whose traffic funnels through a single cluster agent
// tunnel. The per-user limits in defaultLimits still apply (defense in
// depth); this is a second, coarser gate so that N distinct users — each
// individually within their per-user quota — cannot collectively saturate
// one cluster's tunnel. Only the tunnel-bound classes carry a ceiling;
// classes absent from this map are limited per-user only.
//
// Sizing: the ceiling is set comfortably above a healthy multi-operator
// working set but well below what would exhaust the agent's single
// in-flight relay, and the burst absorbs the natural fan-out of a UI
// loading several panes at once. Other clusters keep independent buckets,
// so hammering cluster A never throttles cluster B.
var defaultClusterCeilings = map[APIRateLimitClass]APIRateLimitConfig{
	ClassK8sProxy:    {RatePerSecond: 10.0, Burst: 60},
	ClassArgoCDProxy: {RatePerSecond: 100.0, Burst: 2000},      // baseline provisioning across many Apps per cluster
	ClassExecLogs:    {RatePerSecond: 120.0 / 60.0, Burst: 20}, // 120 new sessions/min/cluster
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
	// clusterCeilings holds the aggregate per-cluster cap for tunnel-bound
	// classes. Separate from configs so the per-user and per-cluster gates
	// tune independently. A class absent here is per-user only.
	clusterCeilings map[APIRateLimitClass]APIRateLimitConfig
	now             func() time.Time
	// evictAfter is the idle-bucket TTL — anything not touched in this
	// long gets dropped. Default 10× the longest fill window so a brief
	// pause doesn't lose the carry-over tokens.
	evictAfter time.Duration
}

func newAPIRateLimiter(configs map[APIRateLimitClass]APIRateLimitConfig, now func() time.Time) *apiRateLimiter {
	return newAPIRateLimiterWithCeilings(configs, defaultClusterCeilings, now)
}

func newAPIRateLimiterWithCeilings(configs, clusterCeilings map[APIRateLimitClass]APIRateLimitConfig, now func() time.Time) *apiRateLimiter {
	if configs == nil {
		configs = defaultLimits
	}
	if now == nil {
		now = time.Now
	}
	return &apiRateLimiter{
		buckets:         make(map[string]*apiBucket),
		configs:         configs,
		clusterCeilings: clusterCeilings,
		now:             now,
		evictAfter:      10 * time.Minute,
	}
}

// rateLimitDecision carries the standard-header inputs alongside the
// allow/deny verdict so the middleware can emit RateLimit-Limit /
// -Remaining / -Reset on both the success and the 429 path. Limit is the
// bucket burst (the policy quota), Remaining is the whole tokens left
// after this request, Reset is the seconds until the bucket is full again.
type rateLimitDecision struct {
	allowed   bool
	wait      time.Duration
	limit     int
	remaining int
	reset     int
	// known is false for an unknown class (fail-open) where there is no
	// configured quota to report; the middleware then emits no headers.
	known bool
}

// allow consults (and creates) the bucket for (class, key). Returns
// allowed + the wait suggested by the limiter when blocked.
func (l *apiRateLimiter) allow(class APIRateLimitClass, key string) rateLimitDecision {
	cfg, ok := l.configs[class]
	if !ok {
		// Unknown class: allow. Better to fail open on misconfig than
		// to lock the whole API.
		return rateLimitDecision{allowed: true}
	}
	return l.allowBucket(string(class)+":"+key, cfg)
}

// allowClusterCeiling enforces the aggregate (all-users-combined)
// per-cluster cap for tunnel-bound classes. It is a SECOND gate, applied
// on top of the per-user allow() — one cluster's tunnel is protected from
// the combined load of many in-policy users, while other clusters keep
// independent buckets and stay unaffected. A class with no configured
// ceiling, or a request with no resolvable cluster id, is not capped here
// (the per-user limit still applies).
func (l *apiRateLimiter) allowClusterCeiling(class APIRateLimitClass, clusterID string) rateLimitDecision {
	if clusterID == "" {
		return rateLimitDecision{allowed: true}
	}
	cfg, ok := l.clusterCeilings[class]
	if !ok {
		return rateLimitDecision{allowed: true}
	}
	return l.allowBucket(string(class)+":cluster:"+clusterID, cfg)
}

// allowBucket is the shared token-bucket primitive: consult (and create)
// the bucket at mapKey under cfg, returning the allow/deny verdict plus the
// standard-header inputs (limit/remaining/reset).
func (l *apiRateLimiter) allowBucket(mapKey string, cfg APIRateLimitConfig) rateLimitDecision {
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

	d := rateLimitDecision{known: true, limit: cfg.Burst}
	if !b.lim.Allow() {
		// Reserve to learn the wait. Cancel immediately so the reserved
		// token returns to the bucket — the caller is being rejected, not
		// throttled-and-served.
		res := b.lim.Reserve()
		d.wait = res.Delay()
		res.Cancel()
		d.allowed = false
		d.remaining = 0
		d.reset = rateLimitResetSeconds(d.wait)
		return d
	}
	d.allowed = true
	// Whole tokens left after consuming this request. TokensAt reflects the
	// bucket state right after Allow() debited a token; floor so we never
	// over-report headroom to a client.
	d.remaining = int(b.lim.TokensAt(l.now()))
	if d.remaining < 0 {
		d.remaining = 0
	}
	// Seconds until the bucket refills to full burst from its current level.
	if missing := cfg.Burst - d.remaining; missing > 0 && cfg.RatePerSecond > 0 {
		d.reset = rateLimitResetSeconds(time.Duration(float64(missing)/cfg.RatePerSecond * float64(time.Second)))
	}
	return d
}

// rateLimitResetSeconds renders a duration as a whole-second count for the
// RateLimit-Reset header (ceil, min 1 when there is any wait).
func rateLimitResetSeconds(d time.Duration) int {
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
			// Per-cluster aggregate ceiling first: a saturated tunnel must
			// reject every caller regardless of their individual quota, and
			// checking it first avoids spending a per-user token on a
			// request the cluster gate would reject anyway.
			clusterID := apiRateLimitClusterID(r)
			if d := limiter.allowClusterCeiling(class, clusterID); !d.allowed {
				writeRateLimited(w, d)
				return
			}
			key := apiRateLimitKey(r)
			d := limiter.allow(class, key)
			if !d.allowed {
				writeRateLimited(w, d)
				return
			}
			// Advertise the per-caller quota on the success path so clients
			// can self-pace before they ever hit a 429.
			setRateLimitHeaders(w, d)
			next.ServeHTTP(w, r)
		})
	}
}

// setRateLimitHeaders emits the IETF draft standard rate-limit headers
// (RateLimit-Limit / -Remaining / -Reset) for a decision that carries a
// known quota. Unknown-class (fail-open) decisions carry no quota and emit
// nothing.
func setRateLimitHeaders(w http.ResponseWriter, d rateLimitDecision) {
	if !d.known {
		return
	}
	h := w.Header()
	h.Set("RateLimit-Limit", strconv.Itoa(d.limit))
	h.Set("RateLimit-Remaining", strconv.Itoa(d.remaining))
	h.Set("RateLimit-Reset", strconv.Itoa(d.reset))
}

func writeRateLimited(w http.ResponseWriter, d rateLimitDecision) {
	setRateLimitHeaders(w, d)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Retry-After", formatAPIRetryAfter(d.wait))
	w.WriteHeader(http.StatusTooManyRequests)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"code":    "rate_limited",
		"message": "Too many requests for this endpoint class",
	})
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

// apiRateLimitClusterID extracts the target cluster from the request path
// for the aggregate per-cluster ceiling. Every tunnel-bound route that
// carries a ceiling is mounted under a {cluster_id} URL param, so chi has
// already populated it by the time this middleware runs (it sits inside
// the route's With-chain, after the route match). Returns "" when no
// cluster id is present — such requests are not subject to the per-cluster
// gate (the per-user limit still applies).
func apiRateLimitClusterID(r *http.Request) string {
	if r == nil {
		return ""
	}
	return strings.TrimSpace(chi.URLParam(r, "cluster_id"))
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
