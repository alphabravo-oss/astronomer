package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// defaultDiscoveryTTL controls how long a fetched OIDC discovery document is
// considered fresh. Ten minutes is a reasonable default: long enough to avoid
// hitting the IdP on every login, short enough that key rotation / endpoint
// changes propagate within a single coffee break. Crucially this is a
// *positive* cache — on token validation failure we explicitly invalidate.
const defaultDiscoveryTTL = 10 * time.Minute

// defaultJWKSTTL is the JWKS cache TTL. Keeping this slightly shorter than the
// discovery TTL is deliberate: most IdPs rotate keys far more frequently than
// they change endpoints. On signature failure we always re-fetch.
const defaultJWKSTTL = 5 * time.Minute

// OIDCDiscovery is a subset of the OpenID Connect discovery document, covering
// only the fields astronomer-go consumes today.
type OIDCDiscovery struct {
	Issuer                string   `json:"issuer"`
	AuthEndpoint          string   `json:"authorization_endpoint"`
	TokenEndpoint         string   `json:"token_endpoint"`
	UserinfoEndpoint      string   `json:"userinfo_endpoint"`
	JWKSURI               string   `json:"jwks_uri"`
	EndSessionEndpoint    string   `json:"end_session_endpoint"`
	ScopesSupported       []string `json:"scopes_supported"`
	ResponseTypesSupports []string `json:"response_types_supported"`
	IDTokenSigningAlgs    []string `json:"id_token_signing_alg_values_supported"`
}

// OIDCClaims captures the standard ID-token claims we read.
type OIDCClaims struct {
	jwt.RegisteredClaims
	Email             string   `json:"email"`
	EmailVerified     bool     `json:"email_verified"`
	Name              string   `json:"name"`
	GivenName         string   `json:"given_name"`
	FamilyName        string   `json:"family_name"`
	PreferredUsername string   `json:"preferred_username"`
	Picture           string   `json:"picture"`
	Groups            []string `json:"groups"`
	HostedDomain      string   `json:"hd"`
}

// OIDCDiscoveryClient fetches and caches OIDC discovery documents and the
// associated JWKS. It is safe for concurrent use.
type OIDCDiscoveryClient struct {
	httpClient   *http.Client
	discoveryTTL time.Duration
	jwksTTL      time.Duration

	mu        sync.RWMutex
	discovery map[string]*discoveryEntry // keyed by issuer URL (trim trailing slash)
	jwks      map[string]*jwksEntry      // keyed by JWKS URI
}

type discoveryEntry struct {
	doc       *OIDCDiscovery
	expiresAt time.Time
}

type jwksEntry struct {
	keys      map[string]any // kid -> *rsa.PublicKey or *ecdsa.PublicKey
	expiresAt time.Time
}

// NewOIDCDiscoveryClient builds a client with sensible defaults. Pass nil to
// httpClient to use http.DefaultClient.
func NewOIDCDiscoveryClient(httpClient *http.Client) *OIDCDiscoveryClient {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &OIDCDiscoveryClient{
		httpClient:   httpClient,
		discoveryTTL: defaultDiscoveryTTL,
		jwksTTL:      defaultJWKSTTL,
		discovery:    make(map[string]*discoveryEntry),
		jwks:         make(map[string]*jwksEntry),
	}
}

