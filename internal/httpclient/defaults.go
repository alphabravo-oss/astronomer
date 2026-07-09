package httpclient

import (
	"net/http"
	"time"
)

// DefaultExternalTimeout is the fallback wall-clock budget for ordinary
// outbound HTTP calls that are not long-lived streams.
const DefaultExternalTimeout = 30 * time.Second

// defaultExternalClient is a SafeClient: dial-time public-IP enforcement so
// operator/DB-supplied URLs cannot rebind to loopback/metadata after a
// GuardPublicHost pre-check (SEC-03). Callers that need private destinations
// must use an explicit allow-private client, not this default.
var defaultExternalClient = SafeClient(DefaultExternalTimeout)

// DefaultExternal returns the shared bounded SafeClient for routine outbound
// calls that may target operator- or DB-supplied URLs.
func DefaultExternal() *http.Client {
	return defaultExternalClient
}

// New returns a SafeClient with timeout, falling back to DefaultExternalTimeout
// when timeout is not positive. Prefer this over &http.Client{} for any
// server-side fetch of an external URL.
func New(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = DefaultExternalTimeout
	}
	return SafeClient(timeout)
}

// WithDefault returns client when provided, otherwise a SafeClient with timeout.
func WithDefault(client *http.Client, timeout time.Duration) *http.Client {
	if client != nil {
		return client
	}
	return New(timeout)
}
