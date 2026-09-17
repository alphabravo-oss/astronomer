package astrocli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
)

const maxAuthResponseBytes = 1 << 20

// PasswordAPIToken is the non-browser credential produced by a cookie-bound
// password login. The plaintext is returned exactly once by the API and should
// be persisted only in the CLI's mode-0600 config.
type PasswordAPIToken struct {
	ID       string
	Token    string
	Username string
	Email    string
}

// IssuePasswordAPIToken authenticates through the browser session boundary,
// then exchanges that HttpOnly/CSRF-bound session for a scoped API token. It
// never expects or exposes browser access/refresh JWTs in response JSON.
func IssuePasswordAPIToken(
	ctx context.Context,
	server string,
	email string,
	password string,
	tokenName string,
	expiresInDays int,
) (*PasswordAPIToken, error) {
	base, baseURL, err := normalizeAuthServer(server)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(email) == "" || password == "" {
		return nil, fmt.Errorf("email and password are required")
	}
	if strings.TrimSpace(tokenName) == "" {
		return nil, fmt.Errorf("API token name is required")
	}
	if expiresInDays < 1 {
		return nil, fmt.Errorf("API token lifetime must be at least one day")
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("create session cookie jar: %w", err)
	}
	client := &http.Client{Timeout: 30 * time.Second, Jar: jar}

	loginBody, err := json.Marshal(map[string]string{"email": strings.TrimSpace(email), "password": password})
	if err != nil {
		return nil, fmt.Errorf("encode login request: %w", err)
	}
	loginRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/v1/auth/login/", bytes.NewReader(loginBody))
	if err != nil {
		return nil, fmt.Errorf("build login request: %w", err)
	}
	loginRequest.Header.Set("Content-Type", "application/json")
	loginRequest.Header.Set("Accept", "application/json")
	loginResponse, err := client.Do(loginRequest)
	if err != nil {
		return nil, fmt.Errorf("login request: %w", err)
	}
	var loginEnvelope struct {
		Data struct {
			User struct {
				Username string `json:"username"`
				Email    string `json:"email"`
			} `json:"user"`
		} `json:"data"`
	}
	if err := decodeAuthResponse(loginResponse, http.StatusOK, &loginEnvelope); err != nil {
		return nil, fmt.Errorf("login failed: %w", err)
	}

	csrfToken := ""
	for _, cookie := range jar.Cookies(baseURL) {
		if cookie.Name == auth.CSRFCookieName {
			csrfToken = cookie.Value
			break
		}
	}
	if csrfToken == "" {
		return nil, fmt.Errorf("login established no CSRF-bound browser session; complete any required password or MFA flow in the web UI first")
	}

	tokenBody, err := json.Marshal(map[string]any{
		"name":            strings.TrimSpace(tokenName),
		"expires_in_days": expiresInDays,
		"scopes":          []string{auth.ScopeAdmin},
	})
	if err != nil {
		return nil, fmt.Errorf("encode API token request: %w", err)
	}
	tokenRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/v1/auth/tokens/", bytes.NewReader(tokenBody))
	if err != nil {
		return nil, fmt.Errorf("build API token request: %w", err)
	}
	tokenRequest.Header.Set("Content-Type", "application/json")
	tokenRequest.Header.Set("Accept", "application/json")
	tokenRequest.Header.Set("X-CSRF-Token", csrfToken)
	tokenResponse, err := client.Do(tokenRequest)
	if err != nil {
		return nil, fmt.Errorf("create API token: %w", err)
	}
	var tokenEnvelope struct {
		Data struct {
			ID    string `json:"id"`
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := decodeAuthResponse(tokenResponse, http.StatusCreated, &tokenEnvelope); err != nil {
		return nil, fmt.Errorf("create API token: %w", err)
	}
	if strings.TrimSpace(tokenEnvelope.Data.ID) == "" || strings.TrimSpace(tokenEnvelope.Data.Token) == "" {
		return nil, fmt.Errorf("create API token: server returned an incomplete credential")
	}
	// The cookie session was only a credential-exchange boundary. Revoke its
	// access JTI and refresh family immediately so a CLI login does not leave an
	// unowned browser session alive after the in-memory jar disappears.
	logoutRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/v1/auth/logout/", nil)
	if err != nil {
		revokeIssuedToken(base, tokenEnvelope.Data.Token, tokenEnvelope.Data.ID)
		return nil, fmt.Errorf("build session cleanup request: %w", err)
	}
	logoutRequest.Header.Set("Accept", "application/json")
	logoutRequest.Header.Set("X-CSRF-Token", csrfToken)
	logoutResponse, err := client.Do(logoutRequest)
	if err != nil {
		revokeIssuedToken(base, tokenEnvelope.Data.Token, tokenEnvelope.Data.ID)
		return nil, fmt.Errorf("clean up browser session: %w", err)
	}
	if err := decodeAuthResponse(logoutResponse, http.StatusOK, nil); err != nil {
		revokeIssuedToken(base, tokenEnvelope.Data.Token, tokenEnvelope.Data.ID)
		return nil, fmt.Errorf("clean up browser session: %w", err)
	}

	return &PasswordAPIToken{
		ID:       tokenEnvelope.Data.ID,
		Token:    tokenEnvelope.Data.Token,
		Username: loginEnvelope.Data.User.Username,
		Email:    loginEnvelope.Data.User.Email,
	}, nil
}

func revokeIssuedToken(server, token, tokenID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = RevokeAPIToken(ctx, server, token, tokenID)
}

// RevokeAPIToken invalidates a CLI-owned token. A missing token is already in
// the desired state and is therefore treated as a successful idempotent revoke.
func RevokeAPIToken(ctx context.Context, server, token, tokenID string) error {
	if strings.TrimSpace(server) == "" || strings.TrimSpace(token) == "" || strings.TrimSpace(tokenID) == "" {
		return nil
	}
	err := NewClient(server, token).Do(ctx, http.MethodDelete, "/api/v1/auth/tokens/"+url.PathEscape(tokenID)+"/", nil, nil)
	if apiErr, ok := err.(*APIError); ok && apiErr.StatusCode == http.StatusNotFound {
		return nil
	}
	return err
}

func normalizeAuthServer(server string) (string, *url.URL, error) {
	base := strings.TrimRight(strings.TrimSpace(server), "/")
	parsed, err := url.Parse(base)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", nil, fmt.Errorf("invalid server URL %q", server)
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", nil, fmt.Errorf("server URL must not include a query or fragment")
	}
	return base, parsed, nil
}

func decodeAuthResponse(response *http.Response, expectedStatus int, out any) error {
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxAuthResponseBytes+1))
	if err != nil {
		return fmt.Errorf("read HTTP %d response: %w", response.StatusCode, err)
	}
	if len(raw) > maxAuthResponseBytes {
		return fmt.Errorf("HTTP %d response exceeded %d bytes", response.StatusCode, maxAuthResponseBytes)
	}
	if response.StatusCode != expectedStatus {
		apiErr := &APIError{StatusCode: response.StatusCode}
		var envelope struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(raw, &envelope) == nil {
			apiErr.Code = envelope.Error.Code
			apiErr.Message = envelope.Error.Message
		}
		return apiErr
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("decode HTTP %d response: %w", response.StatusCode, err)
		}
	}
	return nil
}
