package main

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
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
