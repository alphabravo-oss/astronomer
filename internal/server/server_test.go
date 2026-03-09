package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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

	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
		wantFields []string
	}{
		{
			name:       "GET /health/ returns status, version, and time",
			method:     http.MethodGet,
			path:       "/health/",
			wantStatus: http.StatusOK,
			wantFields: []string{"status", "version", "time"},
		},
		{
			name:       "GET /health without trailing slash",
			method:     http.MethodGet,
			path:       "/health",
			wantStatus: http.StatusOK,
			wantFields: []string{"status", "version", "time"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			rec := httptest.NewRecorder()

			srv.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("expected status %d, got %d", tt.wantStatus, rec.Code)
			}

			var body map[string]string
			if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
				t.Fatalf("decoding response body: %v", err)
			}

			for _, field := range tt.wantFields {
				if _, ok := body[field]; !ok {
					t.Errorf("expected field %q in response, got %v", field, body)
				}
			}

			if body["status"] != "ok" {
				t.Errorf("expected status \"ok\", got %q", body["status"])
			}
		})
	}
}

func TestBootstrapEndpoints(t *testing.T) {
	srv := testServer(t)

	tests := []struct {
		name           string
		method         string
		path           string
		wantStatus     int
		wantJSON       map[string]interface{}
		wantBodySubstr string
	}{
		{
			name:       "GET /api/v1/bootstrap/ returns bootstrap status",
			method:     http.MethodGet,
			path:       "/api/v1/bootstrap/",
			wantStatus: http.StatusOK,
			wantJSON: map[string]interface{}{
				"bootstrapped":  false,
				"platform_name": "Astronomer",
				"server_url":    "",
			},
		},
		{
			name:           "POST /api/v1/bootstrap/complete/ returns 501",
			method:         http.MethodPost,
			path:           "/api/v1/bootstrap/complete/",
			wantStatus:     http.StatusNotImplemented,
			wantBodySubstr: "Not Implemented",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			rec := httptest.NewRecorder()

			srv.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("expected status %d, got %d", tt.wantStatus, rec.Code)
			}

			if tt.wantJSON != nil {
				var body map[string]interface{}
				if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
					t.Fatalf("decoding response body: %v", err)
				}

				for key, want := range tt.wantJSON {
					got, ok := body[key]
					if !ok {
						t.Errorf("expected field %q in response", key)
						continue
					}
					if got != want {
						t.Errorf("field %q: expected %v, got %v", key, want, got)
					}
				}
			}

			if tt.wantBodySubstr != "" {
				bodyStr := rec.Body.String()
				if !strings.Contains(bodyStr, tt.wantBodySubstr) {
					t.Errorf("expected body to contain %q, got %q", tt.wantBodySubstr, bodyStr)
				}
			}
		})
	}
}
