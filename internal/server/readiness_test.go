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
