package agent

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
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
	mux := hr.healthMux()

	t.Run("healthz returns 200", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", w.Code)
		}
		if w.Body.String() != "ok" {
			t.Errorf("expected body 'ok', got %q", w.Body.String())
		}
	})

	t.Run("readyz returns 503 when not connected", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("expected status 503, got %d", w.Code)
		}
		if w.Body.String() != "not connected" {
			t.Errorf("expected body 'not connected', got %q", w.Body.String())
		}
	})

	t.Run("readyz returns 200 when connected", func(t *testing.T) {
		hr.SetConnected(true)
		req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", w.Code)
		}
		if w.Body.String() != "ok" {
			t.Errorf("expected body 'ok', got %q", w.Body.String())
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
