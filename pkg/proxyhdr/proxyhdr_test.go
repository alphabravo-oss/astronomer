package proxyhdr

import (
	"net/http"
	"testing"
)

// TestForwardedHeadersDropSpoofableAndForbiddenHeaders asserts the allowlist
// strips identity-spoofing / forbidden headers (X-Remote-*, Impersonate-*,
// Authorization, Cookie, Host, X-Forwarded-*) as well as any novel header,
// while still forwarding legitimately-needed headers like Content-Type.
func TestForwardedHeadersDropSpoofableAndForbiddenHeaders(t *testing.T) {
	// Build an inbound request as a browser/dashboard might send it, with
	// case-varied header names to exercise case-insensitive matching.
	in := http.Header{}
	in.Set("X-Remote-User", "system:admin")
	in.Set("X-Remote-Group", "system:masters")
	in.Set("X-Remote-Extra-Scopes", "cluster-admin")
	in.Set("Impersonate-User", "system:admin")
	in.Set("Impersonate-Group", "system:masters")
	in.Set("Authorization", "Bearer astronomer-jwt")
	in.Set("Cookie", "session=abc")
	in.Set("Host", "evil.example.com")
	in.Set("X-Forwarded-For", "10.0.0.1")
	in.Set("X-Whatever", "novel-attacker-header")
	in.Set("Content-Type", "application/json")
	in.Set("Accept", "application/json")

	// Simulate the forwarding loop used by both proxy sites.
	forwarded := http.Header{}
	for key, values := range in {
		if !ShouldForwardRequestHeader(key) {
			continue
		}
		forwarded[http.CanonicalHeaderKey(key)] = values
	}

	mustBeAbsent := []string{
		"X-Remote-User",
		"X-Remote-Group",
		"X-Remote-Extra-Scopes",
		"Impersonate-User",
		"Impersonate-Group",
		"Authorization",
		"Cookie",
		"Host",
		"X-Forwarded-For",
		"X-Whatever",
	}
	for _, h := range mustBeAbsent {
		if v := forwarded.Get(h); v != "" {
			t.Errorf("header %q must be stripped before forwarding, got %q", h, v)
		}
	}

	mustSurvive := map[string]string{
		"Content-Type": "application/json",
		"Accept":       "application/json",
	}
	for h, want := range mustSurvive {
		if got := forwarded.Get(h); got != want {
			t.Errorf("header %q must be forwarded; want %q, got %q", h, want, got)
		}
	}
}

// TestShouldForwardRequestHeaderCaseInsensitive guards the case-insensitive
// contract relied on by both proxy call sites.
func TestShouldForwardRequestHeaderCaseInsensitive(t *testing.T) {
	cases := map[string]bool{
		"content-type":   true,
		"Content-Type":   true,
		"CONTENT-TYPE":   true,
		" content-type":  true, // trimmed
		"accept":         true,
		"content-length": true,
		"user-agent":     true,

		"authorization":         false,
		"X-Remote-User":         false,
		"x-remote-extra-scopes": false,
		"Impersonate-User":      false,
		"X-Forwarded-For":       false,
		"cookie":                false,
		"host":                  false,
		"x-whatever":            false,
	}
	for name, want := range cases {
		if got := ShouldForwardRequestHeader(name); got != want {
			t.Errorf("ShouldForwardRequestHeader(%q) = %v, want %v", name, got, want)
		}
	}
}
