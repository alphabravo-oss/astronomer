//go:build integration

package handler

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/golang-jwt/jwt/v5"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/identity"
	"github.com/alphabravocompany/astronomer-go/internal/testdb"
)

func TestSharedIdentityBrowserSessionLifecycle(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := testdb.Pool(t)
	q := sqlc.New(pool)
	user, err := q.CreateUser(ctx, sqlc.CreateUserParams{Email: "browser@example.test", Username: "browser", IsActive: true})
	if err != nil {
		t.Fatal(err)
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var issuerURL, challenge, raw string
	expires := time.Now().Add(2 * time.Minute)
	issuer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/keys" {
			_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "key", Algorithm: "RS256", Use: "sig"}}})
			return
		}
		if r.URL.Path != "/token" {
			http.NotFound(w, r)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Error(err)
			http.Error(w, "bad form", 400)
			return
		}
		id, secret, _ := r.BasicAuth()
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if id != "astronomer" || secret != "test-secret" || r.Form.Get("code") != "single-use-code" || challenge != base64.RawURLEncoding.EncodeToString(sum[:]) {
			http.Error(w, "bad proof", http.StatusUnauthorized)
			return
		}
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"iss": issuerURL, "aud": "astronomer", "sub": "subject-1", "iat": time.Now().Add(-time.Second).Unix(), "exp": expires.Unix()})
		token.Header["kid"] = "key"
		var e error
		raw, e = token.SignedString(key)
		if e != nil {
			t.Error(e)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": raw, "token_type": "Bearer", "expires_in": 120})
	}))
	t.Cleanup(issuer.Close)
	issuerURL = issuer.URL
	_, err = q.CreateSharedIdentityBinding(ctx, sqlc.CreateSharedIdentityBindingParams{Issuer: issuerURL, Subject: "subject-1", UserID: user.ID, LinkedBy: user.ID})
	if err != nil {
		t.Fatal(err)
	}
	svc := identity.New(config.IdentitySettings{Mode: "both", Issuer: issuerURL, ClientID: "astronomer", ClientSecret: "test-secret", JWKSURL: issuerURL + "/keys", AuthorizationURL: issuerURL + "/authorize", TokenURL: issuerURL + "/token", RedirectURI: "https://product.example/api/v1/auth/callback/identity/"}, q, issuer.Client())
	manager := auth.MustNewJWTManager("test-shared-identity-browser-key", 60)
	manager.SetIdentityValidator(svc.ValidateSession)
	manager.SetRevocationChecker(NewJWTRevocationChecker(q))
	h := NewSSOHandler(svc, manager, "/")
	h.SetRunTx(func(ctx context.Context, fn func(SSOCallbackTx) error) error {
		tx, err := pool.Begin(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if err := fn(sqlc.New(tx)); err != nil {
			return err
		}
		return tx.Commit(ctx)
	})
	login := httptest.NewRecorder()
	h.Login(login, httptest.NewRequest("GET", "https://product.example/api/v1/auth/login/identity/", nil))
	if login.Code != 302 {
		t.Fatalf("login %d: %s", login.Code, login.Body.String())
	}
	redirect, err := url.Parse(login.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	challenge = redirect.Query().Get("code_challenge")
	if challenge == "" || redirect.Query().Get("code_challenge_method") != "S256" {
		t.Fatal("missing PKCE")
	}
	callback := httptest.NewRequest("GET", "https://product.example/api/v1/auth/callback/identity/?code=single-use-code&state="+redirect.Query().Get("state"), nil)
	for _, cookie := range login.Result().Cookies() {
		callback.AddCookie(cookie)
	}
	result := httptest.NewRecorder()
	h.Callback(result, callback)
	if result.Code != 303 {
		t.Fatalf("callback %d: %s", result.Code, result.Body.String())
	}
	var access string
	for _, cookie := range result.Result().Cookies() {
		if cookie.Value == raw {
			t.Fatal("upstream credential leaked to browser")
		}
		if cookie.Name == "__Host-"+auth.SessionCookieName && cookie.MaxAge >= 0 {
			access = cookie.Value
			if !cookie.Secure || !cookie.HttpOnly {
				t.Fatal("unsafe session cookie")
			}
		}
	}
	claims, err := manager.ValidateTokenContext(ctx, access)
	if err != nil {
		t.Fatal(err)
	}
	if claims.UserID != user.ID || claims.IdentitySubject != "subject-1" || claims.ExpiresAt.After(expires) || claims.TokenType != auth.AccessToken {
		t.Fatal("invalid product session")
	}
	bad := callback.Clone(ctx)
	bad.URL.RawQuery = "code=single-use-code&state=attacker"
	denied := httptest.NewRecorder()
	h.Callback(denied, bad)
	if denied.Code != 401 {
		t.Fatalf("state mismatch: %d", denied.Code)
	}
	// Logout uses the existing product revocation path, never a provider refresh.
	local := NewAuthHandlerWithTokens(q, q, manager)
	local.SetRevocationQuerier(q)
	local.SetRunTx(func(ctx context.Context, fn func(AuthMutationTx) error) error {
		tx, err := pool.Begin(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if err := fn(sqlc.New(tx)); err != nil {
			return err
		}
		return tx.Commit(ctx)
	})
	logout := httptest.NewRequest("POST", "https://product.example/api/v1/auth/logout/", nil)
	logout.Header.Set("Authorization", "Bearer "+access)
	out := httptest.NewRecorder()
	local.Logout(out, logout)
	if out.Code != 200 {
		t.Fatalf("logout: %d %s", out.Code, out.Body.String())
	}
	if _, err := manager.ValidateTokenContext(ctx, access); err == nil {
		t.Fatal("logged-out session accepted")
	}
	if strings.Contains(result.Body.String(), raw) {
		t.Fatal("upstream token in body")
	}
}
