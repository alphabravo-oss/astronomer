package httpclient

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"
)

// ClientOptions configures a dial-guarded HTTP client (SEC-03).
type ClientOptions struct {
	// Timeout is the client wall-clock budget. Non-positive falls back to
	// DefaultExternalTimeout.
	Timeout time.Duration
	// TLSConfig is applied to the transport when non-nil (SIEM CA / skip-verify).
	TLSConfig *tls.Config
	// AllowPrivate permits RFC 1918 and CGNAT destinations while still
	// blocking loopback, link-local (including cloud metadata 169.254.169.254),
	// unspecified, and multicast. Use for in-cluster Prometheus, Argo CD, and
	// Vault only — never for operator-supplied public webhook/catalog URLs.
	// Prefer DisableGuardForTest only in unit tests that dial httptest.
	AllowPrivate bool
}

// SafeClient returns an HTTP client for fetching operator/DB-supplied URLs
// server-side. It closes the DNS-rebinding TOCTOU window that GuardPublicHost
// alone leaves open: GuardPublicHost validates the host at check time, but the
// transport re-resolves at dial time, so an attacker-controlled name can pass
// the pre-check and then rebind to a loopback / RFC-1918 / link-local
// (169.254.169.254 metadata) address. This client validates the ACTUAL
// connected IP in the dialer's Control hook — after resolution, before the
// socket is used — so the address the guard would have rejected is rejected at
// the point of connection regardless of what DNS returned.
//
// Use it for every server-side fetch of a URL that is operator- or DB-supplied
// (catalog sync, webhook/alert dispatch, cloud-credential probes, backup/SIEM
// endpoints). GuardPublicHost remains a useful cheap pre-filter for a fast,
// URL-only rejection, but this client is the authoritative enforcement.
func SafeClient(timeout time.Duration) *http.Client {
	return NewSafeClient(ClientOptions{Timeout: timeout})
}

// SafeTransport returns an *http.Transport with dial-time public-IP enforcement
// (SEC-03). Optional tlsConfig is applied when non-nil (SIEM CA / skip-verify).
func SafeTransport(tlsConfig *tls.Config) *http.Transport {
	return safeTransport(tlsConfig, false)
}

// SafeTransportAllowPrivate is SafeTransport with AllowPrivate dial policy
// (RFC 1918/CGNAT allowed; loopback/link-local/metadata still blocked).
func SafeTransportAllowPrivate(tlsConfig *tls.Config) *http.Transport {
	return safeTransport(tlsConfig, true)
}

func safeTransport(tlsConfig *tls.Config, allowPrivate bool) *http.Transport {
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
		Control:   guardDialControl(allowPrivate),
	}
	return &http.Transport{
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		TLSClientConfig:       tlsConfig,
	}
}

// SafeClientWithTLS is SafeClient with a custom TLS config (SIEM CA bundles,
// skip-verify). Dial-time rebinding defense is still applied.
func SafeClientWithTLS(timeout time.Duration, tlsConfig *tls.Config) *http.Client {
	return NewSafeClient(ClientOptions{Timeout: timeout, TLSConfig: tlsConfig})
}

// SafeClientAllowPrivate is SafeClient with AllowPrivate dial policy for
// in-cluster backends (Prometheus, Argo CD, Vault). Loopback, link-local, and
// cloud metadata remain blocked — see ClientOptions.AllowPrivate.
func SafeClientAllowPrivate(timeout time.Duration) *http.Client {
	return NewSafeClient(ClientOptions{Timeout: timeout, AllowPrivate: true})
}

// SafeClientAllowPrivateWithTLS is SafeClientAllowPrivate with a custom TLS config.
func SafeClientAllowPrivateWithTLS(timeout time.Duration, tlsConfig *tls.Config) *http.Client {
	return NewSafeClient(ClientOptions{Timeout: timeout, TLSConfig: tlsConfig, AllowPrivate: true})
}

// NewSafeClient builds a dial-guarded client from options.
func NewSafeClient(opts ClientOptions) *http.Client {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultExternalTimeout
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: safeTransport(opts.TLSConfig, opts.AllowPrivate),
	}
}

// guardDialControl returns a dial Control hook that enforces isDialAllowed
// after DNS resolution with the concrete address the socket is about to
// connect to. Rejecting a disallowed address here defeats DNS rebinding —
// the dialer never re-resolves, so the checked IP is the used IP. Disabled
// under the same test switch as GuardPublicHost so httptest servers on
// loopback stay reachable in unit tests.
func guardDialControl(allowPrivate bool) func(network, address string, c syscall.RawConn) error {
	return func(_ string, address string, _ syscall.RawConn) error {
		if guardDisabled.Load() {
			return nil
		}
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			host = address
		}
		ip := net.ParseIP(host)
		if ip == nil {
			return fmt.Errorf("dial to unresolved address is not permitted")
		}
		if !isDialAllowed(ip, allowPrivate) {
			return fmt.Errorf("dial to a disallowed address is blocked")
		}
		return nil
	}
}
