package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// fakeIdP is an httptest.Server that pretends to be an OIDC IdP. It serves a
// valid discovery document and JWKS for an in-process RSA key, and exposes a
// helper to mint signed ID tokens.
type fakeIdP struct {
	srv     *httptest.Server
	priv    *rsa.PrivateKey
	kid     string
	issuer  string
	hits    *atomic.Int64 // discovery doc fetch counter
	jwksHit *atomic.Int64
}

func newFakeIdP(t *testing.T) *fakeIdP {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa: %v", err)
	}
	idp := &fakeIdP{
		priv:    priv,
		kid:     "test-key-1",
		hits:    &atomic.Int64{},
		jwksHit: &atomic.Int64{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		idp.hits.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                idp.issuer,
			"authorization_endpoint":                idp.issuer + "/auth",
			"token_endpoint":                        idp.issuer + "/token",
			"userinfo_endpoint":                     idp.issuer + "/userinfo",
			"jwks_uri":                              idp.issuer + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		idp.jwksHit.Add(1)
		n := base64.RawURLEncoding.EncodeToString(priv.N.Bytes())
		// Encode public exponent (typically 65537 = 0x010001).
		eBytes := big.NewInt(int64(priv.PublicKey.E)).Bytes()
		e := base64.RawURLEncoding.EncodeToString(eBytes)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]any{{
				"kty": "RSA",
				"kid": idp.kid,
				"alg": "RS256",
				"use": "sig",
				"n":   n,
				"e":   e,
			}},
		})
	})
	srv := httptest.NewServer(mux)
	idp.srv = srv
	idp.issuer = srv.URL
	t.Cleanup(srv.Close)
	return idp
}

func (idp *fakeIdP) signToken(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = idp.kid
	signed, err := tok.SignedString(idp.priv)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return signed
}

func TestDiscoveryFetchAndCache(t *testing.T) {
	idp := newFakeIdP(t)
	c := NewOIDCDiscoveryClient(idp.srv.Client())

	ctx := context.Background()
	d1, err := c.FetchDiscovery(ctx, idp.issuer)
	if err != nil {
		t.Fatalf("FetchDiscovery: %v", err)
	}
	if d1.JWKSURI != idp.issuer+"/jwks" {
		t.Errorf("JWKSURI = %q", d1.JWKSURI)
	}
	if d1.AuthEndpoint == "" || d1.TokenEndpoint == "" {
		t.Error("missing endpoints")
	}

	// Second fetch should be served from the cache.
	if _, err := c.FetchDiscovery(ctx, idp.issuer); err != nil {
		t.Fatalf("FetchDiscovery #2: %v", err)
	}
	if got := idp.hits.Load(); got != 1 {
		t.Errorf("expected 1 discovery hit, got %d", got)
	}

	// Trailing slash on the issuer should normalise to the same cache key.
	if _, err := c.FetchDiscovery(ctx, idp.issuer+"/"); err != nil {
		t.Fatalf("FetchDiscovery trailing-slash: %v", err)
	}
	if got := idp.hits.Load(); got != 1 {
		t.Errorf("expected 1 discovery hit after slash variant, got %d", got)
	}

	c.InvalidateDiscovery(idp.issuer)
	if _, err := c.FetchDiscovery(ctx, idp.issuer); err != nil {
		t.Fatalf("FetchDiscovery post-invalidate: %v", err)
	}
	if got := idp.hits.Load(); got != 2 {
		t.Errorf("expected 2 discovery hits after invalidate, got %d", got)
	}
}

func TestNewOIDCDiscoveryClientUsesBoundedDefaultHTTPClient(t *testing.T) {
	c := NewOIDCDiscoveryClient(nil)
	if c.httpClient == nil {
		t.Fatal("httpClient was not configured")
	}
	if c.httpClient.Timeout != defaultOIDCHTTPTimeout {
		t.Fatalf("httpClient.Timeout = %s, want %s", c.httpClient.Timeout, defaultOIDCHTTPTimeout)
	}
}

