package middleware

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// TrustedRealIP accepts X-Forwarded-For only from explicitly configured proxy
// networks and walks the chain from right to left. Spoofable vendor headers are
// always discarded. With an empty/invalid configuration it fails closed and
// leaves the socket peer in RemoteAddr.
func TrustedRealIP(rawCIDRs string) func(http.Handler) http.Handler {
	trusted := parseTrustedProxyCIDRs(rawCIDRs)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			request.Header.Del("True-Client-IP")
			request.Header.Del("CF-Connecting-IP")
			request.Header.Del("X-Cluster-Client-IP")
			peer, ok := remoteAddress(request.RemoteAddr)
			if ok && trustedAddress(peer, trusted) {
				client := peer
				parts := strings.Split(request.Header.Get("X-Forwarded-For"), ",")
				for index := len(parts) - 1; index >= 0 && trustedAddress(client, trusted); index-- {
					candidate, err := netip.ParseAddr(strings.TrimSpace(parts[index]))
					if err != nil {
						break
					}
					client = candidate.Unmap()
				}
				if client == peer {
					if candidate, err := netip.ParseAddr(strings.TrimSpace(request.Header.Get("X-Real-IP"))); err == nil {
						client = candidate.Unmap()
					}
				}
				request.RemoteAddr = client.String()
			}
			next.ServeHTTP(response, request)
		})
	}
}

func parseTrustedProxyCIDRs(raw string) []netip.Prefix {
	values := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' })
	prefixes := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(value))
		if err == nil {
			prefixes = append(prefixes, prefix.Masked())
		}
	}
	return prefixes
}

func remoteAddress(value string) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(value)
	if err != nil {
		host = value
	}
	address, err := netip.ParseAddr(strings.TrimSpace(host))
	return address.Unmap(), err == nil
}

func trustedAddress(address netip.Addr, prefixes []netip.Prefix) bool {
	for _, prefix := range prefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}
