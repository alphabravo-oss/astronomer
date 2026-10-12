package resolver

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strings"
	"time"
)

// NetworkPolicy permits named enterprise destinations without weakening the
// default public-internet policy. Private addresses require both a matching
// hostname and a matching CIDR. Loopback, link-local/metadata, unspecified,
// multicast, Unix sockets, and URL credentials are never permitted.
type NetworkPolicy struct {
	AllowedPrivateHosts []string
	AllowedPrivateCIDRs []netip.Prefix
}

func (p NetworkPolicy) ValidateURL(ctx context.Context, raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil {
		return denied("URL must be absolute and credential-free")
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return denied("URL scheme is not permitted for an HTTP fetch")
	}
	if parsed.Scheme == "http" {
		return denied("plaintext HTTP is not permitted")
	}
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", parsed.Hostname())
	if err != nil || len(addresses) == 0 {
		return &Error{Code: CodeUpstreamTemporary, Message: "source hostname could not be resolved", Retryable: true}
	}
	for _, address := range addresses {
		if !p.allows(parsed.Hostname(), address) {
			return denied("source hostname resolves to a disallowed network")
		}
	}
	return nil
}

func resolveAllowedAddresses(ctx context.Context, policy NetworkPolicy, host string) ([]netip.Addr, error) {
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil || len(addresses) == 0 {
		return nil, &Error{Code: CodeUpstreamTemporary, Message: "source hostname could not be resolved", Retryable: true}
	}
	result := make([]netip.Addr, 0, len(addresses))
	for _, address := range addresses {
		if !policy.allows(host, address) {
			return nil, denied("source hostname resolves to a disallowed network")
		}
		result = append(result, address.Unmap())
	}
	sort.Slice(result, func(left, right int) bool { return result[left].Less(result[right]) })
	return result, nil
}

func (p NetworkPolicy) allows(host string, address netip.Addr) bool {
	return (httpclient.DestinationPolicy{PrivateHosts: p.AllowedPrivateHosts, PrivateCIDRs: p.AllowedPrivateCIDRs}).AllowsAddress(host, address)
}

type safeClientFactory struct{}

func (safeClientFactory) Client(policy NetworkPolicy, tlsConfig *tls.Config, proxyRaw string, limits Limits) (*http.Client, error) {
	limits = limits.withDefaults()
	client, err := httpclient.NewDestinationClient(httpclient.DestinationPolicy{PrivateHosts: policy.AllowedPrivateHosts, PrivateCIDRs: policy.AllowedPrivateCIDRs}, httpclient.DestinationOptions{AllowPublicCrossOriginRedirects: true, SensitiveHeaders: []string{"Private-Token", "X-Gitlab-Token", "X-Auth-Token", "X-Api-Key"}, TLSConfig: tlsConfig, ProxyURL: proxyRaw, Timeout: limits.Timeout, MaxRedirects: limits.MaxRedirects, MaxResponseBytes: limits.MaxArtifactBytes})
	if err != nil {
		return nil, denied("source client policy is invalid")
	}
	return client, nil
}

func (p NetworkPolicy) dialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, denied("source dial address is invalid")
	}
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil || len(addresses) == 0 {
		return nil, &Error{Code: CodeUpstreamTemporary, Message: "source hostname could not be resolved", Retryable: true}
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	for _, candidate := range addresses {
		if !p.allows(host, candidate) {
			return nil, denied("source hostname resolves to a disallowed network")
		}
		conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(candidate.String(), port))
		if dialErr == nil {
			return conn, nil
		}
	}
	return nil, &Error{Code: CodeUpstreamTemporary, Message: "source connection failed", Retryable: true}
}

func denied(message string) error {
	return &Error{Code: CodeNetworkDenied, Message: message}
}

func validateFetchURL(ctx context.Context, policy NetworkPolicy, raw string) error {
	if strings.ContainsRune(raw, '\x00') || strings.HasPrefix(raw, "unix:") || strings.HasPrefix(raw, "file:") {
		return denied("source URL is not permitted")
	}
	if err := policy.ValidateURL(ctx, raw); err != nil {
		return err
	}
	return nil
}

func tlsConfigForCA(bundle []byte) (*tls.Config, error) {
	if len(bundle) == 0 {
		return &tls.Config{MinVersion: tls.VersionTLS12}, nil
	}
	pool, err := systemCertPool()
	if err != nil {
		return nil, fmt.Errorf("load system trust store: %w", err)
	}
	if !pool.AppendCertsFromPEM(bundle) {
		return nil, &Error{Code: CodeInvalidRequest, Message: "source CA bundle is invalid"}
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool}, nil
}

var systemCertPool = func() (*x509.CertPool, error) {
	pool, err := x509.SystemCertPool()
	if pool == nil {
		pool = x509.NewCertPool()
	}
	return pool, err
}
