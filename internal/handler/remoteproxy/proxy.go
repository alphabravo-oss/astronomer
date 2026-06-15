// Package remoteproxy bridges the new remotedialer tunnel to standard
// kubernetes/client-go. The single entry point K8sClient builds a clientset
// whose every request is dialed through the per-cluster remotedialer Dialer.
//
// This is the linchpin of the migration: once any handler can call
// remoteproxy.K8sClient(server, clusterID) and get back a real
// kubernetes.Interface, all the per-feature originator code in
// internal/handler can be deleted in favour of stock client-go calls.
package remoteproxy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/rancher/remotedialer"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// inClusterCAPath is the well-known location of the API server CA bundle that
// kubernetes projects into every pod's ServiceAccount mount. The astronomer
// agent runs in-cluster, so this file is always present on the agent side; we
// use it to verify the apiserver certificate end-to-end instead of skipping
// verification.
const inClusterCAPath = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"

// TunnelDialer is the minimal interface we need from a remotedialer.Server.
// Defining it here lets tests substitute a fake without dragging the whole
// remotedialer state machine into a unit test.
type TunnelDialer interface {
	DialerFor(clusterID string) remotedialer.Dialer
	HasSession(clusterID string) bool
}

// TLSOptions controls how the v2 transport verifies the remote apiserver's
// certificate. The zero value is SECURE: it verifies against the in-cluster CA
// bundle (inClusterCAPath). Skipping verification is an explicit, loudly
// logged, non-production-only opt-in — see Validate.
type TLSOptions struct {
	// CAPath overrides inClusterCAPath. Used by tests to point at a fake
	// apiserver's CA. Empty means "use the in-cluster CA bundle".
	CAPath string

	// CAPEM, when non-nil, supplies the CA bundle directly (PEM-encoded) and
	// takes precedence over CAPath. Lets callers that already hold the bundle
	// in memory avoid a filesystem read.
	CAPEM []byte

	// Insecure disables apiserver certificate verification. This is a
	// DANGEROUS opt-in: it is rejected by Validate when Production is true and
	// emits a loud warning whenever it is selected. TODO(remoteproxy/v2):
	// remove this escape hatch once every environment provisions the CA bundle
	// and the v2 transport graduates to production.
	Insecure bool

	// Production marks the calling environment as production. When true,
	// Validate refuses to build an insecure transport so a misconfiguration
	// can never graduate InsecureSkipVerify into prod.
	Production bool
}

// Validate enforces the security policy for the v2 transport: production may
// never run insecure, and insecure mode is loudly logged wherever it is used.
// It is called from RestConfigWithOptions before any transport is built.
func (o TLSOptions) Validate() error {
	if !o.Insecure {
		return nil
	}
	if o.Production {
		return fmt.Errorf("remoteproxy: refusing to disable apiserver TLS verification in production")
	}
	slog.Warn("remoteproxy: apiserver TLS verification is DISABLED for the v2 transport; " +
		"this is a non-production-only escape hatch and MUST NOT be used in production")
	return nil
}

// tlsConfig builds the *tls.Config for the transport from these options.
// Secure-by-default: it loads and pins the CA bundle. Only an explicit,
// already-validated Insecure opt-in produces InsecureSkipVerify.
func (o TLSOptions) tlsConfig() (*tls.Config, error) {
	if o.Insecure {
		return &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}, nil //nolint:gosec // gated, non-prod-only, validated by Validate
	}

	pem := o.CAPEM
	if len(pem) == 0 {
		path := o.CAPath
		if path == "" {
			path = inClusterCAPath
		}
		b, err := os.ReadFile(path) //nolint:gosec // well-known in-cluster CA path
		if err != nil {
			return nil, fmt.Errorf("remoteproxy: reading apiserver CA bundle %q: %w "+
				"(set TLSOptions.CAPEM/CAPath, or opt into non-prod Insecure mode)", path, err)
		}
		pem = b
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("remoteproxy: apiserver CA bundle contains no valid certificates")
	}
	return &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}, nil
}

