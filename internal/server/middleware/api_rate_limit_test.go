package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

// The per-class limiter must reject calls past the
// burst, return 429 + Retry-After, and let calls through once the bucket
// refills.
func TestAPIRateLimitMiddleware_BurstThenBlock(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1 req/sec average, burst 2 — a caller can do 2 in quick succession,
	// then has to wait ~1s for the next token.
	configs := map[APIRateLimitClass]APIRateLimitConfig{
		"test": {RatePerSecond: 1.0, Burst: 2},
	}
	mw := apiRateLimitWith(ctx, "test", configs, time.Now)

	called := 0
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		w.WriteHeader(http.StatusNoContent)
	}))

	do := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/anything", nil)
		req.RemoteAddr = "10.0.0.1:1234"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	// First two requests: allowed (burst).
	for i := 0; i < 2; i++ {
		rec := do()
		if rec.Code != http.StatusNoContent {
			t.Fatalf("call %d: status=%d, want 204", i+1, rec.Code)
		}
	}
	if called != 2 {
		t.Fatalf("expected next handler called twice, got %d", called)
	}

	// Third: blocked.
	rec := do()
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("third call should be 429, got %d", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("429 must include Retry-After header")
	}
	if called != 2 {
		t.Errorf("blocked call should NOT reach handler, called=%d", called)
	}
}

// Distinct callers (different keys) must not share buckets. The test
// uses different RemoteAddr values to drive the key derivation.
func TestAPIRateLimitMiddleware_PerCallerBuckets(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	configs := map[APIRateLimitClass]APIRateLimitConfig{
		"test": {RatePerSecond: 0.001, Burst: 1}, // effectively one shot per caller
	}
	mw := apiRateLimitWith(ctx, "test", configs, time.Now)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	hit := func(remote string, wantStatus int) {
		req := httptest.NewRequest(http.MethodGet, "/anything", nil)
		req.RemoteAddr = remote
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != wantStatus {
			t.Errorf("remote=%s wanted %d got %d", remote, wantStatus, rec.Code)
		}
	}

	// Caller A: 1st allowed, 2nd blocked.
	hit("10.0.0.1:1", http.StatusNoContent)
	hit("10.0.0.1:2", http.StatusTooManyRequests)

	// Caller B: 1st allowed (independent bucket), 2nd blocked.
	hit("10.0.0.2:1", http.StatusNoContent)
	hit("10.0.0.2:2", http.StatusTooManyRequests)
}

// Bucket eviction must clear idle entries — same memory-leak fix as the
// login limiter.
func TestAPIRateLimiter_EvictExpired(t *testing.T) {
	now := time.Unix(0, 0)
	cfgs := map[APIRateLimitClass]APIRateLimitConfig{
		"test": {RatePerSecond: 1.0, Burst: 1},
	}
	lim := newAPIRateLimiter(cfgs, func() time.Time { return now })

	// Populate.
	for i := 0; i < 100; i++ {
		lim.allow("test", "client-"+string(rune('a'+i%26))+"-"+string(rune('0'+i/26)))
	}
	lim.mu.Lock()
	before := len(lim.buckets)
	lim.mu.Unlock()
	if before == 0 {
		t.Fatal("setup: no buckets created")
	}

	// Jump past the eviction window.
	now = now.Add(11 * time.Minute)

	evicted := lim.evictExpired()
	if evicted != before {
		t.Errorf("evicted=%d, want %d", evicted, before)
	}
	lim.mu.Lock()
	after := len(lim.buckets)
	lim.mu.Unlock()
	if after != 0 {
		t.Errorf("post-eviction map size = %d, want 0", after)
	}
}

