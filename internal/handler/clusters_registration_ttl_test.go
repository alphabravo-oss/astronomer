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
)

// ttlCaptureQuerier records the TTL of every registration token minted so the
// L1-convergence test can assert all operator-facing mint paths honor the one
// configured RegistrationTokenTTLHours value.
type ttlCaptureQuerier struct {
	clusterRegistryTestQuerier
	lastTokenExpiry time.Time
}

func (q *ttlCaptureQuerier) CreateClusterRegistrationToken(_ context.Context, arg sqlc.CreateClusterRegistrationTokenParams) (sqlc.ClusterRegistrationToken, error) {
	q.lastTokenExpiry = arg.ExpiresAt
	return sqlc.ClusterRegistrationToken{ID: uuid.New(), ClusterID: arg.ClusterID, ExpiresAt: arg.ExpiresAt}, nil
}

// TestRegistrationTokenTTLConvergence asserts GenerateRegistrationToken (the
// old 24h path), GetManifest (the old 1h path), and GetSignedManifest (the old
// 1h sig-cap) all mint at the SINGLE configured TTL — default 1h, and an
// overridden value — instead of diverging.
func TestRegistrationTokenTTLConvergence(t *testing.T) {
	id := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")

	approxHours := func(t *testing.T, expiry time.Time, want time.Duration) {
		t.Helper()
		if expiry.IsZero() {
			t.Fatal("no registration token was minted")
		}
		d := time.Until(expiry)
		if d < want-2*time.Minute || d > want+2*time.Minute {
			t.Fatalf("minted token TTL = %s, want ~%s", d, want)
		}
	}

	// Default handler: NewClusterHandler defaults to 1h.
	t.Run("default-1h-generate", func(t *testing.T) {
		q := &ttlCaptureQuerier{}
		h := NewClusterHandler(q)
		r := chi.NewRouter()
		r.Post("/api/v1/clusters/{id}/register/", h.GenerateRegistrationToken)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/clusters/"+id.String()+"/register/", nil))
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201", rec.Code)
		}
		approxHours(t, q.lastTokenExpiry, time.Hour)
	})

	t.Run("default-1h-manifest", func(t *testing.T) {
		q := &ttlCaptureQuerier{}
		h := NewClusterHandler(q)
		r := chi.NewRouter()
		r.Get("/api/v1/clusters/{id}/manifest/", h.GetManifest)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/clusters/"+id.String()+"/manifest/", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		approxHours(t, q.lastTokenExpiry, time.Hour)
	})

	// Overridden TTL flows through every path identically.
	t.Run("override-3h-generate", func(t *testing.T) {
		q := &ttlCaptureQuerier{}
		h := NewClusterHandler(q)
		h.SetRegistrationTokenTTL(3 * time.Hour)
		r := chi.NewRouter()
		r.Post("/api/v1/clusters/{id}/register/", h.GenerateRegistrationToken)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/clusters/"+id.String()+"/register/", nil))
		approxHours(t, q.lastTokenExpiry, 3*time.Hour)
	})

	// GetSignedManifest mints min(remaining-sig-window, TTL). The signed-URL
	// window itself is capped by maxSignedManifestTTL (30m), so these stay
	// sub-30m. Window (15m) < TTL (3h) -> the window wins (~15m).
	t.Run("signed-manifest-caps-at-window-under-ttl", func(t *testing.T) {
		q := &ttlCaptureQuerier{}
		h := NewClusterHandler(q)
		h.SetRegistrationTokenTTL(3 * time.Hour)
		h.SetManifestSigningSecret("test-secret")
		signed := h.SignManifestURL(id, 15*time.Minute)
		r := chi.NewRouter()
		r.Get("/api/v1/register/signed/{cluster_id}", h.GetSignedManifest)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, signed, nil)
		req.RemoteAddr = "203.0.113.9:5555"
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		approxHours(t, q.lastTokenExpiry, 15*time.Minute)
	})

	// TTL (10m) < window (25m) -> the TTL is the ceiling (~10m), proving the
	// flat 1h was replaced by the configurable knob.
	t.Run("signed-manifest-capped-by-ttl", func(t *testing.T) {
		q := &ttlCaptureQuerier{}
		h := NewClusterHandler(q)
		h.SetRegistrationTokenTTL(10 * time.Minute)
		h.SetManifestSigningSecret("test-secret")
		signed := h.SignManifestURL(id, 25*time.Minute)
		r := chi.NewRouter()
		r.Get("/api/v1/register/signed/{cluster_id}", h.GetSignedManifest)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, signed, nil)
		req.RemoteAddr = "203.0.113.10:5555"
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		approxHours(t, q.lastTokenExpiry, 10*time.Minute)
	})

	// SetRegistrationTokenTTL clamps non-positive values to the 1h default.
	t.Run("clamp-nonpositive", func(t *testing.T) {
		q := &ttlCaptureQuerier{}
		h := NewClusterHandler(q)
		h.SetRegistrationTokenTTL(0)
		r := chi.NewRouter()
		r.Post("/api/v1/clusters/{id}/register/", h.GenerateRegistrationToken)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/clusters/"+id.String()+"/register/", nil))
		approxHours(t, q.lastTokenExpiry, time.Hour)
	})
}
