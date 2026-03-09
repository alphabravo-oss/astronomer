package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/config"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	cfg := &config.Config{
		CORSAllowedOrigins: "http://localhost:3000",
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	return New(cfg, logger)
}

func TestHealthEndpoint(t *testing.T) {
	srv := testServer(t)

	req := httptest.NewRequest(http.MethodGet, "/health/", nil)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decoding response body: %v", err)
	}

	if body["status"] != "ok" {
		t.Errorf("expected status \"ok\", got %q", body["status"])
	}
}

func TestBootstrapStatusEndpoint(t *testing.T) {
	srv := testServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/bootstrap/", nil)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var body map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decoding response body: %v", err)
	}

	if body["bootstrapped"] != false {
		t.Errorf("expected bootstrapped=false, got %v", body["bootstrapped"])
	}
	if body["platform_name"] != "Astronomer" {
		t.Errorf("expected platform_name=\"Astronomer\", got %v", body["platform_name"])
	}
}
