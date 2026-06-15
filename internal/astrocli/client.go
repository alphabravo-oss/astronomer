// Thin HTTP client over the Astronomer REST API.
//
// Wraps net/http with bearer-token injection + standard error
// decoding. Deliberately avoids generating from the OpenAPI spec
// because the CLI's command surface is curated — operators don't
// need 200 commands generated from every route, they need the dozen
// that map to operational tasks (list/create/delete clusters,
// download manifest, open shell, run k8s passthrough).
//
// When we want auto-generated coverage of every route, the OpenAPI
// spec at /api/v1/openapi.yaml is the input — but that's a separate
// branch from this hand-curated CLI.

package astrocli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client is the bearer-auth HTTP wrapper used by every command.
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// NewClient builds a Client bound to one server. The caller is expected
// to refuse commands when token is empty (caller-side check, not here,
// so the `astro login` command can use NewClient with token="" to do
// the initial /auth/login POST).
func NewClient(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		// No global timeout — interactive commands (shell open, watch
		// streams) can take longer than a minute. Per-call timeouts
		// live in command-specific contexts.
		httpClient: &http.Client{},
	}
}

// APIError surfaces a server-returned error body so the CLI can render
// the platform's error code + message instead of a generic HTTP-level
// failure.
type APIError struct {
	StatusCode int
	Code       string
	Message    string
	RawBody    string
}

func (e *APIError) Error() string {
	if e.Code != "" || e.Message != "" {
		return fmt.Sprintf("HTTP %d: %s (%s)", e.StatusCode, e.Message, e.Code)
	}
	if e.RawBody != "" {
		return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.RawBody)
	}
	return fmt.Sprintf("HTTP %d", e.StatusCode)
}

// Do is the inner request method. Path should start with `/api/v1/...`.
// out is optional — when non-nil the response body is JSON-decoded into
// it; when nil the body is discarded after the HTTP status check.
//
// We pass the path through unchanged (caller is expected to include the
// trailing slash) — but the server-side NormalizeAPITrailingSlash
// middleware (shipped this session for the cluster-delete bug) now
// makes either form work.
func (c *Client) Do(ctx context.Context, method, path string, body any, out any) error {
	var bodyReader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(raw)
	}
	url := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	// Default per-request timeout when caller didn't carry a deadline
	// in ctx. Lets a runaway endpoint surface instead of hanging the
	// CLI forever.
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		req = req.WithContext(ctx)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, url, err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	rawBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		apiErr := &APIError{StatusCode: resp.StatusCode, RawBody: string(rawBody)}
		// Best-effort decode of the platform's standard error envelope:
		//   { "error": { "code": "...", "message": "..." } }
		var envelope struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(rawBody, &envelope); err == nil {
			apiErr.Code = envelope.Error.Code
			apiErr.Message = envelope.Error.Message
		}
		return apiErr
	}

	if out != nil && len(rawBody) > 0 {
		if err := json.Unmarshal(rawBody, out); err != nil {
			return fmt.Errorf("decode %s response: %w", path, err)
		}
	}
	return nil
}

// GetRaw is a passthrough that returns the response body bytes — used
// by `astro cluster manifest` (which wants the YAML, not a JSON object).
func (c *Client) GetRaw(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, &APIError{StatusCode: resp.StatusCode, RawBody: string(body)}
	}
	return body, nil
}