// FetchDiscovery returns the cached discovery document for the issuer or
// fetches a fresh one. Issuer is normalised by trimming a trailing slash to
// match the conventions in https://openid.net/specs/openid-connect-discovery-1_0.html.
func (c *OIDCDiscoveryClient) FetchDiscovery(ctx context.Context, issuer string) (*OIDCDiscovery, error) {
	issuer = strings.TrimRight(issuer, "/")
	if issuer == "" {
		return nil, errors.New("oidc discovery: issuer must not be empty")
	}

	// Fast-path: cached and fresh.
	c.mu.RLock()
	if entry, ok := c.discovery[issuer]; ok && time.Now().Before(entry.expiresAt) {
		doc := entry.doc
		c.mu.RUnlock()
		return doc, nil
	}
	c.mu.RUnlock()

	// Cache miss — fetch.
	url := issuer + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery: fetch %s: %w", url, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MiB cap
	if err != nil {
		return nil, fmt.Errorf("oidc discovery: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oidc discovery: %s returned %d: %s", url, resp.StatusCode, string(body))
	}

	var doc OIDCDiscovery
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("oidc discovery: parse %s: %w", url, err)
	}
	if doc.Issuer == "" || doc.AuthEndpoint == "" || doc.TokenEndpoint == "" || doc.JWKSURI == "" {
		return nil, fmt.Errorf("oidc discovery: %s is missing required fields", url)
	}
	// Per spec, the issuer claim in the doc must match the supplied issuer.
	if strings.TrimRight(doc.Issuer, "/") != issuer {
		return nil, fmt.Errorf("oidc discovery: issuer mismatch: requested %q got %q", issuer, doc.Issuer)
	}

	c.mu.Lock()
	c.discovery[issuer] = &discoveryEntry{
		doc:       &doc,
		expiresAt: time.Now().Add(c.discoveryTTL),
	}
	c.mu.Unlock()

	return &doc, nil
}

// InvalidateDiscovery removes any cached discovery document and JWKS for the
// supplied issuer. Callers should invoke this whenever a previously-trusted
// document fails to validate a token, so the next attempt picks up rotated
// endpoints/keys.
func (c *OIDCDiscoveryClient) InvalidateDiscovery(issuer string) {
	issuer = strings.TrimRight(issuer, "/")
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry, ok := c.discovery[issuer]; ok {
		delete(c.jwks, entry.doc.JWKSURI)
		delete(c.discovery, issuer)
	}
}

// fetchJWKS returns the public-key map for the given JWKS URI, fetching and
// caching as needed.
func (c *OIDCDiscoveryClient) fetchJWKS(ctx context.Context, jwksURI string) (map[string]any, error) {
	if jwksURI == "" {
		return nil, errors.New("jwks: empty uri")
	}
	c.mu.RLock()
	if entry, ok := c.jwks[jwksURI]; ok && time.Now().Before(entry.expiresAt) {
		keys := entry.keys
		c.mu.RUnlock()
		return keys, nil
	}
	c.mu.RUnlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jwksURI, nil)
	if err != nil {
		return nil, fmt.Errorf("jwks: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("jwks: fetch %s: %w", jwksURI, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("jwks: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("jwks: %s returned %d: %s", jwksURI, resp.StatusCode, string(body))
	}

	keys, err := parseJWKS(body)
	if err != nil {
		return nil, fmt.Errorf("jwks: parse %s: %w", jwksURI, err)
	}

	c.mu.Lock()
	c.jwks[jwksURI] = &jwksEntry{keys: keys, expiresAt: time.Now().Add(c.jwksTTL)}
	c.mu.Unlock()

	return keys, nil
}

// invalidateJWKS removes any cached entry for the given URI.
func (c *OIDCDiscoveryClient) invalidateJWKS(jwksURI string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.jwks, jwksURI)
}

// jwk is the on-the-wire JSON representation of a single JSON Web Key.
type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Alg string `json:"alg"`
	Use string `json:"use"`
	// RSA
	N string `json:"n"`
	E string `json:"e"`
	// EC
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

