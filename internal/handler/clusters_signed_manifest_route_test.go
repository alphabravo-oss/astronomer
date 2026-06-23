package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	appmiddleware "github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

// signedManifestCaptureQuerier reuses the full ClusterQuerier fake and
// records the TTL of the registration token GetSignedManifest mints, so
// the test can assert it's capped to the signature window.
type signedManifestCaptureQuerier struct {
	clusterRegistryTestQuerier
	lastTokenExpiry time.Time
}

func (q *signedManifestCaptureQuerier) CreateClusterRegistrationToken(_ context.Context, arg sqlc.CreateClusterRegistrationTokenParams) (sqlc.ClusterRegistrationToken, error) {
	q.lastTokenExpiry = arg.ExpiresAt
	return sqlc.ClusterRegistrationToken{}, nil
}

// TestSignedManifestRateLimitAndTokenCap covers the hardening pass:
// (a) the IP-keyed rate limit trips after N requests, and the minted
// registration token TTL is capped to the remaining signature window.
func TestSignedManifestRateLimitAndTokenCap(t *testing.T) {
	q := &signedManifestCaptureQuerier{}
	h := NewClusterHandler(q)
	h.SetManifestSigningSecret("test-secret")

	id := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	signed := h.SignManifestURL(id, 15*time.Minute)
	if signed == "" {
		t.Fatal("SignManifestURL returned empty")
	}

	// Wire the route exactly as routes.go does: rate-limit middleware in
	// front of GetSignedManifest.
	r := chi.NewRouter()
	r.With(appmiddleware.LoginRateLimit(5, time.Minute)).
		Get("/api/v1/register/signed/{cluster_id}", h.GetSignedManifest)

	const fromIP = "203.0.113.7:5555"
	do := func() int {
		req := httptest.NewRequest(http.MethodGet, signed, nil)
		req.RemoteAddr = fromIP
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Code
	}

	// First 5 from the same IP pass the limiter (200 OK).
	for i := 0; i < 5; i++ {
		if code := do(); code != http.StatusOK {
			t.Fatalf("request %d: code = %d, want 200", i+1, code)
		}
	}
	// 6th trips the limiter.
	if code := do(); code != http.StatusTooManyRequests {
		t.Fatalf("6th request code = %d, want 429", code)
	}

	// Token TTL was capped to the signature window (~15m), not a flat 1h.
	if q.lastTokenExpiry.IsZero() {
		t.Fatal("no registration token was minted")
	}
	if d := time.Until(q.lastTokenExpiry); d > 20*time.Minute {
		t.Fatalf("minted token TTL = %s, want capped to the ~15m signature window", d)
	}
}
