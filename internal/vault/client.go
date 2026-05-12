// Package vault — production HashiCorp Vault HTTP client.
//
// The Client interface in resolver.go is satisfied by *vaultClient
// (this file) in production and by a fake in tests. We wrap the
// official github.com/hashicorp/vault/api library rather than rolling
// our own HTTP code because:
//
//   - the auth methods (approle, k8s) have small but easy-to-miss
//     subtleties (form-encoded body for some, JSON for others; token
//     headers vs. wrapping; renewal semantics) the upstream library
//     gets right;
//   - the official library handles X-Vault-Namespace, retry, and the
//     KV v1/v2 path-rewriting cleanly.
//
// The factory is wired in NewResolver. Tests override via
// NewResolverWithFactory so no real network calls run.

package vault

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	vaultapi "github.com/hashicorp/vault/api"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// DefaultClientFactory builds a production *vaultClient. Decrypted auth
// blob arrives as a JSON string; the factory parses it according to the
// connection's auth_method.
func DefaultClientFactory(conn sqlc.VaultConnection, authBlob string) (Client, error) {
	authData, err := DecodeAuthBlob(conn.AuthMethod, authBlob)
	if err != nil {
		return nil, err
	}
	cfg := vaultapi.DefaultConfig()
	cfg.Address = conn.Addr
	// Build a custom transport so we can honour tls_skip_verify +
	// ca_cert_pem without mutating the global http.DefaultTransport.
	if conn.TlsSkipVerify || conn.CaCertPem != "" {
		tlsCfg := &tls.Config{InsecureSkipVerify: conn.TlsSkipVerify} //nolint:gosec
		if conn.CaCertPem != "" {
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM([]byte(conn.CaCertPem)) {
				return nil, errors.New("vault: ca_cert_pem could not be parsed")
			}
			tlsCfg.RootCAs = pool
		}
		cfg.HttpClient = &http.Client{
			Transport: &http.Transport{TLSClientConfig: tlsCfg},
			Timeout:   30 * time.Second,
		}
	}
	api, err := vaultapi.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("build vault api client: %w", err)
	}
	if conn.Namespace != "" {
		api.SetNamespace(conn.Namespace)
	}
	return &vaultClient{
		api:        api,
		authMethod: conn.AuthMethod,
		authData:   authData,
		connName:   conn.Name,
	}, nil
}

// vaultClient is the production Client. It owns the *vaultapi.Client
// plus the auth-method config it needs to re-login on 403.
//
// Token lifecycle:
//   - On first FetchSecret, login is performed and the token cached
//     on the embedded *vaultapi.Client.
//   - On a 403/permission-denied from a kv read, the token is cleared
//     and login is retried once before failing the call.
//   - 5xx responses are not retried — the operator's job is to fix
//     Vault; piling retries from the install hot path doesn't help.
type vaultClient struct {
	api        *vaultapi.Client
	authMethod string
	authData   map[string]string
	connName   string

	mu        sync.Mutex
	tokenSet  bool
}

// FetchSecret implements Client. Tries KV v2 first
// ("<engine>/data/<path>") then falls back to KV v1 ("<engine>/<path>").
// On 403, drops the cached token, re-authenticates once, and retries.
func (c *vaultClient) FetchSecret(ctx context.Context, engine, path string) (map[string]any, error) {
	if err := c.ensureToken(ctx); err != nil {
		return nil, err
	}
	data, err := c.tryFetch(ctx, engine, path)
	if err == nil {
		return data, nil
	}
	if isPermissionDenied(err) {
		c.invalidateToken()
		if err := c.ensureToken(ctx); err != nil {
			return nil, err
		}
		return c.tryFetch(ctx, engine, path)
	}
	return nil, err
}

