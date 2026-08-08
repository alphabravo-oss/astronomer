package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/charlie"
)

func TestCharlieOnboardingHandlerEnforcesMediaTypeAndSize(t *testing.T) {
	handler := NewCharlieOnboardingHandler(nil)

	missingType := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
	missingTypeResponse := httptest.NewRecorder()
	handler.Validate(missingTypeResponse, missingType)
	if missingTypeResponse.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("missing Content-Type status=%d", missingTypeResponse.Code)
	}

	oversized := `{"package":"` + strings.Repeat("x", charlie.MaxOnboardingPackageBytes) + `"}`
	oversizedRequest := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(oversized))
	oversizedRequest.Header.Set("Content-Type", "application/json")
	oversizedResponse := httptest.NewRecorder()
	handler.Validate(oversizedResponse, oversizedRequest)
	if oversizedResponse.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized request status=%d body=%s", oversizedResponse.Code, oversizedResponse.Body.String())
	}
}

func TestCharlieOnboardingHandlerDoesNotEchoInvalidSecrets(t *testing.T) {
	handler := NewCharlieOnboardingHandler(nil)
	secret := "fixture-plaintext-onboarding-secret"
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"package":{"credential":"`+secret+`"},"signing_public_key":"invalid","confirmed_signing_fingerprint":"invalid"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.Validate(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid request status=%d", response.Code)
	}
	if strings.Contains(response.Body.String(), secret) {
		t.Fatalf("error response echoed onboarding secret: %s", response.Body.String())
	}
}
