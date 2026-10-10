package httpclient

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"

	"golang.org/x/net/http/httpproxy"
)

// ProxyRouting is explicit operator configuration. It is never populated by
// process-global environment discovery or by a chart/catalog request. NoProxy
// selects a route; it cannot grant an origin or bypass destination checks.
// URL may carry operator credentials, so it must never be logged or exposed.
type ProxyRouting struct {
	// URL selects one operator endpoint for both schemes. HTTPURL/HTTPSURL
	// instead preserve distinct standard operator routes; they cannot mix with URL.
	URL      string
	HTTPURL  string
	HTTPSURL string
	NoProxy  string
}

// CaptureOperatorProxyRouting captures values supplied by a process config
// loader. It never reads environment variables. Standard upper-case nonempty
// values precede lower-case values as in httpproxy.FromEnvironment. An explicit
// Helm URL overrides both routes; an explicitly empty bypass disables inherited
// NO_PROXY. Capturing routes grants no destination or proxy-origin authority.
func CaptureOperatorProxyRouting(explicitURL string, explicitNoProxy *string, upper, lower ProxyRouting) ProxyRouting {
	first := func(primary, secondary string) string {
		if primary != "" {
			return primary
		}
		return secondary
	}
	value := ProxyRouting{HTTPURL: first(upper.HTTPURL, lower.HTTPURL), HTTPSURL: first(upper.HTTPSURL, lower.HTTPSURL), NoProxy: first(upper.NoProxy, lower.NoProxy)}
	if explicitURL != "" {
		value.URL = explicitURL
		value.HTTPURL = ""
		value.HTTPSURL = ""
	}
	if explicitNoProxy != nil {
		value.NoProxy = *explicitNoProxy
	}
	// An inherited NO_PROXY by itself is inert, matching the standard matcher.
	if value.URL == "" && value.HTTPURL == "" && value.HTTPSURL == "" && explicitNoProxy == nil {
		value.NoProxy = ""
	}
	return value
}

type frozenProxyRouting struct {
	authorities map[string]*url.URL
	proxy       func(*url.URL) (*url.URL, error)
}

// ValidateProxyRouting performs shape/policy validation without a DNS lookup.
// Existing generic ProxyURL consumers retain their HTTPS/no-userinfo contract.
func ValidateProxyRouting(value ProxyRouting, policy DestinationPolicy) error {
	_, err := freezeProxyRouting(value, policy)
	return err
}

func freezeProxyRouting(value ProxyRouting, policy DestinationPolicy) (*frozenProxyRouting, error) {
	if len(value.NoProxy) > 8<<10 || strings.ContainsAny(value.NoProxy, "\x00\r\n") || len(strings.Split(value.NoProxy, ",")) > 128 {
		return nil, errors.New("destination proxy routing exceeds bounds")
	}
	if value.URL != "" && (value.HTTPURL != "" || value.HTTPSURL != "") {
		return nil, errors.New("destination proxy routing is ambiguous")
	}
	httpURL, httpsURL := value.HTTPURL, value.HTTPSURL
	if value.URL != "" {
		httpURL = value.URL
		httpsURL = value.URL
	}
	if httpURL == "" && httpsURL == "" {
		if value.NoProxy != "" {
			return nil, errors.New("destination proxy bypass requires an explicit proxy")
		}
		return nil, nil
	}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	authorities := make(map[string]*url.URL, 2)
	endpoint := func(raw string) (string, error) {
		if raw == "" {
			return "", nil
		}
		if len(raw) > 16<<10 || strings.ContainsAny(raw, "\x00\r\n\\") {
			return "", errors.New("destination proxy routing exceeds bounds")
		}
		parsed, err := url.Parse(raw)
		if err != nil {
			return "", errors.New("destination proxy routing is invalid")
		}
		credentials := parsed.User
		parsed.User = nil
		authority, err := parseOrigin(parsed.String())
		if err != nil || authority.Path != "" && authority.Path != "/" || authority.RawQuery != "" || authority.ForceQuery || authority.Fragment != "" {
			return "", errors.New("destination proxy routing is invalid")
		}
		if authority.Scheme == "http" {
			// Plaintext proxy credentials/CONNECT require exact operator confinement.
			origin := policy.origin(authority)
			if origin == nil {
				return "", errors.New("plaintext proxy requires an explicit private origin")
			}
			for _, cidr := range origin.CIDRs {
				if !entirelyPrivatePrefix(cidr) {
					return "", errors.New("plaintext proxy requires private CIDRs")
				}
			}
		}
		if !policy.permitsURL(authority) {
			return "", errors.New("destination proxy origin is not permitted")
		}
		// Literal IPs need no DNS: reject loopback/metadata/out-of-policy literals
		// during freeze, even if a bypass would otherwise leave this route unused.
		if address, err := netip.ParseAddr(authority.Hostname()); err == nil && !policy.allows(authority, address) {
			return "", errors.New("destination proxy address is not permitted")
		}
		authorities[originIdentity(authority)] = authority
		selected := *authority
		selected.User = credentials
		return selected.String(), nil
	}
	httpEndpoint, err := endpoint(httpURL)
	if err != nil {
		return nil, err
	}
	httpsEndpoint, err := endpoint(httpsURL)
	if err != nil {
		return nil, err
	}
	configuration := httpproxy.Config{HTTPProxy: httpEndpoint, HTTPSProxy: httpsEndpoint, NoProxy: value.NoProxy}
	return &frozenProxyRouting{authorities: authorities, proxy: configuration.ProxyFunc()}, nil
}

func (r *frozenProxyRouting) selectProxy(request *http.Request) (*url.URL, error) {
	if r == nil {
		return nil, nil
	}
	proxy, err := r.proxy(request.URL)
	if err != nil {
		return nil, errors.New("destination proxy routing failed")
	}
	if proxy == nil {
		return nil, nil
	}
	if r.authorities[originIdentity(proxy)] == nil {
		return nil, errors.New("destination proxy selection changes authority")
	}
	return proxy, nil
}

type selectedProxyOriginKey struct{}

// dialTLSWithProxyIdentity keeps target client certificates out of the first
// HTTPS proxy handshake. It uses the same canonical guarded TCP dial; target
// TLS inside CONNECT remains standard net/http behavior. Direct/bypass target
// TLS retains the repository identity. No second downloader is introduced.
func dialTLSWithProxyIdentity(transport *http.Transport) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		connection, err := transport.DialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			_ = connection.Close()
			return nil, errors.New("destination TLS address is invalid")
		}
		configuration := &tls.Config{MinVersion: tls.VersionTLS12}
		if transport.TLSClientConfig != nil {
			configuration = transport.TLSClientConfig.Clone()
		}
		if proxy, ok := ctx.Value(selectedProxyOriginKey{}).(*url.URL); ok && proxy != nil {
			configuration.Certificates = nil
			configuration.GetClientCertificate = nil
			configuration.ServerName = proxy.Hostname()
		} else if configuration.ServerName == "" {
			configuration.ServerName = host
		}
		secured := tls.Client(connection, configuration)
		if err := secured.HandshakeContext(ctx); err != nil {
			_ = secured.Close()
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, errors.New("destination TLS handshake failed")
		}
		if ctx.Err() != nil {
			_ = secured.Close()
			return nil, ctx.Err()
		}
		return secured, nil
	}
}