func (c *vaultClient) tryFetch(ctx context.Context, engine, path string) (map[string]any, error) {
	v2Path := fmt.Sprintf("%s/data/%s", engine, path)
	sec, err := c.api.Logical().ReadWithContext(ctx, v2Path)
	if err == nil && sec != nil {
		// KV v2 wraps the actual fields under "data".
		if inner, ok := sec.Data["data"].(map[string]any); ok {
			return inner, nil
		}
		// KV v2 path returned something but not the v2 shape; treat
		// data verbatim.
		return sec.Data, nil
	}
	// Fallback to KV v1.
	v1Path := fmt.Sprintf("%s/%s", engine, path)
	sec, err = c.api.Logical().ReadWithContext(ctx, v1Path)
	if err != nil {
		return nil, err
	}
	if sec == nil {
		return nil, fmt.Errorf("secret not found at %s", v1Path)
	}
	return sec.Data, nil
}

func (c *vaultClient) ensureToken(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tokenSet {
		return nil
	}
	token, err := c.login(ctx)
	if err != nil {
		return err
	}
	c.api.SetToken(token)
	c.tokenSet = true
	return nil
}

func (c *vaultClient) invalidateToken() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tokenSet = false
	c.api.ClearToken()
}

// login performs the auth-method-specific login dance and returns the
// new client token. Per Vault docs:
//   - token: nothing to do, the token IS the credential.
//   - approle: POST auth/approle/login {role_id, secret_id}
//   - kubernetes: POST auth/kubernetes/login {role, jwt}
//
// Errors are wrapped with the connection name so the operator can find
// the bad connection in a multi-connection setup.
func (c *vaultClient) login(ctx context.Context) (string, error) {
	switch c.authMethod {
	case "token":
		return c.authData["token"], nil
	case "approle":
		body := map[string]any{
			"role_id":   c.authData["role_id"],
			"secret_id": c.authData["secret_id"],
		}
		sec, err := c.api.Logical().WriteWithContext(ctx, "auth/approle/login", body)
		if err != nil {
			return "", fmt.Errorf("approle login on %q: %w", c.connName, err)
		}
		if sec == nil || sec.Auth == nil || sec.Auth.ClientToken == "" {
			return "", fmt.Errorf("approle login on %q returned no token", c.connName)
		}
		return sec.Auth.ClientToken, nil
	case "kubernetes":
		jwt, err := os.ReadFile(c.authData["jwt_path"])
		if err != nil {
			return "", fmt.Errorf("read k8s SA token at %s for %q: %w", c.authData["jwt_path"], c.connName, err)
		}
		body := map[string]any{
			"role": c.authData["role"],
			"jwt":  string(jwt),
		}
		sec, err := c.api.Logical().WriteWithContext(ctx, "auth/kubernetes/login", body)
		if err != nil {
			return "", fmt.Errorf("kubernetes login on %q: %w", c.connName, err)
		}
		if sec == nil || sec.Auth == nil || sec.Auth.ClientToken == "" {
			return "", fmt.Errorf("kubernetes login on %q returned no token", c.connName)
		}
		return sec.Auth.ClientToken, nil
	}
	return "", fmt.Errorf("unknown auth_method %q for connection %q", c.authMethod, c.connName)
}

// isPermissionDenied detects the 403/permission-denied class of error
// from the upstream library. The library returns *vaultapi.ResponseError
// with StatusCode populated for non-2xx; we also fall back to a string
// match because some code paths wrap the error before returning.
func isPermissionDenied(err error) bool {
	if err == nil {
		return false
	}
	var re *vaultapi.ResponseError
	if errors.As(err, &re) {
		return re.StatusCode == http.StatusForbidden
	}
	// Permission-denied responses sometimes lose their structured type
	// when routed through the api.RetryableTransport — fall back to a
	// best-effort substring check.
	return containsCI(err.Error(), "permission denied") ||
		containsCI(err.Error(), "403")
}

func containsCI(s, sub string) bool {
	if len(s) < len(sub) {
		return false
	}
	// avoid pulling strings.EqualFold for the substring search; the
	// inputs are small and ASCII so manual is fine.
	for i := 0; i+len(sub) <= len(s); i++ {
		match := true
		for j := 0; j < len(sub); j++ {
			a := s[i+j]
			b := sub[j]
			if a >= 'A' && a <= 'Z' {
				a += 'a' - 'A'
			}
			if b >= 'A' && b <= 'Z' {
				b += 'a' - 'A'
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
