package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func TestLoginRateLimiterAllowAndReset(t *testing.T) {
	now := time.Unix(0, 0)
	limiter := newLoginRateLimiter(2, time.Minute, func() time.Time { return now })

	if allowed, _ := limiter.allow("127.0.0.1"); !allowed {
		t.Fatal("first attempt should pass")
	}
	if allowed, _ := limiter.allow("127.0.0.1"); !allowed {
		t.Fatal("second attempt should pass")
	}
	if allowed, retryAfter := limiter.allow("127.0.0.1"); allowed || retryAfter <= 0 {
		t.Fatalf("third attempt should be blocked with retryAfter>0, got allowed=%v retryAfter=%v", allowed, retryAfter)
	}

	now = now.Add(time.Minute + time.Second)
	if allowed, _ := limiter.allow("127.0.0.1"); !allowed {
		t.Fatal("attempt after window reset should pass")
	}
}

func TestLoginRateLimitMiddlewareReturnsJSON429(t *testing.T) {
	now := time.Unix(0, 0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mw := newLoginRateLimitMiddleware(ctx, 1, time.Minute, func() time.Time { return now })
	nextCalls := 0
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalls++
		w.WriteHeader(http.StatusNoContent)
	}))

	first := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login/", nil)
	first.RemoteAddr = "198.51.100.10:1234"
	firstRec := httptest.NewRecorder()
	handler.ServeHTTP(firstRec, first)
	if firstRec.Code != http.StatusNoContent {
		t.Fatalf("expected first status 204, got %d", firstRec.Code)
	}

	second := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login/", nil)
	second.RemoteAddr = "198.51.100.10:5678"
	secondRec := httptest.NewRecorder()
	handler.ServeHTTP(secondRec, second)

	if secondRec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected second status 429, got %d", secondRec.Code)
	}
	if got := secondRec.Header().Get("Retry-After"); got != "60" {
		t.Fatalf("expected Retry-After=60, got %q", got)
	}
	if nextCalls != 1 {
		t.Fatalf("expected next handler to be called once, got %d", nextCalls)
	}

	var body map[string]string
	if err := json.NewDecoder(secondRec.Body).Decode(&body); err != nil {
		t.Fatalf("decode 429 body: %v", err)
	}
	if body["code"] != "rate_limited" {
		t.Fatalf("expected code=rate_limited, got %q", body["code"])
	}
}

func TestClientKeyUsesHostPart(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login/", nil)
	req.RemoteAddr = "203.0.113.5:4321"
	if got := clientKey(req); got != "203.0.113.5" {
		t.Fatalf("clientKey=%q, want 203.0.113.5", got)
	}
}

// Bucket-map leak guard: after a flood of distinct keys, expired buckets
// must be evicted by the janitor. Without this, the map grows unboundedly
// over the process lifetime — every distinct client IP that ever hit
// /auth/login leaves a row. The bug was caught by the 2026-05-11
// enterprise audit (FEATURES-051126 T09).
func TestLoginRateLimiterEvictsExpiredBuckets(t *testing.T) {
	now := time.Unix(0, 0)
	limiter := newLoginRateLimiter(5, time.Minute, func() time.Time { return now })

	// Populate 1000 buckets — simulating a churning fleet of distinct
	// client IPs.
	for i := 0; i < 1000; i++ {
		limiter.allow(strconv.Itoa(i))
	}
	limiter.mu.Lock()
	before := len(limiter.buckets)
	limiter.mu.Unlock()
	if before != 1000 {
		t.Fatalf("setup: expected 1000 buckets, got %d", before)
	}

	// Jump past the window so every bucket is expired.
	now = now.Add(2 * time.Minute)

	if evicted := limiter.evictExpired(); evicted != 1000 {
		t.Errorf("evictExpired returned %d, want 1000", evicted)
	}
	limiter.mu.Lock()
	after := len(limiter.buckets)
	limiter.mu.Unlock()
	if after != 0 {
		t.Errorf("after eviction: %d buckets remain, want 0", after)
	}
}
