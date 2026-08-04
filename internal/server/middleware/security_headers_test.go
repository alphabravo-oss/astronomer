package middleware

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
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