// G1 (M5): the aggregate per-cluster ceiling must bound the COMBINED load
// of many distinct in-policy users hammering ONE cluster's tunnel, while a
// second cluster — even hit by the same users — stays fully unaffected.
//
// This is the negative test for the finding: previously every limiter key
// was "<class>:u:<userID>" (per-user-global, no per-cluster dimension), so
// N users could each stay within their own quota yet collectively saturate
// a single agent tunnel. With the per-cluster ceiling in place that attack
// must now fail (cluster A trips) without collateral damage (cluster B
// untouched).
func TestAPIRateLimit_PerClusterCeiling_ManyUsersOnOneCluster(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const class APIRateLimitClass = ClassK8sProxy

	// Generous per-user limit so the per-user gate never trips in this
	// test: any blocking must come purely from the per-cluster ceiling,
	// proving the new aggregate dimension (not the old per-user one) is
	// doing the work.
	perUser := map[APIRateLimitClass]APIRateLimitConfig{
		class: {RatePerSecond: 1000, Burst: 1000},
	}
	// Aggregate per-cluster cap: 5 combined requests, then backpressure.
	ceilings := map[APIRateLimitClass]APIRateLimitConfig{
		class: {RatePerSecond: 0.001, Burst: 5},
	}

	limiter := newAPIRateLimiterWithCeilings(perUser, ceilings, time.Now)
	limiter.startJanitor(ctx, 0)

	mw := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if allowed, wait := limiter.allowClusterCeiling(class, apiRateLimitClusterID(r)); !allowed {
				writeRateLimited(w, wait)
				return
			}
			if allowed, wait := limiter.allow(class, apiRateLimitKey(r)); !allowed {
				writeRateLimited(w, wait)
				return
			}
			next.ServeHTTP(w, r)
		})
	}

	// Mount on a chi router so chi.URLParam(r, "cluster_id") resolves from
	// the path, exactly as in production routing.
	router := chi.NewRouter()
	router.With(mw).Get("/api/v1/clusters/{cluster_id}/k8s/*", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	// hit issues one request as `userID` against `clusterID`.
	hit := func(clusterID, userID string) int {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/"+clusterID+"/k8s/api/v1/pods", nil)
		req = req.WithContext(context.WithValue(req.Context(), userContextKey, &AuthenticatedUser{ID: userID}))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec.Code
	}

	// Attack: 20 distinct users each fire one request at cluster A. Each
	// user is well within its own per-user quota, but the aggregate
	// per-cluster ceiling (burst 5) must trip once combined load exceeds 5.
	clusterA := "11111111-1111-1111-1111-111111111111"
	allowedA, blockedA := 0, 0
	for i := 0; i < 20; i++ {
		switch hit(clusterA, "user-"+strconv.Itoa(i)) {
		case http.StatusNoContent:
			allowedA++
		case http.StatusTooManyRequests:
			blockedA++
		}
	}
	if allowedA != 5 {
		t.Errorf("cluster A: expected exactly 5 allowed (the burst ceiling), got %d", allowedA)
	}
	if blockedA != 15 {
		t.Errorf("cluster A: expected 15 blocked once the aggregate ceiling tripped, got %d", blockedA)
	}

	// Cluster B is a DIFFERENT cluster: it keeps an independent bucket, so
	// even the same users that just saturated cluster A get through here.
	clusterB := "22222222-2222-2222-2222-222222222222"
	for i := 0; i < 5; i++ {
		if got := hit(clusterB, "user-"+strconv.Itoa(i)); got != http.StatusNoContent {
			t.Errorf("cluster B request %d should be allowed (independent bucket), got %d", i+1, got)
		}
	}
}

