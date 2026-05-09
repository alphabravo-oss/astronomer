package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/github"
	"golang.org/x/oauth2/google"
)

// builtInProviders are the legacy provider kinds that ship with provider-
// specific user-info logic (GitHub orgs, Google hosted-domain). Anything else
// is treated as a generic OIDC IdP and discovered at registration time.
var builtInProviders = map[string]oauth2.Endpoint{
	"github": github.Endpoint,
	"google": google.Endpoint,
}

func isBuiltInProvider(name string) bool {
	_, ok := builtInProviders[strings.ToLower(name)]
	return ok
}

// SSOConfigQuerier abstracts the SSO config lookups the manager needs at boot.
type SSOConfigQuerier interface {
	GetEnabledSSOProviders(ctx context.Context) ([]sqlc.SsoConfiguration, error)
}

// SSOProvider represents a configured SSO provider.
type SSOProvider struct {
	Name         string
	ClientID     string
	ClientSecret string // decrypted
	RedirectURL  string
	Scopes       []string
	Config       *oauth2.Config

	// Kind is "github", "google", or "oidc" (generic). It drives the user
	// info / id-token validation flow at callback time.
	Kind string

	// IssuerURL is non-empty for generic OIDC providers and is used by the
	// discovery client to look up endpoints + JWKS.
	IssuerURL string
}

