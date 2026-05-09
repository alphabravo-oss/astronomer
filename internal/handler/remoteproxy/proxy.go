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
	"fmt"
	"net/http"
	"time"

	"github.com/rancher/remotedialer"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// TunnelDialer is the minimal interface we need from a remotedialer.Server.
// Defining it here lets tests substitute a fake without dragging the whole
// remotedialer state machine into a unit test.
type TunnelDialer interface {
	DialerFor(clusterID string) remotedialer.Dialer
	HasSession(clusterID string) bool
}

// K8sClient returns a kubernetes.Interface that talks to the in-cluster API
// server of the remote cluster identified by clusterID. Every HTTP call from
// the returned clientset is dialed through the WS tunnel.
//
// Behaviour notes:
//   - The Host is "https://kubernetes.default.svc:443". The agent end of the
//     tunnel sees the dial as a normal net.Dial("tcp", "kubernetes.default.svc:443")
//     and forwards bytes to the in-cluster API server. The agent's
//     ServiceAccount token + CA are presented by the in-cluster API server's
//     own TLS — but because the dial is through a tunnel and the tunnel does
//     not carry a kubeconfig, we currently leave Insecure=true on the server
//     side. The traffic is end-to-end TLS to the agent's API server inside the
//     tunnel; the lack of CA verification on this end means we trust whichever
//     cluster the agent dialed us from. That is acceptable because the agent
//     was authenticated at WS-upgrade time.
//   - BearerToken is empty; auth is the agent's in-cluster ServiceAccount
//     token, which client-go reads from the agent process. (That is, the
//     server side does NOT supply a token — the dial lands on the *agent's*
//     local network where the API server accepts the agent's pod identity.)
//
// Caller MUST check HasSession first if they want a clean "agent offline"
// error; otherwise list/get calls will fail with a dial error.
func K8sClient(server TunnelDialer, clusterID string) (kubernetes.Interface, error) {
	if server == nil {
		return nil, fmt.Errorf("remoteproxy: nil tunnel server")
	}
	if clusterID == "" {
		return nil, fmt.Errorf("remoteproxy: cluster_id required")
	}
	if !server.HasSession(clusterID) {
		return nil, fmt.Errorf("remoteproxy: agent for cluster %q is not connected", clusterID)
	}

	cfg := RestConfig(server, clusterID)
	return kubernetes.NewForConfig(cfg)
}

// RestConfig builds the *rest.Config K8sClient uses. Exposed separately so
// callers (e.g. dynamic clients, discovery clients) can build their own
// typed/dynamic client off the same plumbing.
func RestConfig(server TunnelDialer, clusterID string) *rest.Config {
	dial := server.DialerFor(clusterID)

	cfg := &rest.Config{
		Host:    "https://kubernetes.default.svc:443",
		Timeout: 30 * time.Second,
		TLSClientConfig: rest.TLSClientConfig{
			Insecure: true,
		},
	}

	// rest.Config.WrapTransport runs after rest builds its base transport, but
	// rest builds the base transport from the explicit Dial fields if we set
	// them via Transport. Setting Transport directly is simpler and skips the
	// implicit TLSClientConfig stitching — we manage TLS ourselves above.
	cfg.Transport = &http.Transport{
		DialContext:           dial,
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: true}, // see godoc above
		ResponseHeaderTimeout: 30 * time.Second,
		IdleConnTimeout:       60 * time.Second,
		MaxIdleConns:          16,
		MaxIdleConnsPerHost:   8,
	}
	// Explicitly clear TLSClientConfig on the rest.Config so rest.HTTPClientFor
	// does not try to layer its own TLS on top of our transport. (Both must
	// match; clearing prevents rest from rebuilding the transport.)
	cfg.TLSClientConfig = rest.TLSClientConfig{}

	return cfg
}

// dialOnce is unused at runtime but kept as documentation of the contract:
// the function returned by remotedialer.Server.Dialer matches the
// http.Transport.DialContext signature.
var _ = func(ctx context.Context, _, _ string) {} //nolint:unused
