package httpclient

import (
	"net/http"
	"time"
)

// DefaultExternalTimeout is the fallback wall-clock budget for ordinary
// outbound HTTP calls that are not long-lived streams.
const DefaultExternalTimeout = 30 * time.Second

var defaultExternalClient = &http.Client{Timeout: DefaultExternalTimeout}

// DefaultExternal returns the shared bounded client for routine outbound calls.
func DefaultExternal() *http.Client {
	return defaultExternalClient
}

// New returns an HTTP client with timeout, falling back to DefaultExternalTimeout
// when timeout is not positive.
func New(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = DefaultExternalTimeout
	}
	return &http.Client{Timeout: timeout}
}

// WithDefault returns client when provided, otherwise a new bounded HTTP client
// using timeout.
func WithDefault(client *http.Client, timeout time.Duration) *http.Client {
	if client != nil {
		return client
	}
	return New(timeout)
}
