package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSSOPresetsListShape pins the response envelope + the canonical
// preset order. Order matters because the React app renders these in
// list order on the SSO setup page — re-ordering shuffles the UI.
func TestSSOPresetsListShape(t *testing.T) {
	h := NewSSOPresetsHandler()
	rr := httptest.NewRecorder()
	h.List(rr, httptest.NewRequest(http.MethodGet, "/api/v1/settings/sso/presets/", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var body struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Data) == 0 {
		t.Fatal("no presets returned")
	}

	wantOrder := []string{"github", "google", "azure-ad", "gitlab", "okta"}
	got := make([]string, 0, len(body.Data))
	for _, p := range body.Data {
		k, _ := p["key"].(string)
		got = append(got, k)
	}
	if strings.Join(got, ",") != strings.Join(wantOrder, ",") {
		t.Errorf("preset order = %v, want %v", got, wantOrder)
	}
}

// TestSSOPresetsAzureADTenantIssuer verifies the Azure AD preset
// pins the v2.0 endpoint (not v1). The v1 endpoint returns different
// claim shapes and would break our user mapping.
func TestSSOPresetsAzureADTenantIssuer(t *testing.T) {
	for _, p := range ssoPresets {
		if p.Key != "azure-ad" {
			continue
		}
		if !strings.Contains(p.IssuerURLTemplate, "v2.0") {
			t.Errorf("azure-ad IssuerURLTemplate = %q, want it to pin v2.0", p.IssuerURLTemplate)
		}
		if !strings.Contains(p.IssuerURLTemplate, "{tenant}") {
			t.Errorf("azure-ad IssuerURLTemplate = %q, want it to carry {tenant} placeholder", p.IssuerURLTemplate)
		}
		found := false
		for _, f := range p.RequiredFields {
			if f.Name == "tenant" && f.Required {
				found = true
			}
		}
		if !found {
			t.Errorf("azure-ad missing required tenant field")
		}
		return
	}
	t.Fatal("azure-ad preset not found")
}

// TestSSOPresetsTypes ensures each preset's Type is one of the
// values the registration handler understands.
func TestSSOPresetsTypes(t *testing.T) {
	for _, p := range ssoPresets {
		switch p.Type {
		case "oauth2", "oidc":
			// ok
		default:
			t.Errorf("preset %q has unknown type %q", p.Key, p.Type)
		}
	}
}
