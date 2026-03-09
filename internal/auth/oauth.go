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

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/github"
	"golang.org/x/oauth2/google"
)

// SSOProvider represents a configured SSO provider.
type SSOProvider struct {
	Name         string
	ClientID     string
	ClientSecret string // decrypted
	RedirectURL  string
	Scopes       []string
	Config       *oauth2.Config
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

// providerEndpoint returns the oauth2.Endpoint for known providers.
func providerEndpoint(name string) (oauth2.Endpoint, error) {
	switch strings.ToLower(name) {
	case "github":
		return github.Endpoint, nil
	case "google":
		return google.Endpoint, nil
	case "oidc":
		// OIDC needs discovery; for now return empty endpoint.
		// Full implementation will use go-oidc discovery.
		return oauth2.Endpoint{}, nil
	default:
		return oauth2.Endpoint{}, fmt.Errorf("unknown provider: %s", name)
	}
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

// RegisterProvider configures an OAuth2 provider.
// clientSecretEncrypted is a Fernet-encrypted client secret.
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
		ClientID:     clientID,
		ClientSecret: secret,
		RedirectURL:  redirectURL,
		Scopes:       scopes,
		Config:       cfg,
	}
	m.mu.Unlock()

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

	// Fetch user info based on provider type
	switch strings.ToLower(provider) {
	case "github":
		return m.fetchGitHubUserInfo(ctx, token)
	case "google":
		return m.fetchGoogleUserInfo(ctx, token)
	default:
		return nil, fmt.Errorf("user info fetch not implemented for provider: %s", provider)
	}
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
