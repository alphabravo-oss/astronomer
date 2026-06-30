package tunnel2

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/tunnel"
)

func TestRateLimitMiddlewareRejects429WhenBlocked(t *testing.T) {
	rs := NewRemoteServer(nil, nil)
	lim := tunnel.NewConnectFailureLimiter(3, time.Minute, nil)
	rs.SetConnectLimiter(lim)
	for i := 0; i < 3; i++ {
		lim.Fail("192.0.2.1") // httptest default RemoteAddr host
	}

	called := false
	h := rs.RateLimitMiddleware()(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/connect/c1/", nil))

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rec.Code)
	}
	if called {
		t.Fatal("next handler must not run for a blocked IP")
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("expected Retry-After header")
	}
}

func TestRateLimitMiddlewarePassesThroughWhenNoLimiter(t *testing.T) {
	rs := NewRemoteServer(nil, nil) // no limiter wired

	called := false
	h := rs.RateLimitMiddleware()(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/connect/c1/", nil))

	if !called {
		t.Fatal("nil limiter must pass through to the next handler")
	}
}

// TestNilValidatorFailsClosed (L18): a RemoteServer with no validator must
// REJECT connections by default (a mis-wired prod server can't accept
// unauthenticated tunnels); only an explicit insecure opt-in accepts.
func TestNilValidatorFailsClosed(t *testing.T) {
	rs := NewRemoteServer(nil, nil) // nil validator
	authz := rs.authorize(nil)
	req := httptest.NewRequest("GET", "/api/v1/connect/c1/", nil)
	req.Header.Set(HeaderClusterID, "c1")
	req.Header.Set("Authorization", "Bearer some-token")

	if _, ok, err := authz(req); ok || err == nil {
		t.Fatalf("nil validator must fail closed (reject), got ok=%v err=%v", ok, err)
	}

	rs.SetAllowInsecureNilValidator(true)
	authz2 := rs.authorize(nil)
	if _, ok, err := authz2(req); !ok || err != nil {
		t.Fatalf("with insecure opt-in, nil validator should accept, got ok=%v err=%v", ok, err)
	}
}
