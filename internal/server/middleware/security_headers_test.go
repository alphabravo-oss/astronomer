package middleware

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
)

func TestSecurityHeadersAddsBrowserHardeningHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.42.0.8:4321"
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()

	handler := TrustedRealIP("10.42.0.0/16")(SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})))
	handler.ServeHTTP(rec, req)

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
	if reqctx.RequestIsHTTPS(nil) {
		t.Fatal("nil request should not be HTTPS")
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if reqctx.RequestIsHTTPS(req) {
		t.Fatal("plain request should not be HTTPS")
	}

	req.TLS = &tls.ConnectionState{}
	if !reqctx.RequestIsHTTPS(req) {
		t.Fatal("TLS request should be HTTPS")
	}

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	if reqctx.RequestIsHTTPS(req) {
		t.Fatal("untrusted X-Forwarded-Proto=https should not be HTTPS")
	}
}

func assertHeader(t *testing.T, rec *httptest.ResponseRecorder, key, want string) {
	t.Helper()
	if got := rec.Header().Get(key); got != want {
		t.Fatalf("%s = %q, want %q", key, got, want)
	}
}
