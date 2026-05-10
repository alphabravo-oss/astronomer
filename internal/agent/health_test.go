package agent

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

func TestNewHealthReporter(t *testing.T) {
	log := slog.Default()

	t.Run("default intervals", func(t *testing.T) {
		hr := NewHealthReporter(nil, log, 0, 0)
		if hr.heartbeatInterval.Seconds() != 30 {
			t.Errorf("expected heartbeat interval 30s, got %v", hr.heartbeatInterval)
		}
		if hr.metricsInterval.Seconds() != 60 {
			t.Errorf("expected metrics interval 60s, got %v", hr.metricsInterval)
		}
	})

	t.Run("custom intervals", func(t *testing.T) {
		hr := NewHealthReporter(nil, log, 10, 120)
		if hr.heartbeatInterval.Seconds() != 10 {
			t.Errorf("expected heartbeat interval 10s, got %v", hr.heartbeatInterval)
		}
		if hr.metricsInterval.Seconds() != 120 {
			t.Errorf("expected metrics interval 120s, got %v", hr.metricsInterval)
		}
	})

	t.Run("negative intervals use defaults", func(t *testing.T) {
		hr := NewHealthReporter(nil, log, -5, -10)
		if hr.heartbeatInterval.Seconds() != 30 {
			t.Errorf("expected heartbeat interval 30s, got %v", hr.heartbeatInterval)
		}
		if hr.metricsInterval.Seconds() != 60 {
			t.Errorf("expected metrics interval 60s, got %v", hr.metricsInterval)
		}
	})
}

func TestHealthEndpoints(t *testing.T) {
	log := slog.Default()
	hr := NewHealthReporter(nil, log, 30, 60)
	hr.SetClusterID("cluster-abc")
	mux := hr.healthMux()

	decode := func(t *testing.T, body string) map[string]any {
		t.Helper()
		var out map[string]any
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			t.Fatalf("body is not JSON: %v (body=%q)", err, body)
		}
		return out
	}

	t.Run("healthz returns 200 JSON", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", w.Code)
		}
		if got := w.Header().Get("Content-Type"); got != "application/json" {
			t.Errorf("expected JSON content type, got %q", got)
		}
		body := decode(t, w.Body.String())
		if body["status"] != "ok" {
			t.Errorf("expected status=ok, got %v", body["status"])
		}
		if body["cluster_id"] != "cluster-abc" {
			t.Errorf("expected cluster_id=cluster-abc, got %v", body["cluster_id"])
		}
		if _, ok := body["uptime_seconds"]; !ok {
			t.Error("expected uptime_seconds field present")
		}
	})

	t.Run("readyz returns 503 JSON when not connected", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("expected status 503, got %d", w.Code)
		}
		body := decode(t, w.Body.String())
		if body["status"] != "not_connected" {
			t.Errorf("expected status=not_connected, got %v", body["status"])
		}
	})

	t.Run("readyz returns 200 JSON when connected", func(t *testing.T) {
		hr.SetConnected(true)
		req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", w.Code)
		}
		body := decode(t, w.Body.String())
		if body["status"] != "ok" {
			t.Errorf("expected status=ok, got %v", body["status"])
		}
	})

	t.Run("metrics returns prometheus scrape output", func(t *testing.T) {
		agentStateUpdatesReceivedTotal.WithLabelValues(observability.MetricValues("Pod")...).Add(0)
		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", w.Code)
		}
		if got := w.Header().Get("Content-Type"); !strings.Contains(got, "text/plain") {
			t.Errorf("expected Prometheus text content type, got %q", got)
		}
		body := w.Body.String()
		if !strings.Contains(body, "astronomer_agent_state_updates_received_total") {
			t.Errorf("expected agent metric in scrape output, got %q", body)
		}
	})
}

func TestSetAgentVersion(t *testing.T) {
	hr := NewHealthReporter(nil, slog.Default(), 30, 60)
	hr.SetAgentVersion("v1.2.3")
	if hr.agentVersion != "v1.2.3" {
		t.Errorf("expected agent version v1.2.3, got %s", hr.agentVersion)
	}
}

func TestSetConnected(t *testing.T) {
	hr := NewHealthReporter(nil, slog.Default(), 30, 60)

	if hr.connected.Load() {
		t.Error("expected initial connected to be false")
	}

	hr.SetConnected(true)
	if !hr.connected.Load() {
		t.Error("expected connected to be true after SetConnected(true)")
	}

	hr.SetConnected(false)
	if hr.connected.Load() {
		t.Error("expected connected to be false after SetConnected(false)")
	}
}
