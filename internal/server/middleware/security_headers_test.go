package middleware

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSecurityHeadersAddsBrowserHardeningHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()

	SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(rec, req)

	assertHeader(t, rec, "X-Content-Type-Options", "nosniff")
	assertHeader(t, rec, "Referrer-Policy", "strict-origin-when-cross-origin")
	assertHeader(t, rec, "X-Frame-Options", "DENY")
	assertHeader(t, rec, "Content-Security-Policy", defaultContentSecurityPolicy)
	assertHeader(t, rec, "Strict-Transport-Security", "max-age=31536000; includeSubDomains")
}

func TestSecurityHeadersArgoCDPathAllowsInlineScripts(t *testing.T) {
	for _, path := range []string{"/argocd", "/argocd/", "/argocd/applications", "/argocd/main.abc.js"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(rec, req)
		csp := rec.Header().Get("Content-Security-Policy")
		if csp != argocdContentSecurityPolicy {
			t.Fatalf("%s CSP = %q, want argocd policy", path, csp)
		}
		// The whole point: inline scripts must be permitted for the Argo UI, and
		// the strict default must NOT be the one applied.
		if !containsDirective(csp, "script-src 'self' 'unsafe-inline'") {
			t.Fatalf("%s CSP missing inline script-src: %q", path, csp)
		}
		if csp == defaultContentSecurityPolicy {
			t.Fatalf("%s must not get the strict default CSP", path)
		}
		if got := rec.Header().Get("X-Frame-Options"); got != "SAMEORIGIN" {
			t.Fatalf("%s X-Frame-Options = %q, want SAMEORIGIN", path, got)
		}
	}
	// A path that merely starts with the substring but isn't the argo prefix
	// stays strict.
	req := httptest.NewRequest(http.MethodGet, "/argocdxyz", nil)
	rec := httptest.NewRecorder()
	SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {})).ServeHTTP(rec, req)
	if rec.Header().Get("Content-Security-Policy") != defaultContentSecurityPolicy {
		t.Fatalf("/argocdxyz should get the strict default CSP")
	}
}

func containsDirective(csp, directive string) bool {
	for _, d := range strings.Split(csp, ";") {
		if strings.TrimSpace(d) == directive {
			return true
		}
	}
	return false
}

func TestSecurityHeadersOmitsHSTSForPlainHTTP(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(rec, req)

	if got := rec.Header().Get("Strict-Transport-Security"); got != "" {
		t.Fatalf("Strict-Transport-Security = %q, want empty", got)
	}
}

func TestSecurityHeadersPreservesHandlerOverrides(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'none'")
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(rec, req)

	assertHeader(t, rec, "Content-Security-Policy", "default-src 'none'")
}

func TestRequestIsHTTPS(t *testing.T) {
	if RequestIsHTTPS(nil) {
		t.Fatal("nil request should not be HTTPS")
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if RequestIsHTTPS(req) {
		t.Fatal("plain request should not be HTTPS")
	}

	req.TLS = &tls.ConnectionState{}
	if !RequestIsHTTPS(req) {
		t.Fatal("TLS request should be HTTPS")
	}

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	if !RequestIsHTTPS(req) {
		t.Fatal("X-Forwarded-Proto=https should be HTTPS")
	}
}

func assertHeader(t *testing.T, rec *httptest.ResponseRecorder, key, want string) {
	t.Helper()
	if got := rec.Header().Get(key); got != want {
		t.Fatalf("%s = %q, want %q", key, got, want)
	}
}
