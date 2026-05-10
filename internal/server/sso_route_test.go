package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
)

func TestSSORoutesAcceptNoTrailingSlash(t *testing.T) {
	router := NewRouter(&config.Config{}, RouterDependencies{
		SSO: handler.NewSSOHandler(nil, nil, nil, "/"),
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/callback/dex", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "sso_not_configured") {
		t.Fatalf("expected sso handler response, got %s", rec.Body.String())
	}
}
