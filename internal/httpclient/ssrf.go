package httpclient

import (
	"fmt"
	"net"
	"net/url"
)

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
