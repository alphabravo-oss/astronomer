package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
)

// registerQuerierStub satisfies handler.ClusterQuerier via the embedded nil
// interface; GetManifestByToken only calls GetRegistrationTokenByToken, which
// we override to return an error so the handler responds 404 (below the limit)
// — letting us observe the 429 wrap (L3) without a DB.
type registerQuerierStub struct {
	handler.ClusterQuerier
}

func (registerQuerierStub) GetRegistrationTokenByToken(context.Context, string) (sqlc.ClusterRegistrationToken, error) {
	return sqlc.ClusterRegistrationToken{}, context.Canceled // any error -> 404
}

func TestRegisterManifestRouteIsRateLimited(t *testing.T) {
	cfg := &config.Config{TunnelRegisterRateLimitPerMinute: 3}
	router := NewRouter(cfg, RouterDependencies{
		Clusters: handler.NewClusterHandler(registerQuerierStub{}),
	})

	// The real one-liner fetch uses the `.yaml` suffix; the dot also keeps the
	// trailing-slash normalizer from rewriting the path (the handler strips
	// .yaml before the token lookup).
	const path = "/api/v1/register/sometoken.yaml"
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.RemoteAddr = "198.51.100.77:5000"
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("attempt %d: expected 404 from handler before limit, got %d", i+1, rec.Code)
		}
	}

	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.RemoteAddr = "198.51.100.77:5000"
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 4th request to be rate limited, got %d", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("expected Retry-After header on the 429")
	}

	// A different source IP is unaffected by the first IP's window.
	other := httptest.NewRequest(http.MethodGet, path, nil)
	other.RemoteAddr = "203.0.113.200:5000"
	orec := httptest.NewRecorder()
	router.ServeHTTP(orec, other)
	if orec.Code == http.StatusTooManyRequests {
		t.Fatal("an unrelated IP must not be throttled by another IP's window")
	}
}
