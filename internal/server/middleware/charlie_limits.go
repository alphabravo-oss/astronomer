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
	// One live SSE stream permanently holds a session slot. Leave headroom for
	// concurrent history/message/abort traffic and a brief reconnect overlap.
	charlieUserConcurrency    = 16
	charlieSessionConcurrency = 8
	charlieIPConcurrency      = 32
	charlieLimitMaxKeys       = 10_000
	// Short-request tokens must absorb history refreshes during a tool-heavy
	// turn without starving message POSTs. This is browser→API only; Product
	// MCP / cluster-agent paths never enter this middleware.
	charlieShortRequestRate  = 20.0 // per second
	charlieShortRequestBurst = 60
)

type charlieLimitBucket struct {
	lim      *rate.Limiter
	inFlight int
	lastSeen time.Time
}

// CharlieSessionLimits is retained for tests and optional future use. Live
// Charlie routes no longer install it: authenticated product chat in a joined
// installation is not request-rate-limited. Cluster-agent tunnels never use it.
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
			// Long-lived EventSource connections reconnect automatically. Counting
			// each open against the short-request token bucket turns a single 429
			// into a reconnect death spiral. Streams keep concurrency caps only.
			streamOpen := charlieIsEventStream(r)
			allowed := true
			for _, key := range keys {
				bucket := buckets[key]
				if bucket == nil {
					bucket = &charlieLimitBucket{lim: rate.NewLimiter(rate.Limit(charlieShortRequestRate), charlieShortRequestBurst)}
					buckets[key] = bucket
				}
				bucket.lastSeen = current
				if bucket.inFlight >= charlieConcurrencyForKey(key) {
					allowed = false
					continue
				}
				if !streamOpen && !bucket.lim.Allow() {
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

func charlieIsEventStream(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	path := r.URL.Path
	return strings.HasSuffix(path, "/events/") || strings.HasSuffix(path, "/events")
}
