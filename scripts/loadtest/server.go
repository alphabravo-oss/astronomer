package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/astrocli"
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
	tokenName := "loadtest-" + strings.ReplaceAll(uuid.NewString()[:13], "-", "")
	issued, err := astrocli.IssuePasswordAPIToken(ctx, server, email, password, tokenName, 1)
	if err != nil {
		return "", nil, err
	}
	var revokeOnce sync.Once
	revoke := func() {
		revokeOnce.Do(func() {
			revokeCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			_ = astrocli.RevokeAPIToken(revokeCtx, server, issued.Token, issued.ID)
		})
	}
	return issued.Token, revoke, nil
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
