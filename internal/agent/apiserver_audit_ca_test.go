package agent

import (
	"net/http"
	"testing"
)

// TestNewAuditHTTPClientUsesCABundle proves the direct-HTTP audit client is
// built with the configured management CA bundle (regression for the finding
// that it trusted only the OS store, so audit POSTs to a private-CA cluster
// silently failed TLS and events were never delivered).
func TestNewAuditHTTPClientUsesCABundle(t *testing.T) {
	pemBytes, _ := genSelfSignedCert(t)

	// With a CA configured, the client carries a transport whose TLS config
	// trusts the pinned bundle.
	c, err := newAuditHTTPClient(&AgentConfig{CACert: string(pemBytes)})
	if err != nil {
		t.Fatalf("newAuditHTTPClient with CA: %v", err)
	}
	tr, ok := c.Transport.(*http.Transport)
	if !ok || tr.TLSClientConfig == nil || tr.TLSClientConfig.RootCAs == nil {
		t.Fatalf("client must trust the configured CA bundle, got transport=%#v", c.Transport)
	}

	// With no CA (public-CA cluster) the client is the plain OS-trust client —
	// unchanged pre-fix behavior, still with a timeout.
	c2, err := newAuditHTTPClient(&AgentConfig{})
	if err != nil {
		t.Fatalf("newAuditHTTPClient without CA: %v", err)
	}
	if c2.Timeout == 0 {
		t.Fatalf("default client must keep a request timeout")
	}
}
