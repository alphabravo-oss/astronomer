package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLicense_Get_ReturnsOpenSource(t *testing.T) {
	h := NewLicenseHandler()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/license/", nil)
	rr := httptest.NewRecorder()
	h.Get(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var env struct {
		Data LicenseResponse `json:"data"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Data.State != "open-source" {
		t.Errorf("state = %q, want open-source", env.Data.State)
	}
	if len(env.Data.FeaturesEnabled) < 5 {
		t.Errorf("expected non-trivial feature list, got %d entries", len(env.Data.FeaturesEnabled))
	}
	if env.Data.ExpiresAt != nil {
		t.Errorf("expected expires_at nil for OSS, got %v", *env.Data.ExpiresAt)
	}
}
