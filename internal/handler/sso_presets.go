// SSO provider presets.
//
// Operators wiring SSO shouldn't need to memorise each provider's
// well-known issuer URL, default scope list, and what callback URL to
// register on the IdP side. The presets endpoint returns ready-made
// templates for the five providers we actively support:
//
//   github      — GitHub OAuth (uses provider-specific userinfo path)
//   google      — Google OAuth (OIDC under the hood, but a special-case
//                 hosted-domain claim is documented separately)
//   azure-ad    — Microsoft Entra ID via OIDC discovery on the tenant
//   gitlab      — GitLab.com or self-hosted via OIDC discovery
//   okta        — Okta workspace via OIDC discovery
//
// Each preset declares which fields the operator must supply (e.g.
// Azure needs a tenant ID, GitLab a workspace URL when self-hosted).
// The frontend reads this to render a branded "Add Azure AD" button
// with just the right form, instead of a single "OIDC provider"
// dialog where operators have to know the schema themselves.
//
// Generic OIDC remains supported for any provider not in this list —
// presets are a UX layer over the existing infrastructure, not a
// replacement.

package handler

import (
	"net/http"
)

// SSOPreset describes one ready-to-configure SSO provider.
type SSOPreset struct {
	// Key is the canonical short name. The frontend uses this to
	// pick the right brand icon + to label the audit / config row.
	// Must match what the SSO manager's `Kind` field expects.
	Key string `json:"key"`

	// DisplayName is human-readable.
	DisplayName string `json:"display_name"`

	// Type is one of "oauth2" (provider-specific userinfo path) or
	// "oidc" (generic OIDC discovery). Drives which code path the
	// SSO manager takes at registration.
	Type string `json:"type"`

	// IssuerURLTemplate is the OIDC discovery URL, with optional
	// {tenant} / {workspace} placeholders. Empty for pure-OAuth2
	// providers (GitHub) where there's no OIDC discovery endpoint.
	// Frontend substitutes the placeholder with operator input
	// before POSTing to /api/v1/settings/sso/.
	IssuerURLTemplate string `json:"issuer_url_template,omitempty"`

	// DefaultScopes is the recommended scope list for the provider.
	// Operators can edit but we want a working default so a one-click
	// preset actually completes a login on first try.
	DefaultScopes []string `json:"default_scopes"`

	// CallbackPathHint is the URL operators register on the IdP's
	// admin console as the authorized redirect URI. Same for every
	// preset (matches the server's mounted /auth/login/{provider}/
	// callback) — surfaced here so the docs read in one place.
	CallbackPathHint string `json:"callback_path_hint"`

	// RequiredFields lists the form fields the operator must fill
	// (in addition to client_id + client_secret which every provider
	// needs). Each entry is {name, label, placeholder, kind}. kind is
	// "text" / "url" / "tenant" — the frontend uses it to pick the
	// right input shape (e.g. tenant gets a UUID validator).
	RequiredFields []SSOPresetField `json:"required_fields"`

	// DocsURL is the operator-facing setup guide for this provider.
	// Surfaced as a "Setup instructions" link next to the preset
	// button so first-time operators get a working flow.
	DocsURL string `json:"docs_url,omitempty"`

	// LogoSlug matches a file in the frontend's icon set (a brand
	// SVG bundled into the React app). Centralized here so the
	// preset-to-icon mapping stays in one place.
	LogoSlug string `json:"logo_slug"`
}

// SSOPresetField is one form field in a preset.
type SSOPresetField struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Placeholder string `json:"placeholder"`
	Kind        string `json:"kind"` // text|url|tenant
	Required    bool   `json:"required"`
}

