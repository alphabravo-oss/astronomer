package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
)

func testSSOHandler(t *testing.T) *SSOHandler {
	t.Helper()
	key, err := auth.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	enc, err := auth.NewEncryptor(key)
	if err != nil {
		t.Fatalf("encryptor: %v", err)
	}
	jwt := auth.NewJWTManager("super-secret-state-key", 60)
	mgr := auth.NewSSOManager(enc, jwt, "https://app.example.com")
	encrypted, err := enc.Encrypt("client-secret")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if err := mgr.RegisterProvider("github", "client-id", encrypted, "", nil); err != nil {
		t.Fatalf("register provider: %v", err)
	}
	h := NewSSOHandler(mgr, nil, jwt, "/")
	h.now = func() time.Time { return time.Unix(1_700_000_000, 0).UTC() }
	return h
}

func withProvider(req *http.Request, provider string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("provider", provider)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func TestSSOLoginSetsSignedStateCookie(t *testing.T) {
	h := testSSOHandler(t)
	req := withProvider(httptest.NewRequest(http.MethodGet, "/api/v1/auth/login/github/", nil), "github")
	rec := httptest.NewRecorder()

	h.Login(rec, req)

	res := rec.Result()
	if res.StatusCode != http.StatusFound {
		t.Fatalf("expected redirect, got %d", res.StatusCode)
	}
	cookies := res.Cookies()
	if len(cookies) == 0 {
		t.Fatal("expected SSO state cookie")
	}
	cookie := cookies[0]
	if cookie.Name != "astro_sso_state" {
		t.Fatalf("unexpected cookie %q", cookie.Name)
	}
	loc := res.Header.Get("Location")
	parsed, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("parse redirect location: %v", err)
	}
	state := parsed.Query().Get("state")
	if state == "" {
		t.Fatalf("redirect location missing state: %s", loc)
	}
	if !h.verifyStateCookie(cookie.Value, "github", state) {
		t.Fatal("expected signed cookie to verify")
	}
}

func TestSSOCallbackRejectsMissingStateCookie(t *testing.T) {
	h := testSSOHandler(t)
	h.rememberState("abc123", "github")

	req := withProvider(httptest.NewRequest(http.MethodGet, "/api/v1/auth/callback/github/?code=x&state=abc123", nil), "github")
	rec := httptest.NewRecorder()

	h.Callback(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "cookie missing") {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}

func TestSSOCallbackRejectsTamperedStateCookie(t *testing.T) {
	h := testSSOHandler(t)
	h.rememberState("abc123", "github")
	cookie, err := h.signStateCookie("github", "abc123")
	if err != nil {
		t.Fatalf("sign cookie: %v", err)
	}
	cookie = cookie[:len(cookie)-1] + "A"

	req := withProvider(httptest.NewRequest(http.MethodGet, "/api/v1/auth/callback/github/?code=x&state=abc123", nil), "github")
	req.AddCookie(&http.Cookie{Name: "astro_sso_state", Value: cookie})
	rec := httptest.NewRecorder()

	h.Callback(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "cookie mismatch") {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}

func TestSSOCallbackRejectsExpiredStateCookie(t *testing.T) {
	h := testSSOHandler(t)
	h.rememberState("abc123", "github")
	cookie, err := h.signStateCookie("github", "abc123")
	if err != nil {
		t.Fatalf("sign cookie: %v", err)
	}
	h.now = func() time.Time { return time.Unix(1_700_000_000, 0).UTC().Add(11 * time.Minute) }

	req := withProvider(httptest.NewRequest(http.MethodGet, "/api/v1/auth/callback/github/?code=x&state=abc123", nil), "github")
	req.AddCookie(&http.Cookie{Name: "astro_sso_state", Value: cookie})
	rec := httptest.NewRecorder()

	h.Callback(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "state did not match") && !strings.Contains(rec.Body.String(), "cookie mismatch") {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}
