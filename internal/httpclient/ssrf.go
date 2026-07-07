package httpclient

import (
	"fmt"
	"net"
	"net/url"
	"sync/atomic"
)

// guardDisabled, when true, makes GuardPublicHost a no-op. It is flipped only
// by tests via DisableGuardForTest so that httptest servers (which always
// listen on loopback) stay reachable in unit tests. Production code never sets
// it, so the guard is always active at runtime.
var guardDisabled atomic.Bool

// DisableGuardForTest disables GuardPublicHost and returns a restore function
// that re-enables it. Intended as `defer httpclient.DisableGuardForTest()()`.
// Only tests that must dial an httptest server on loopback should call this.
func DisableGuardForTest() (restore func()) {
	guardDisabled.Store(true)
	return func() { guardDisabled.Store(false) }
}

// GuardPublicHost resolves the host in rawURL and returns a non-nil error when
// any resolved address is loopback, unspecified, link-local (which includes the
// 169.254.169.254 cloud-metadata endpoint), private/RFC-1918, or multicast.
//
// It is a deliberately small backstop for server-side "test connection" probes
// that fetch an operator-supplied URL and echo the upstream status/error back
// to the caller — behaviour that otherwise turns the probe into an SSRF /
// internal port-scan oracle. It is NOT a general SSRF framework: callers that
// pass the check should still use a bounded HTTP client, and it does not defend
// against DNS-rebinding between this check and the subsequent dial.
func GuardPublicHost(rawURL string) error {
	if guardDisabled.Load() {
		return nil
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("url has no host")
	}
	// A literal IP needs no resolution; still run it through the filter.
	if ip := net.ParseIP(host); ip != nil {
		if !isPublicIP(ip) {
			return fmt.Errorf("host resolves to a disallowed address")
		}
		return nil
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("resolve host: %w", err)
	}
	for _, ip := range ips {
		if !isPublicIP(ip) {
			return fmt.Errorf("host resolves to a disallowed address")
		}
	}
	return nil
}

// isPublicIP reports whether ip is a routable public address, rejecting the
// address classes an SSRF probe must never reach.
func isPublicIP(ip net.IP) bool {
	if ip.IsLoopback() ||
		ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsPrivate() {
		return false
	}
	return true
}