// End-to-end through the real APIRateLimit middleware (apiRateLimitWith):
// the production ceiling for ClassK8sProxy must trip under combined
// many-user load on one cluster when mounted on a chi route, while a
// different cluster stays clear. This guards the actual wiring, not just
// the limiter primitives.
func TestAPIRateLimitMiddleware_PerClusterCeiling_EndToEnd(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Use the production default per-class + per-cluster ceiling configs
	// (passing nil selects defaultLimits / defaultClusterCeilings).
	mw := apiRateLimitWith(ctx, ClassK8sProxy, nil, time.Now)

	router := chi.NewRouter()
	router.With(mw).Get("/api/v1/clusters/{cluster_id}/k8s/*", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	hit := func(clusterID, userID string) int {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/"+clusterID+"/k8s/api/v1/pods", nil)
		req = req.WithContext(context.WithValue(req.Context(), userContextKey, &AuthenticatedUser{ID: userID}))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec.Code
	}

	// Production ClassK8sProxy ceiling burst is 60. Fire 200 requests from
	// 200 distinct users at cluster A; the aggregate ceiling must reject at
	// least some once burst is exhausted.
	clusterA := "aaaaaaaa-0000-0000-0000-000000000001"
	allowedA, blockedA := 0, 0
	for i := 0; i < 200; i++ {
		switch hit(clusterA, "user-"+strconv.Itoa(i)) {
		case http.StatusNoContent:
			allowedA++
		case http.StatusTooManyRequests:
			blockedA++
		}
	}
	if blockedA == 0 {
		t.Fatalf("cluster A: aggregate per-cluster ceiling never tripped under 200-user load (allowed=%d)", allowedA)
	}
	if allowedA == 0 {
		t.Fatalf("cluster A: expected some requests within the burst to pass, got 0")
	}

	// A second cluster, hit by the same users, must still admit traffic up
	// to its own (independent) burst — proving isolation.
	clusterB := "bbbbbbbb-0000-0000-0000-000000000002"
	if got := hit(clusterB, "user-0"); got != http.StatusNoContent {
		t.Errorf("cluster B first request should pass on its independent bucket, got %d", got)
	}
}

// The per-cluster ceiling is an ADDITIONAL gate, not a replacement: a
// single user still hits their own per-user limit first when that limit is
// the tighter of the two. This guards against accidentally dropping the
// existing per-user defense-in-depth control.
func TestAPIRateLimit_PerUserLimitStillEnforced(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const class APIRateLimitClass = ClassK8sProxy

	perUser := map[APIRateLimitClass]APIRateLimitConfig{
		class: {RatePerSecond: 0.001, Burst: 2}, // tight per-user
	}
	ceilings := map[APIRateLimitClass]APIRateLimitConfig{
		class: {RatePerSecond: 1000, Burst: 1000}, // generous cluster cap
	}

	limiter := newAPIRateLimiterWithCeilings(perUser, ceilings, time.Now)
	limiter.startJanitor(ctx, 0)

	mw := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if allowed, wait := limiter.allowClusterCeiling(class, apiRateLimitClusterID(r)); !allowed {
				writeRateLimited(w, wait)
				return
			}
			if allowed, wait := limiter.allow(class, apiRateLimitKey(r)); !allowed {
				writeRateLimited(w, wait)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
	router := chi.NewRouter()
	router.With(mw).Get("/api/v1/clusters/{cluster_id}/k8s/*", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	hit := func() int {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/cid/k8s/api/v1/pods", nil)
		req = req.WithContext(context.WithValue(req.Context(), userContextKey, &AuthenticatedUser{ID: "solo-user"}))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec.Code
	}

	// One user, well under the cluster ceiling, still trips their own
	// per-user burst of 2.
	if hit() != http.StatusNoContent {
		t.Fatal("1st per-user request should pass")
	}
	if hit() != http.StatusNoContent {
		t.Fatal("2nd per-user request should pass")
	}
	if got := hit(); got != http.StatusTooManyRequests {
		t.Fatalf("3rd request must be blocked by the per-user limit, got %d", got)
	}
}

// Unknown classes fail open — better than locking the whole API if a
// route is wired with a typo'd class name.
func TestAPIRateLimiter_UnknownClassAllows(t *testing.T) {
	lim := newAPIRateLimiter(map[APIRateLimitClass]APIRateLimitConfig{}, nil)
	allowed, _ := lim.allow("typo-class", "anyone")
	if !allowed {
		t.Error("unknown class must fail open, not block")
	}
}
