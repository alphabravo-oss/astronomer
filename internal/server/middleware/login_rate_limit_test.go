package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	mw := newLoginRateLimitMiddleware(1, time.Minute, func() time.Time { return now })
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
