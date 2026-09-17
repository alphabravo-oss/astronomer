package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
)

// loadToken reads a bearer token from the supplied file path. Empty path is
// allowed; verifyServer rejects it when the target requires authentication.
func loadToken(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(b))
	if token == "" {
		return "", fmt.Errorf("token file %s is empty", path)
	}
	return token, nil
}

func loadCredential(path, name string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("%s file is required", name)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", name, err)
	}
	value := strings.TrimSpace(string(b))
	if value == "" {
		return "", fmt.Errorf("%s file is empty", name)
	}
	return value, nil
}

// bootstrapAdminAPIToken uses the cookie-only browser session boundary to mint
// a short-lived API credential entirely in memory. It is deliberately excluded
// from certification: protected runs receive a pre-provisioned token from the
// environment approval gate and never handle a human password.
func bootstrapAdminAPIToken(ctx context.Context, server, email, password string) (string, func(), error) {
	base, err := normalizedManagementURL(server)
	if err != nil {
		return "", nil, err
	}
	email = strings.TrimSpace(email)
	if email == "" || strings.TrimSpace(password) == "" {
		return "", nil, fmt.Errorf("login email and password are required")
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return "", nil, fmt.Errorf("create in-memory cookie jar: %w", err)
	}
	client := &http.Client{Timeout: 15 * time.Second, Jar: jar}
	loginBody, err := json.Marshal(map[string]string{"email": email, "password": password})
	if err != nil {
		return "", nil, err
	}
	loginRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/v1/auth/login/", bytes.NewReader(loginBody))
	if err != nil {
		return "", nil, err
	}
	loginRequest.Header.Set("Content-Type", "application/json")
	loginResponse, err := client.Do(loginRequest)
	if err != nil {
		return "", nil, fmt.Errorf("login request failed")
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(loginResponse.Body, maxProvisionResponseBytes))
	_ = loginResponse.Body.Close()
	if loginResponse.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("login returned HTTP %d", loginResponse.StatusCode)
	}

	baseURL, err := url.Parse(base)
	if err != nil {
		return "", nil, err
	}
	csrfToken := ""
	for _, cookie := range jar.Cookies(baseURL) {
		if cookie.Name == auth.CSRFCookieName {
			csrfToken = cookie.Value
			break
		}
	}
	if csrfToken == "" {
		return "", nil, fmt.Errorf("login established no CSRF-bound browser session")
	}
	tokenName := "loadtest-" + strings.ReplaceAll(uuid.NewString()[:13], "-", "")
	tokenBody, err := json.Marshal(map[string]any{
		"name": tokenName, "expires_in_days": 1, "scopes": []string{auth.ScopeAdmin},
	})
	if err != nil {
		return "", nil, err
	}
	tokenRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/v1/auth/tokens/", bytes.NewReader(tokenBody))
	if err != nil {
		return "", nil, err
	}
	tokenRequest.Header.Set("Content-Type", "application/json")
	tokenRequest.Header.Set("X-CSRF-Token", csrfToken)
	tokenResponse, err := client.Do(tokenRequest)
	if err != nil {
		return "", nil, fmt.Errorf("API token request failed")
	}
	var created struct {
		Data struct {
			ID    string `json:"id"`
			Token string `json:"token"`
		} `json:"data"`
	}
	decodeErr := json.NewDecoder(io.LimitReader(tokenResponse.Body, maxProvisionResponseBytes)).Decode(&created)
	_ = tokenResponse.Body.Close()
	if tokenResponse.StatusCode != http.StatusCreated {
		return "", nil, fmt.Errorf("API token creation returned HTTP %d", tokenResponse.StatusCode)
	}
	if decodeErr != nil || strings.TrimSpace(created.Data.Token) == "" {
		return "", nil, fmt.Errorf("API token creation returned an invalid response")
	}
	tokenID, err := uuid.Parse(created.Data.ID)
	if err != nil {
		return "", nil, fmt.Errorf("API token creation returned an invalid ID")
	}
	plaintext := created.Data.Token
	var revokeOnce sync.Once
	revoke := func() {
		revokeOnce.Do(func() {
			revokeCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			request, requestErr := http.NewRequestWithContext(revokeCtx, http.MethodDelete,
				base+"/api/v1/auth/tokens/"+tokenID.String()+"/", nil)
			if requestErr != nil {
				return
			}
			request.Header.Set("Authorization", "Bearer "+plaintext)
			response, requestErr := (&http.Client{Timeout: 15 * time.Second}).Do(request)
			if requestErr == nil && response != nil {
				_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxProvisionResponseBytes))
				_ = response.Body.Close()
			}
		})
	}
	return plaintext, revoke, nil
}

// verifyServer fails fast for every response except a successful auth probe.
func verifyServer(server, token string) error {
	req, err := http.NewRequest(http.MethodGet, server+"/api/v1/auth/me/", nil)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("auth/me/ returned %d; target or credentials are invalid", response.StatusCode)
	}
	return nil
}
