package middleware

import (
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

func (l *loginRateLimiter) allow(key string) (allowed bool, retryAfter time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	bucket, ok := l.buckets[key]
	if !ok || !now.Before(bucket.resetAt) {
		l.buckets[key] = loginRateLimitBucket{
			count:   1,
			resetAt: now.Add(l.window),
		}
		return true, 0
	}

	if bucket.count >= l.limit {
		return false, bucket.resetAt.Sub(now)
	}

	bucket.count++
	l.buckets[key] = bucket
	return true, 0
}

// LoginRateLimit applies a small per-client cap to login attempts to slow
// brute-force attacks against the password endpoint.
func LoginRateLimit(limit int, window time.Duration) func(http.Handler) http.Handler {
	return newLoginRateLimitMiddleware(limit, window, time.Now)
}

func newLoginRateLimitMiddleware(limit int, window time.Duration, now func() time.Time) func(http.Handler) http.Handler {
	limiter := newLoginRateLimiter(limit, window, now)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := clientKey(r)
			if key == "" {
				key = "unknown"
			}
			allowed, retryAfter := limiter.allow(key)
			if allowed {
				next.ServeHTTP(w, r)
				return
			}

			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", formatRetryAfter(retryAfter))
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"code":    "rate_limited",
				"message": "Too many login attempts",
			})
		})
	}
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