// parseJWKS turns a raw JWKS document into a kid -> public-key map. Keys
// without a kid are stored under the empty string and used as a fallback when
// a token has no kid header.
func parseJWKS(raw []byte) (map[string]any, error) {
	var doc struct {
		Keys []jwk `json:"keys"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	out := make(map[string]any, len(doc.Keys))
	for _, k := range doc.Keys {
		// Filter out non-signing keys when use is set.
		if k.Use != "" && k.Use != "sig" {
			continue
		}
		key, err := jwkToPublicKey(k)
		if err != nil {
			// Skip unsupported entries instead of failing the whole set;
			// IdPs sometimes mix algorithms we don't support.
			continue
		}
		out[k.Kid] = key
	}
	if len(out) == 0 {
		return nil, errors.New("no usable keys in jwks")
	}
	return out, nil
}

// jwkToPublicKey converts a single JWK into a Go public-key value. We support
// RSA (RS256/384/512) and EC (ES256/384/512); HMAC and OKP are intentionally
// unsupported for ID-token verification.
func jwkToPublicKey(k jwk) (any, error) {
	switch strings.ToUpper(k.Kty) {
	case "RSA":
		nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
		if err != nil {
			return nil, fmt.Errorf("rsa modulus: %w", err)
		}
		eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
		if err != nil {
			return nil, fmt.Errorf("rsa exponent: %w", err)
		}
		e := 0
		for _, b := range eBytes {
			e = e<<8 | int(b)
		}
		if e == 0 {
			return nil, errors.New("rsa exponent zero")
		}
		return &rsa.PublicKey{
			N: new(big.Int).SetBytes(nBytes),
			E: e,
		}, nil
	case "EC":
		var curve elliptic.Curve
		switch k.Crv {
		case "P-256":
			curve = elliptic.P256()
		case "P-384":
			curve = elliptic.P384()
		case "P-521":
			curve = elliptic.P521()
		default:
			return nil, fmt.Errorf("unsupported ec curve %q", k.Crv)
		}
		xBytes, err := base64.RawURLEncoding.DecodeString(k.X)
		if err != nil {
			return nil, fmt.Errorf("ec x: %w", err)
		}
		yBytes, err := base64.RawURLEncoding.DecodeString(k.Y)
		if err != nil {
			return nil, fmt.Errorf("ec y: %w", err)
		}
		return &ecdsa.PublicKey{
			Curve: curve,
			X:     new(big.Int).SetBytes(xBytes),
			Y:     new(big.Int).SetBytes(yBytes),
		}, nil
	default:
		return nil, fmt.Errorf("unsupported kty %q", k.Kty)
	}
}

// ValidateIDToken verifies signature, issuer, audience and standard time
// claims on the supplied ID token. On failure it invalidates the JWKS cache
// for the issuer so the next call refreshes — this handles the rotated-key
// case without operator intervention.
func (c *OIDCDiscoveryClient) ValidateIDToken(ctx context.Context, idToken, issuer, audience string) (*OIDCClaims, error) {
	doc, err := c.FetchDiscovery(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("validate id token: %w", err)
	}

	claims, err := c.validateWithJWKS(ctx, idToken, doc.JWKSURI, doc.Issuer, audience)
	if err == nil {
		return claims, nil
	}

	// Likely a key rotation. Drop the cache and try once more.
	c.invalidateJWKS(doc.JWKSURI)
	claims, retryErr := c.validateWithJWKS(ctx, idToken, doc.JWKSURI, doc.Issuer, audience)
	if retryErr != nil {
		return nil, fmt.Errorf("validate id token: %w", err)
	}
	return claims, nil
}

func (c *OIDCDiscoveryClient) validateWithJWKS(ctx context.Context, idToken, jwksURI, issuer, audience string) (*OIDCClaims, error) {
	keys, err := c.fetchJWKS(ctx, jwksURI)
	if err != nil {
		return nil, err
	}

	keyfunc := func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		if k, ok := keys[kid]; ok {
			return k, nil
		}
		// Fallback: tokens without a kid match the only available key, or the
		// empty-kid entry if present.
		if k, ok := keys[""]; ok {
			return k, nil
		}
		if len(keys) == 1 {
			for _, only := range keys {
				return only, nil
			}
		}
		return nil, fmt.Errorf("no jwks key matches kid %q", kid)
	}

	claims := &OIDCClaims{}
	parser := jwt.NewParser(
		jwt.WithIssuer(issuer),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(30*time.Second),
		jwt.WithValidMethods([]string{
			"RS256", "RS384", "RS512",
			"ES256", "ES384", "ES512",
			"PS256", "PS384", "PS512",
		}),
	)
	if _, err := parser.ParseWithClaims(idToken, claims, keyfunc); err != nil {
		return nil, fmt.Errorf("parse id token: %w", err)
	}

	if audience != "" {
		if !claimsHaveAudience(claims.Audience, audience) {
			return nil, fmt.Errorf("id token audience %v does not contain expected %q", []string(claims.Audience), audience)
		}
	}

	_ = ctx // ctx is held for future hooks (logging, metrics)
	return claims, nil
}

func claimsHaveAudience(got jwt.ClaimStrings, want string) bool {
	for _, a := range got {
		if a == want {
			return true
		}
	}
	return false
}
