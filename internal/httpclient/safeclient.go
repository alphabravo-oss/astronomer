package httpclient

import (
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"
)

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
	if timeout <= 0 {
		timeout = DefaultExternalTimeout
	}
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
		Control:   guardDialControl,
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext:           dialer.DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          10,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}
}

// guardDialControl runs after DNS resolution with the concrete address the
// socket is about to connect to. Rejecting a disallowed address here defeats
// DNS rebinding — the dialer never re-resolves, so the checked IP is the used
// IP. Disabled under the same test switch as GuardPublicHost so httptest
// servers on loopback stay reachable in unit tests.
func guardDialControl(_ string, address string, _ syscall.RawConn) error {
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
	if !isPublicIP(ip) {
		return fmt.Errorf("dial to a disallowed (non-public) address is blocked")
	}
	return nil
}
