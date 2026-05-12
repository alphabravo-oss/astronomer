package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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

// Unknown classes fail open — better than locking the whole API if a
// route is wired with a typo'd class name.
func TestAPIRateLimiter_UnknownClassAllows(t *testing.T) {
	lim := newAPIRateLimiter(map[APIRateLimitClass]APIRateLimitConfig{}, nil)
	allowed, _ := lim.allow("typo-class", "anyone")
	if !allowed {
		t.Error("unknown class must fail open, not block")
	}
}