func TestDiscoveryRejectsIssuerMismatch(t *testing.T) {
	priv, _ := rsa.GenerateKey(rand.Reader, 2048)
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                 "https://elsewhere.invalid",
			"authorization_endpoint": "https://elsewhere.invalid/auth",
			"token_endpoint":         "https://elsewhere.invalid/token",
			"jwks_uri":               "https://elsewhere.invalid/jwks",
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	_ = priv

	c := NewOIDCDiscoveryClient(srv.Client())
	if _, err := c.FetchDiscovery(context.Background(), srv.URL); err == nil {
		t.Fatal("expected issuer mismatch error")
	}
}

func TestValidateIDTokenSuccess(t *testing.T) {
	idp := newFakeIdP(t)
	c := NewOIDCDiscoveryClient(idp.srv.Client())

	now := time.Now()
	tokenStr := idp.signToken(t, jwt.MapClaims{
		"iss":   idp.issuer,
		"aud":   "astronomer-go",
		"sub":   uuid.NewString(),
		"email": "alice@example.com",
		"name":  "Alice Example",
		"exp":   now.Add(5 * time.Minute).Unix(),
		"iat":   now.Unix(),
	})

	claims, err := c.ValidateIDToken(context.Background(), tokenStr, idp.issuer, "astronomer-go")
	if err != nil {
		t.Fatalf("ValidateIDToken: %v", err)
	}
	if claims.Email != "alice@example.com" {
		t.Errorf("Email = %q", claims.Email)
	}
	if claims.Name != "Alice Example" {
		t.Errorf("Name = %q", claims.Name)
	}
}

func TestValidateIDTokenExpired(t *testing.T) {
	idp := newFakeIdP(t)
	c := NewOIDCDiscoveryClient(idp.srv.Client())

	tokenStr := idp.signToken(t, jwt.MapClaims{
		"iss":   idp.issuer,
		"aud":   "astronomer-go",
		"sub":   "user-1",
		"email": "alice@example.com",
		"exp":   time.Now().Add(-10 * time.Minute).Unix(),
		"iat":   time.Now().Add(-20 * time.Minute).Unix(),
	})
	_, err := c.ValidateIDToken(context.Background(), tokenStr, idp.issuer, "astronomer-go")
	if err == nil {
		t.Fatal("expected expired-token error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "expired") && !strings.Contains(strings.ToLower(err.Error()), "exp") {
		t.Errorf("error = %q, want it to mention expiry", err.Error())
	}
}

func TestValidateIDTokenWrongAudience(t *testing.T) {
	idp := newFakeIdP(t)
	c := NewOIDCDiscoveryClient(idp.srv.Client())

	tokenStr := idp.signToken(t, jwt.MapClaims{
		"iss":   idp.issuer,
		"aud":   "some-other-app",
		"sub":   "user-1",
		"email": "alice@example.com",
		"exp":   time.Now().Add(5 * time.Minute).Unix(),
		"iat":   time.Now().Unix(),
	})
	_, err := c.ValidateIDToken(context.Background(), tokenStr, idp.issuer, "astronomer-go")
	if err == nil {
		t.Fatal("expected audience mismatch error")
	}
	if !strings.Contains(err.Error(), "audience") {
		t.Errorf("error = %q, want audience mention", err.Error())
	}
}

func TestValidateIDTokenWrongIssuer(t *testing.T) {
	idp := newFakeIdP(t)
	c := NewOIDCDiscoveryClient(idp.srv.Client())

	tokenStr := idp.signToken(t, jwt.MapClaims{
		"iss":   "https://attacker.example.com",
		"aud":   "astronomer-go",
		"sub":   "user-1",
		"email": "alice@example.com",
		"exp":   time.Now().Add(5 * time.Minute).Unix(),
		"iat":   time.Now().Unix(),
	})
	_, err := c.ValidateIDToken(context.Background(), tokenStr, idp.issuer, "astronomer-go")
	if err == nil {
		t.Fatal("expected issuer mismatch error")
	}
}

func TestValidateIDTokenInvalidSignature(t *testing.T) {
	idp := newFakeIdP(t)
	c := NewOIDCDiscoveryClient(idp.srv.Client())

	// Sign with a different key — the JWKS won't match.
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss":   idp.issuer,
		"aud":   "astronomer-go",
		"sub":   "user-1",
		"email": "alice@example.com",
		"exp":   time.Now().Add(5 * time.Minute).Unix(),
		"iat":   time.Now().Unix(),
	})
	tok.Header["kid"] = idp.kid
	signed, err := tok.SignedString(other)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	_, err = c.ValidateIDToken(context.Background(), signed, idp.issuer, "astronomer-go")
	if err == nil {
		t.Fatal("expected signature error")
	}
}

