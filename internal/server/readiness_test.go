package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeDBHealth struct {
	err error
}

func (f fakeDBHealth) Health(context.Context) error { return f.err }

type fakeQueuePing struct {
	err error
}

func (f fakeQueuePing) Ping() error { return f.err }

type fakeHubStatus struct {
	clusters []string
}

type fakeSecurityCacheHealth struct{ started, healthy bool }

func (f fakeSecurityCacheHealth) Started() bool { return f.started }
func (f fakeSecurityCacheHealth) Healthy() bool { return f.healthy }

func (f fakeHubStatus) ConnectedClusters() []string { return f.clusters }

func TestReadinessHandlerOK(t *testing.T) {
	h := newReadinessHandler(fakeDBHealth{}, fakeQueuePing{}, fakeHubStatus{clusters: []string{"a", "b"}})

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("expected status ok, got %v", body["status"])
	}
	checks := body["checks"].(map[string]any)
	tunnelHub := checks["tunnel_hub"].(map[string]any)
	if got := int(tunnelHub["connected_clusters"].(float64)); got != 2 {
		t.Fatalf("expected connected_clusters=2, got %d", got)
	}
}

// fakeDBSchema implements both Health and SchemaVersion so the C-03 gate runs.
type fakeDBSchema struct {
	version int64
	dirty   bool
	err     error
}

func (f fakeDBSchema) Health(context.Context) error { return nil }
func (f fakeDBSchema) SchemaVersion(context.Context) (int64, bool, error) {
	return f.version, f.dirty, f.err
}

// TestReadinessSchemaVersionGate (C-03): the pod stays out of rotation until the
// applied schema reaches the binary's embedded floor.
func TestReadinessSchemaVersionGate(t *testing.T) {
	t.Run("stale schema 503s", func(t *testing.T) {
		h := newReadinessHandler(fakeDBSchema{version: 5}, fakeQueuePing{}, fakeHubStatus{})
		h.expectedSchemaVersion = 131
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("stale schema should 503, got %d", rec.Code)
		}
		var body map[string]any
		_ = json.NewDecoder(rec.Body).Decode(&body)
		sv, ok := body["checks"].(map[string]any)["schema_version"].(map[string]any)
		if !ok || sv["ok"] != false {
			t.Fatalf("expected failing schema_version check, got %v", body["checks"])
		}
	})

	t.Run("current schema ready", func(t *testing.T) {
		h := newReadinessHandler(fakeDBSchema{version: 131}, fakeQueuePing{}, fakeHubStatus{clusters: []string{"a"}})
		h.expectedSchemaVersion = 131
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("current schema should be ready, got %d", rec.Code)
		}
	})

	// dirty=true at the expected version means the last migration is mid-flight
	// or crashed mid-DDL: the pod must stay out of rotation even though
	// version >= floor, so it never serves a half-applied schema.
	t.Run("dirty at expected version 503s", func(t *testing.T) {
		h := newReadinessHandler(fakeDBSchema{version: 131, dirty: true}, fakeQueuePing{}, fakeHubStatus{clusters: []string{"a"}})
		h.expectedSchemaVersion = 131
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("dirty schema should 503, got %d", rec.Code)
		}
		var body map[string]any
		_ = json.NewDecoder(rec.Body).Decode(&body)
		sv, ok := body["checks"].(map[string]any)["schema_version"].(map[string]any)
		if !ok || sv["ok"] != false {
			t.Fatalf("expected failing schema_version check, got %v", body["checks"])
		}
		if sv["error"] != "schema migration in progress or failed (dirty)" {
			t.Fatalf("schema_version.error = %v, want dirty message", sv["error"])
		}
	})
}

func TestReadinessHandlerDependencyFailure(t *testing.T) {
	h := newReadinessHandler(fakeDBHealth{err: errors.New("db down")}, fakeQueuePing{err: errors.New("redis down")}, fakeHubStatus{})

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d", rec.Code)
	}

	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["status"] != "not_ready" {
		t.Fatalf("expected status not_ready, got %v", body["status"])
	}
	checks := body["checks"].(map[string]any)
	if dbCheck := checks["database"].(map[string]any); dbCheck["error"] != "db down" {
		t.Fatalf("expected database error, got %v", dbCheck)
	}
	if redisCheck := checks["redis"].(map[string]any); redisCheck["error"] != "redis down" {
		t.Fatalf("expected redis error, got %v", redisCheck)
	}
}

func TestReadinessRequiresInitialSecurityCacheEpochStateInHA(t *testing.T) {
	for _, tt := range []struct {
		name     string
		provider securityCacheHealthProvider
		want     int
	}{
		{name: "missing", provider: nil, want: http.StatusServiceUnavailable},
		{name: "not started", provider: fakeSecurityCacheHealth{}, want: http.StatusServiceUnavailable},
		{name: "unhealthy", provider: fakeSecurityCacheHealth{started: true}, want: http.StatusServiceUnavailable},
		{name: "healthy", provider: fakeSecurityCacheHealth{started: true, healthy: true}, want: http.StatusOK},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newReadinessHandler(fakeDBHealth{}, fakeQueuePing{}, fakeHubStatus{}).
				withSecurityCacheCoordinator(tt.provider, true)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d", rec.Code, tt.want)
			}
		})
	}

	local := newReadinessHandler(fakeDBHealth{}, fakeQueuePing{}, fakeHubStatus{}).
		withSecurityCacheCoordinator(nil, false)
	rec := httptest.NewRecorder()
	local.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("single-replica local-only readiness = %d, want 200", rec.Code)
	}
}

// When the hub is nil (misconfigured wiring), the
// readiness probe must report 503 instead of silently returning OK.
// Otherwise a pod that can never serve tunnel traffic stays in Service
// rotation indefinitely.
func TestReadinessHandlerNilHubFailsClosed(t *testing.T) {
	h := newReadinessHandler(fakeDBHealth{}, fakeQueuePing{}, nil)

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 with nil hub, got %d", rec.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	checks := body["checks"].(map[string]any)
	tunnelHub, ok := checks["tunnel_hub"].(map[string]any)
	if !ok {
		t.Fatal("tunnel_hub check missing from body")
	}
	if tunnelHub["ok"] != false {
		t.Fatalf("tunnel_hub.ok = %v, want false", tunnelHub["ok"])
	}
	if tunnelHub["error"] != "tunnel hub not initialized" {
		t.Fatalf("tunnel_hub.error = %v, want 'tunnel hub not initialized'", tunnelHub["error"])
	}
}

// TestReadinessHandlerLocatorMisconfig (L19): a multi-replica deployment with a
// missing ASTRONOMER_POD_IP fails readiness loudly via the tunnel_locator check.
func TestReadinessHandlerLocatorMisconfig(t *testing.T) {
	h := newReadinessHandler(fakeDBHealth{}, fakeQueuePing{}, fakeHubStatus{}).
		withLocatorError("ASTRONOMER_POD_IP is unset on a multi-replica deployment")

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 for HA-misconfigured locator, got %d", rec.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	loc, ok := body["checks"].(map[string]any)["tunnel_locator"].(map[string]any)
	if !ok || loc["ok"] != false {
		t.Fatalf("expected failing tunnel_locator check, got %v", body["checks"])
	}

	// And the healthy single-replica path (no locator error) stays ready.
	ok2 := newReadinessHandler(fakeDBHealth{}, fakeQueuePing{}, fakeHubStatus{}).withLocatorError("")
	rec2 := httptest.NewRecorder()
	ok2.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec2.Code != http.StatusOK {
		t.Fatalf("healthy locator (no error) should be ready, got %d", rec2.Code)
	}
}