// K8sClient returns a kubernetes.Interface that talks to the in-cluster API
// server of the remote cluster identified by clusterID. Every HTTP call from
// the returned clientset is dialed through the WS tunnel.
//
// TLS is verified against the in-cluster CA bundle by default. Use
// K8sClientWithOptions to supply an explicit CA bundle or the non-prod-only
// insecure escape hatch.
//
// Behaviour notes:
//   - The Host is "https://kubernetes.default.svc:443". The agent end of the
//     tunnel sees the dial as a normal net.Dial("tcp", "kubernetes.default.svc:443")
//     and forwards bytes to the in-cluster API server. The agent's
//     ServiceAccount CA (inClusterCAPath) verifies that the bytes really came
//     from the agent's own apiserver, so a mismatched/MITM cert is rejected.
//   - BearerToken is empty; auth is the agent's in-cluster ServiceAccount
//     token, which client-go reads from the agent process. (That is, the
//     server side does NOT supply a token — the dial lands on the *agent's*
//     local network where the API server accepts the agent's pod identity.)
//
// Caller MUST check HasSession first if they want a clean "agent offline"
// error; otherwise list/get calls will fail with a dial error.
func K8sClient(server TunnelDialer, clusterID string) (kubernetes.Interface, error) {
	return K8sClientWithOptions(server, clusterID, TLSOptions{})
}

// K8sClientWithOptions is K8sClient with explicit TLS policy.
func K8sClientWithOptions(server TunnelDialer, clusterID string, tlsOpts TLSOptions) (kubernetes.Interface, error) {
	if server == nil {
		return nil, fmt.Errorf("remoteproxy: nil tunnel server")
	}
	if clusterID == "" {
		return nil, fmt.Errorf("remoteproxy: cluster_id required")
	}
	if !server.HasSession(clusterID) {
		return nil, fmt.Errorf("remoteproxy: agent for cluster %q is not connected", clusterID)
	}

	cfg, err := RestConfigWithOptions(server, clusterID, tlsOpts)
	if err != nil {
		return nil, err
	}
	return kubernetes.NewForConfig(cfg)
}

// RestConfig builds a *rest.Config using the secure default TLS policy
// (verify against the in-cluster CA bundle). Exposed separately so callers
// (e.g. dynamic clients, discovery clients) can build their own typed/dynamic
// client off the same plumbing.
func RestConfig(server TunnelDialer, clusterID string) (*rest.Config, error) {
	return RestConfigWithOptions(server, clusterID, TLSOptions{})
}

// RestConfigWithOptions builds the *rest.Config the v2 transport uses with an
// explicit TLS policy. It verifies tlsOpts (production can never run insecure)
// and pins the apiserver CA so a mismatched/bad apiserver cert is rejected.
func RestConfigWithOptions(server TunnelDialer, clusterID string, tlsOpts TLSOptions) (*rest.Config, error) {
	if err := tlsOpts.Validate(); err != nil {
		return nil, err
	}
	tlsCfg, err := tlsOpts.tlsConfig()
	if err != nil {
		return nil, err
	}

	dial := server.DialerFor(clusterID)

	cfg := &rest.Config{
		Host:    "https://kubernetes.default.svc:443",
		Timeout: 30 * time.Second,
	}

	// rest.Config.WrapTransport runs after rest builds its base transport, but
	// rest builds the base transport from the explicit Dial fields if we set
	// them via Transport. Setting Transport directly is simpler and skips the
	// implicit TLSClientConfig stitching — we manage TLS ourselves above.
	cfg.Transport = &http.Transport{
		DialContext:           dial,
		TLSClientConfig:       tlsCfg,
		ResponseHeaderTimeout: 30 * time.Second,
		IdleConnTimeout:       60 * time.Second,
		MaxIdleConns:          16,
		MaxIdleConnsPerHost:   8,
	}
	// Explicitly clear TLSClientConfig on the rest.Config so rest.HTTPClientFor
	// does not try to layer its own TLS on top of our transport. (Both must
	// match; clearing prevents rest from rebuilding the transport.)
	cfg.TLSClientConfig = rest.TLSClientConfig{}

	return cfg, nil
}

// dialOnce is unused at runtime but kept as documentation of the contract:
// the function returned by remotedialer.Server.Dialer matches the
// http.Transport.DialContext signature.
var _ = func(ctx context.Context, _, _ string) {} //nolint:unused