// ssoPresets is the canonical list. Order matters — frontend renders
// them in this sequence so the "common" providers appear first.
var ssoPresets = []SSOPreset{
	{
		Key:               "github",
		DisplayName:       "GitHub",
		Type:              "oauth2",
		IssuerURLTemplate: "", // GitHub doesn't expose OIDC discovery
		DefaultScopes:     []string{"user:email", "read:org"},
		CallbackPathHint:  "/auth/login/github/callback",
		LogoSlug:          "github",
		DocsURL:           "https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/creating-an-oauth-app",
		RequiredFields:    []SSOPresetField{},
	},
	{
		Key:               "google",
		DisplayName:       "Google Workspace",
		Type:              "oauth2",
		IssuerURLTemplate: "",
		DefaultScopes:     []string{"openid", "email", "profile"},
		CallbackPathHint:  "/auth/login/google/callback",
		LogoSlug:          "google",
		DocsURL:           "https://developers.google.com/identity/protocols/oauth2/openid-connect",
		RequiredFields: []SSOPresetField{
			{
				Name:        "hosted_domain",
				Label:       "Hosted domain (optional)",
				Placeholder: "example.com",
				Kind:        "text",
				Required:    false,
			},
		},
	},
	{
		Key:         "azure-ad",
		DisplayName: "Microsoft Entra ID",
		Type:        "oidc",
		// Microsoft's per-tenant OIDC discovery endpoint. The `v2.0`
		// suffix is mandatory — the `v1` endpoint returns different
		// claim shapes (e.g. `upn` instead of `preferred_username`)
		// and our user-mapping logic would silently produce empty
		// usernames. We pin v2.0 to avoid that footgun.
		IssuerURLTemplate: "https://login.microsoftonline.com/{tenant}/v2.0",
		DefaultScopes:     []string{"openid", "email", "profile"},
		CallbackPathHint:  "/auth/login/azure-ad/callback",
		LogoSlug:          "azure",
		DocsURL:           "https://learn.microsoft.com/en-us/entra/identity-platform/v2-protocols-oidc",
		RequiredFields: []SSOPresetField{
			{
				Name:        "tenant",
				Label:       "Directory (tenant) ID",
				Placeholder: "00000000-0000-0000-0000-000000000000 or contoso.onmicrosoft.com",
				Kind:        "tenant",
				Required:    true,
			},
		},
	},
	{
		Key:               "gitlab",
		DisplayName:       "GitLab",
		Type:              "oidc",
		IssuerURLTemplate: "{workspace}",
		DefaultScopes:     []string{"openid", "email", "profile", "read_user"},
		CallbackPathHint:  "/auth/login/gitlab/callback",
		LogoSlug:          "gitlab",
		DocsURL:           "https://docs.gitlab.com/integration/openid_connect_provider/",
		RequiredFields: []SSOPresetField{
			{
				Name:        "workspace",
				Label:       "GitLab instance URL",
				Placeholder: "https://gitlab.com (or your self-hosted URL)",
				Kind:        "url",
				Required:    true,
			},
		},
	},
	{
		Key:               "okta",
		DisplayName:       "Okta",
		Type:              "oidc",
		IssuerURLTemplate: "{workspace}",
		DefaultScopes:     []string{"openid", "email", "profile", "groups"},
		CallbackPathHint:  "/auth/login/okta/callback",
		LogoSlug:          "okta",
		DocsURL:           "https://developer.okta.com/docs/guides/sign-into-web-app-redirect/",
		RequiredFields: []SSOPresetField{
			{
				Name:        "workspace",
				Label:       "Okta domain",
				Placeholder: "https://dev-12345.okta.com",
				Kind:        "url",
				Required:    true,
			},
		},
	},
}

// SSOPresetsHandler serves GET /api/v1/settings/sso/presets/.
type SSOPresetsHandler struct{}

// NewSSOPresetsHandler builds the handler. Stateless — the preset
// list is a compile-time constant. Kept as a named handler (not just
// a route inline) so wiring tests can mount it cleanly.
func NewSSOPresetsHandler() *SSOPresetsHandler { return &SSOPresetsHandler{} }

// List returns the canonical preset list. Public-readable so the
// login page can show the "Sign in with Azure AD" branded buttons
// even for users that aren't logged in yet. RespondJSON already
// wraps the slice as `{"data": [...]}` per the platform's standard
// response envelope; don't double-wrap here.
func (h *SSOPresetsHandler) List(w http.ResponseWriter, _ *http.Request) {
	RespondJSON(w, http.StatusOK, ssoPresets)
}
