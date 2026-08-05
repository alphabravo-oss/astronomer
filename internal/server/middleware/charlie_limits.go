package middleware

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/time/rate"
)

const (
	charlieUserConcurrency    = 4
	charlieSessionConcurrency = 2
	charlieIPConcurrency      = 8
	charlieLimitMaxKeys       = 10_000
)

type charlieLimitBucket struct {
	lim      *rate.Limiter
	inFlight int
	lastSeen time.Time
}

// CharlieSessionLimits independently bounds each authenticated user, local
// session, and client IP. This product-local protection complements the
// deployment-wide Product Bridge limiter; it does not affect any core API.
func CharlieSessionLimits() func(http.Handler) http.Handler {
	var mu sync.Mutex
	buckets := make(map[string]*charlieLimitBucket)
	now := time.Now
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			keys := charlieLimitKeys(r)
			mu.Lock()
			current := now()
			if len(buckets) > charlieLimitMaxKeys {
				for key, bucket := range buckets {
					if bucket.inFlight == 0 && current.Sub(bucket.lastSeen) > 10*time.Minute {
						delete(buckets, key)
					}
				}
			}
			allowed := true
			for _, key := range keys {
				bucket := buckets[key]
				if bucket == nil {
					bucket = &charlieLimitBucket{lim: rate.NewLimiter(rate.Limit(30.0/60.0), 10)}
					buckets[key] = bucket
				}
				bucket.lastSeen = current
				if !bucket.lim.Allow() || bucket.inFlight >= charlieConcurrencyForKey(key) {
					allowed = false
				}
			}
			if allowed {
				for _, key := range keys {
					buckets[key].inFlight++
				}
			}
			mu.Unlock()
			if !allowed {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", "2")
				w.WriteHeader(http.StatusTooManyRequests)
				_ = json.NewEncoder(w).Encode(map[string]string{"code": "charlie_rate_limited", "message": "Charlie request capacity is temporarily exhausted"})
				return
			}
			defer func() {
				mu.Lock()
				for _, key := range keys {
					if bucket := buckets[key]; bucket != nil && bucket.inFlight > 0 {
						bucket.inFlight--
						bucket.lastSeen = now()
					}
				}
				mu.Unlock()
			}()
			next.ServeHTTP(w, r)
		})
	}
}

func charlieLimitKeys(r *http.Request) []string {
	keys := make([]string, 0, 3)
	if user, ok := GetAuthenticatedUser(r.Context()); ok && user != nil && strings.TrimSpace(user.ID) != "" {
		keys = append(keys, "user:"+user.ID)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if host == "" {
		host = "unknown"
	}
	keys = append(keys, "ip:"+host)
	if sessionID := strings.TrimSpace(chi.URLParam(r, "session_id")); sessionID != "" {
		keys = append(keys, "session:"+sessionID)
	}
	return keys
}

func charlieConcurrencyForKey(key string) int {
	switch {
	case strings.HasPrefix(key, "user:"):
		return charlieUserConcurrency
	case strings.HasPrefix(key, "session:"):
		return charlieSessionConcurrency
	default:
		return charlieIPConcurrency
	}
}
