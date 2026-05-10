package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
)

func TestLoginRouteIsRateLimited(t *testing.T) {
	router := NewRouter(&config.Config{}, RouterDependencies{
		Auth: handler.NewAuthHandler(nil, nil),
	})

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login/", strings.NewReader(`{}`))
		req.RemoteAddr = "198.51.100.20:4000"
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("attempt %d: expected 400 from handler before limit, got %d", i+1, rec.Code)
		}
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login/", strings.NewReader(`{}`))
	req.RemoteAddr = "198.51.100.20:4000"
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected sixth request to be rate limited, got %d", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got == "" {
		t.Fatal("expected Retry-After header on rate-limited response")
	}
	if !strings.Contains(rec.Body.String(), "rate_limited") {
		t.Fatalf("expected response body to mention rate_limited, got %q", rec.Body.String())
	}
}