func TestRegisterOIDCProviderEndToEnd(t *testing.T) {
	idp := newFakeIdP(t)
	mgr, enc := newTestSSOManager(t)
	mgr.SetDiscoveryClient(NewOIDCDiscoveryClient(idp.srv.Client()))

	encrypted, err := enc.Encrypt("client-secret-xyz")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	if err := mgr.RegisterOIDCProvider(
		context.Background(),
		"keycloak",
		idp.issuer,
		"astronomer-go",
		encrypted,
		"",
		nil,
	); err != nil {
		t.Fatalf("RegisterOIDCProvider: %v", err)
	}

	if !mgr.HasProvider("keycloak") {
		t.Fatal("expected keycloak provider to be registered")
	}

	mgr.mu.RLock()
	p := mgr.providers["keycloak"]
	mgr.mu.RUnlock()

	if p.Kind != "oidc" {
		t.Errorf("Kind = %q, want oidc", p.Kind)
	}
	if p.IssuerURL != idp.issuer {
		t.Errorf("IssuerURL = %q, want %q", p.IssuerURL, idp.issuer)
	}
	if p.Config.Endpoint.AuthURL != idp.issuer+"/auth" {
		t.Errorf("AuthURL = %q", p.Config.Endpoint.AuthURL)
	}
	if p.Config.Endpoint.TokenURL != idp.issuer+"/token" {
		t.Errorf("TokenURL = %q", p.Config.Endpoint.TokenURL)
	}
	// Default scope set should include openid.
	got := strings.Join(p.Scopes, " ")
	if !strings.Contains(got, "openid") {
		t.Errorf("scopes = %q", got)
	}
}

func TestRegisterOIDCProviderRejectsEmptyIssuer(t *testing.T) {
	mgr, enc := newTestSSOManager(t)
	encrypted, _ := enc.Encrypt("secret")

	err := mgr.RegisterOIDCProvider(context.Background(), "keycloak", "", "client", encrypted, "", nil)
	if err == nil {
		t.Fatal("expected empty-issuer error")
	}
}

func TestParseJWKSSkipsUnsupportedKeys(t *testing.T) {
	// A JWKS with one supported RSA key and one unsupported OKP key.
	doc := []byte(`{"keys":[
        {"kty":"OKP","crv":"Ed25519","x":"abc","kid":"ignored"},
        {"kty":"RSA","kid":"ok","n":"` + base64.RawURLEncoding.EncodeToString(big.NewInt(123).Bytes()) +
		`","e":"` + base64.RawURLEncoding.EncodeToString([]byte{0x01, 0x00, 0x01}) + `"}
    ]}`)
	keys, err := parseJWKS(doc)
	if err != nil {
		t.Fatalf("parseJWKS: %v", err)
	}
	if _, ok := keys["ok"]; !ok {
		t.Fatal("expected RSA key under kid=ok")
	}
	if _, ok := keys["ignored"]; ok {
		t.Fatal("did not expect OKP key to be parsed")
	}
}

// Helper: ensure fmt isn't unused if some assertions are removed during edits.
var _ = fmt.Sprint