// SSOUserInfo represents user info from an SSO provider.
type SSOUserInfo struct {
	Email     string `json:"email"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	AvatarURL string `json:"avatar_url"`
	Provider  string `json:"provider"`
	// Provider-specific fields
	Organizations []string `json:"organizations,omitempty"` // GitHub orgs
	Domain        string   `json:"domain,omitempty"`        // Google hosted domain
	Groups        []string `json:"groups,omitempty"`        // OIDC groups claim
}

// SSOManager manages OAuth2/SSO flows.
type SSOManager struct {
	encryptor   *Encryptor
	jwtManager  *JWTManager
	providers   map[string]*SSOProvider
	callbackURL string // base URL for callbacks
	mu          sync.RWMutex

	// httpClient allows overriding for tests
	httpClient *http.Client

	// discovery is the generic-OIDC discovery + JWKS client. It is lazy-
	// constructed on first use (or replaced via SetDiscoveryClient for tests).
	discovery *OIDCDiscoveryClient
}

// NewSSOManager creates a new SSO manager.
func NewSSOManager(encryptor *Encryptor, jwtManager *JWTManager, callbackBaseURL string) *SSOManager {
	return &SSOManager{
		encryptor:   encryptor,
		jwtManager:  jwtManager,
		providers:   make(map[string]*SSOProvider),
		callbackURL: strings.TrimRight(callbackBaseURL, "/"),
		httpClient:  http.DefaultClient,
	}
}

// SetDiscoveryClient replaces the OIDC discovery client. Tests use this to
// inject an httptest.Server-backed client.
func (m *SSOManager) SetDiscoveryClient(c *OIDCDiscoveryClient) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.discovery = c
}

// discoveryClient returns the manager's discovery client, lazily constructing
// one that uses m.httpClient if unset.
func (m *SSOManager) discoveryClient() *OIDCDiscoveryClient {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.discovery == nil {
		m.discovery = NewOIDCDiscoveryClient(m.httpClient)
	}
	return m.discovery
}

// providerEndpoint returns the oauth2.Endpoint for the legacy hard-coded
// providers. Generic OIDC providers go through RegisterOIDCProvider and
// derive their endpoints from the discovery document.
func providerEndpoint(name string) (oauth2.Endpoint, error) {
	if ep, ok := builtInProviders[strings.ToLower(name)]; ok {
		return ep, nil
	}
	return oauth2.Endpoint{}, fmt.Errorf("unknown provider: %s (did you mean to use RegisterOIDCProvider with an issuer_url?)", name)
}

// defaultScopes returns sensible defaults per provider when none are supplied.
func defaultScopes(name string) []string {
	switch strings.ToLower(name) {
	case "github":
		return []string{"user:email", "read:org"}
	case "google":
		return []string{"profile", "email"}
	default:
		return []string{"openid", "profile", "email"}
	}
}

// RegisterProvider configures a built-in OAuth2 provider (GitHub or Google).
// clientSecretEncrypted is a Fernet-encrypted client secret. To register a
// generic OIDC IdP (Keycloak, Authentik, Auth0, Okta, etc.) call
// RegisterOIDCProvider instead.
func (m *SSOManager) RegisterProvider(name, clientID, clientSecretEncrypted, redirectURL string, scopes []string) error {
	if name == "" {
		return fmt.Errorf("provider name must not be empty")
	}
	if clientID == "" {
		return fmt.Errorf("client ID must not be empty")
	}

	// Decrypt the client secret
	secret, err := m.encryptor.Decrypt(clientSecretEncrypted)
	if err != nil {
		return fmt.Errorf("decrypting client secret for %s: %w", name, err)
	}

	endpoint, err := providerEndpoint(name)
	if err != nil {
		return err
	}

	if len(scopes) == 0 {
		scopes = defaultScopes(name)
	}

	if redirectURL == "" {
		redirectURL = fmt.Sprintf("%s/auth/callback/%s", m.callbackURL, strings.ToLower(name))
	}

	cfg := &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: secret,
		Endpoint:     endpoint,
		RedirectURL:  redirectURL,
		Scopes:       scopes,
	}

	m.mu.Lock()
	m.providers[strings.ToLower(name)] = &SSOProvider{
		Name:         name,
		Kind:         strings.ToLower(name),
		ClientID:     clientID,
		ClientSecret: secret,
		RedirectURL:  redirectURL,
		Scopes:       scopes,
		Config:       cfg,
	}
	m.mu.Unlock()

	return nil
}

// RegisterOIDCProvider configures a generic OpenID Connect provider. It
// fetches the discovery document at <issuerURL>/.well-known/openid-configuration
// to populate auth/token endpoints and the JWKS URI. Any conformant IdP
// (Keycloak, Authentik, Auth0, Okta-OIDC, Dex, ...) works without
// provider-specific code.
//
// The provider is keyed in the manager by `name` (e.g. "keycloak"), which is
// what shows up in the /auth/login/{provider}/ URL.
func (m *SSOManager) RegisterOIDCProvider(ctx context.Context, name, issuerURL, clientID, clientSecretEncrypted, redirectURL string, scopes []string) error {
	if name == "" {
		return fmt.Errorf("provider name must not be empty")
	}
	if clientID == "" {
		return fmt.Errorf("client ID must not be empty")
	}
	issuerURL = strings.TrimRight(strings.TrimSpace(issuerURL), "/")
	if issuerURL == "" {
		return fmt.Errorf("issuer URL must not be empty for oidc provider %q", name)
	}

	secret, err := m.encryptor.Decrypt(clientSecretEncrypted)
	if err != nil {
		return fmt.Errorf("decrypting client secret for %s: %w", name, err)
	}

	doc, err := m.discoveryClient().FetchDiscovery(ctx, issuerURL)
	if err != nil {
		return fmt.Errorf("oidc discovery for %s: %w", name, err)
	}

	if len(scopes) == 0 {
		scopes = []string{"openid", "profile", "email"}
	}
	if redirectURL == "" {
		redirectURL = fmt.Sprintf("%s/auth/callback/%s", m.callbackURL, strings.ToLower(name))
	}

	cfg := &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: secret,
		Endpoint: oauth2.Endpoint{
			AuthURL:  doc.AuthEndpoint,
			TokenURL: doc.TokenEndpoint,
		},
		RedirectURL: redirectURL,
		Scopes:      scopes,
	}

	m.mu.Lock()
	m.providers[strings.ToLower(name)] = &SSOProvider{
		Name:         name,
		Kind:         "oidc",
		ClientID:     clientID,
		ClientSecret: secret,
		RedirectURL:  redirectURL,
		Scopes:       scopes,
		Config:       cfg,
		IssuerURL:    issuerURL,
	}
	m.mu.Unlock()

	return nil
}

// HasProvider reports whether the named provider is currently registered.
func (m *SSOManager) HasProvider(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.providers[strings.ToLower(name)]
	return ok
}

// EnabledProviders returns the lowercase names of every registered provider.
func (m *SSOManager) EnabledProviders() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]string, 0, len(m.providers))
	for name := range m.providers {
		out = append(out, name)
	}
	return out
}

// SSOProviderConfig is the shape we expect inside the sso_configurations.config
// JSONB blob. issuer_url is required for any non-built-in provider.
// scopes / redirect_url are optional overrides.
type SSOProviderConfig struct {
	IssuerURL   string   `json:"issuer_url,omitempty"`
	RedirectURL string   `json:"redirect_url,omitempty"`
	Scopes      []string `json:"scopes,omitempty"`
}

// LoadFromDatabase reads enabled SSO configurations from the supplied querier
// and registers each one. It is best-effort: a single provider failing to
// register does not prevent the others from loading. Errors are returned as a
// joined message so callers can log them at startup.
//
// Built-in providers (github/google) register with hard-coded endpoints.
// Anything else is treated as a generic OIDC provider and must supply an
// `issuer_url` in the row's `config` JSON.
func (m *SSOManager) LoadFromDatabase(ctx context.Context, queries SSOConfigQuerier) error {
	if queries == nil {
		return nil
	}
	rows, err := queries.GetEnabledSSOProviders(ctx)
	if err != nil {
		return err
	}
	var failures []string
	for _, row := range rows {
		var cfg SSOProviderConfig
		if len(row.Config) > 0 {
			if err := json.Unmarshal(row.Config, &cfg); err != nil {
				failures = append(failures, fmt.Sprintf("%s: parsing config: %v", row.Provider, err))
				continue
			}
		}
		if isBuiltInProvider(row.Provider) {
			if err := m.RegisterProvider(row.Provider, row.ClientID, row.ClientSecretEncrypted, cfg.RedirectURL, cfg.Scopes); err != nil {
				failures = append(failures, fmt.Sprintf("%s: %v", row.Provider, err))
			}
			continue
		}
		// Generic OIDC path. Skip silently when the operator hasn't filled in
		// the issuer URL yet — they'll see it in the validation flow.
		if cfg.IssuerURL == "" {
			failures = append(failures, fmt.Sprintf("%s: issuer_url is required for non-built-in providers", row.Provider))
			continue
		}
		if err := m.RegisterOIDCProvider(ctx, row.Provider, cfg.IssuerURL, row.ClientID, row.ClientSecretEncrypted, cfg.RedirectURL, cfg.Scopes); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", row.Provider, err))
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("registering sso providers: %s", strings.Join(failures, "; "))
	}
	return nil
}

// generateState produces a cryptographically random state parameter for CSRF protection.
func generateState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating state: %w", err)
	}
	return base64.URLEncoding.EncodeToString(b), nil
}

// GetAuthorizationURL returns the OAuth2 authorization URL for a provider
// with a state parameter for CSRF protection.
func (m *SSOManager) GetAuthorizationURL(provider string) (url string, state string, err error) {
	m.mu.RLock()
	p, ok := m.providers[strings.ToLower(provider)]
	m.mu.RUnlock()

	if !ok {
		return "", "", fmt.Errorf("unknown provider: %s", provider)
	}

	state, err = generateState()
	if err != nil {
		return "", "", err
	}

	url = p.Config.AuthCodeURL(state, oauth2.AccessTypeOffline)
	return url, state, nil
}

// HandleCallback processes the OAuth2 callback, exchanges code for token,
// fetches user info, and returns the user's email and profile.
func (m *SSOManager) HandleCallback(ctx context.Context, provider, code, state string) (*SSOUserInfo, error) {
	m.mu.RLock()
	p, ok := m.providers[strings.ToLower(provider)]
	m.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("unknown provider: %s", provider)
	}

	// Exchange authorization code for token
	token, err := p.Config.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("exchanging code for %s: %w", provider, err)
	}

	// Fetch user info based on provider Kind. Falls back to provider name to
	// keep the legacy "oidc" alias working.
	kind := p.Kind
	if kind == "" {
		kind = strings.ToLower(provider)
	}
	switch kind {
	case "github":
		return m.fetchGitHubUserInfo(ctx, token)
	case "google":
		return m.fetchGoogleUserInfo(ctx, token)
	case "oidc":
		return m.fetchGenericOIDCUserInfo(ctx, p, token)
	default:
		return nil, fmt.Errorf("user info fetch not implemented for provider: %s", provider)
	}
}

// fetchGenericOIDCUserInfo validates the ID token via JWKS (when an issuer URL
// is configured), then maps standard OIDC claims into our SSOUserInfo. If
// validation fails, the discovery cache is invalidated so the next attempt
// picks up rotated keys / endpoints.
func (m *SSOManager) fetchGenericOIDCUserInfo(ctx context.Context, p *SSOProvider, token *oauth2.Token) (*SSOUserInfo, error) {
	rawIDToken, _ := token.Extra("id_token").(string)
	if rawIDToken == "" {
		return nil, fmt.Errorf("missing id_token in oidc callback for %s", p.Name)
	}

	if p.IssuerURL == "" {
		// Legacy path: parse-without-verify. Used by the original "oidc"
		// alias before discovery was wired up.
		return m.fetchOIDCUserInfo(token)
	}

	claims, err := m.discoveryClient().ValidateIDToken(ctx, rawIDToken, p.IssuerURL, p.ClientID)
	if err != nil {
		// Force re-discovery next time around.
		m.discoveryClient().InvalidateDiscovery(p.IssuerURL)
		return nil, fmt.Errorf("validating id_token for %s: %w", p.Name, err)
	}

	username := claims.PreferredUsername
	if username == "" {
		username = claims.Email
	}
	firstName := claims.GivenName
	lastName := claims.FamilyName
	if firstName == "" && lastName == "" {
		firstName, lastName = splitName(claims.Name)
	}

	return &SSOUserInfo{
		Email:     claims.Email,
		Username:  username,
		FirstName: firstName,
		LastName:  lastName,
		AvatarURL: claims.Picture,
		Provider:  strings.ToLower(p.Name),
		Groups:    claims.Groups,
		Domain:    claims.HostedDomain,
	}, nil
}

// fetchGitHubUserInfo retrieves user info from GitHub's API.
func (m *SSOManager) fetchGitHubUserInfo(ctx context.Context, token *oauth2.Token) (*SSOUserInfo, error) {
	// Fetch user profile
	userResp, err := m.doAPIRequest(ctx, "https://api.github.com/user", token)
	if err != nil {
		return nil, fmt.Errorf("fetching GitHub user: %w", err)
	}

	var ghUser struct {
		Login     string `json:"login"`
		Name      string `json:"name"`
		Email     string `json:"email"`
		AvatarURL string `json:"avatar_url"`
	}
	if err := json.Unmarshal(userResp, &ghUser); err != nil {
		return nil, fmt.Errorf("parsing GitHub user: %w", err)
	}

	// Split name into first/last
	firstName, lastName := splitName(ghUser.Name)

	info := &SSOUserInfo{
		Email:     ghUser.Email,
		Username:  ghUser.Login,
		FirstName: firstName,
		LastName:  lastName,
		AvatarURL: ghUser.AvatarURL,
		Provider:  "github",
	}

	// Fetch organizations
	orgsResp, err := m.doAPIRequest(ctx, "https://api.github.com/user/orgs", token)
	if err == nil {
		var orgs []struct {
			Login string `json:"login"`
		}
		if err := json.Unmarshal(orgsResp, &orgs); err == nil {
			for _, org := range orgs {
				info.Organizations = append(info.Organizations, org.Login)
			}
		}
	}

	return info, nil
}

// fetchGoogleUserInfo retrieves user info from Google's API.
func (m *SSOManager) fetchGoogleUserInfo(ctx context.Context, token *oauth2.Token) (*SSOUserInfo, error) {
	resp, err := m.doAPIRequest(ctx, "https://www.googleapis.com/oauth2/v2/userinfo", token)
	if err != nil {
		return nil, fmt.Errorf("fetching Google user info: %w", err)
	}

	var gUser struct {
		Email      string `json:"email"`
		GivenName  string `json:"given_name"`
		FamilyName string `json:"family_name"`
		Picture    string `json:"picture"`
		HD         string `json:"hd"` // hosted domain
	}
	if err := json.Unmarshal(resp, &gUser); err != nil {
		return nil, fmt.Errorf("parsing Google user info: %w", err)
	}

	return &SSOUserInfo{
		Email:     gUser.Email,
		Username:  gUser.Email, // Google uses email as username
		FirstName: gUser.GivenName,
		LastName:  gUser.FamilyName,
		AvatarURL: gUser.Picture,
		Provider:  "google",
		Domain:    gUser.HD,
	}, nil
}

// fetchOIDCUserInfo extracts user info from OIDC token claims.
func (m *SSOManager) fetchOIDCUserInfo(token *oauth2.Token) (*SSOUserInfo, error) {
	rawIDToken, _ := token.Extra("id_token").(string)
	if rawIDToken == "" {
		return nil, fmt.Errorf("missing id_token in oidc callback")
	}
	claims, err := parseJWTClaims(rawIDToken)
	if err != nil {
		return nil, fmt.Errorf("parsing oidc id_token: %w", err)
	}
	email := claimString(claims, "email")
	username := claimString(claims, "preferred_username")
	if username == "" {
		username = email
	}
	firstName := claimString(claims, "given_name")
	lastName := claimString(claims, "family_name")
	if firstName == "" && lastName == "" {
		firstName, lastName = splitName(claimString(claims, "name"))
	}
	return &SSOUserInfo{
		Email:     email,
		Username:  username,
		FirstName: firstName,
		LastName:  lastName,
		AvatarURL: claimString(claims, "picture"),
		Provider:  "oidc",
		Groups:    claimStringSlice(claims["groups"]),
	}, nil
}

// doAPIRequest makes an authenticated GET request to the given URL.
func (m *SSOManager) doAPIRequest(ctx context.Context, url string, token *oauth2.Token) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	req.Header.Set("Accept", "application/json")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API request to %s returned status %d: %s", url, resp.StatusCode, string(body))
	}

	return body, nil
}

// splitName splits a full name into first and last name.
func splitName(name string) (first, last string) {
	parts := strings.Fields(name)
	switch len(parts) {
	case 0:
		return "", ""
	case 1:
		return parts[0], ""
	default:
		return parts[0], strings.Join(parts[1:], " ")
	}
}

func parseJWTClaims(raw string) (map[string]any, error) {
	parts := strings.Split(raw, ".")
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid jwt format")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, err
	}
	return claims, nil
}

func claimString(claims map[string]any, key string) string {
	if value, ok := claims[key].(string); ok {
		return value
	}
	return ""
}

func claimStringSlice(value any) []string {
	raw, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}
