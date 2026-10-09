package httpclient

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// TrustedOrigin is operator authority for one origin and its entire DNS answer
// set. It grants network access, never artifact authenticity. HTTP is permitted
// only by explicit origin policy, for private S3 endpoints without TLS.
type TrustedOrigin struct {
	URL   string         `json:"url"`
	CIDRs []netip.Prefix `json:"cidrs"`
}

// DestinationPolicy keeps disconnected destinations finite. The legacy private
// host fields support delivery source policies; scoped distribution/S3 config
// uses exact Origins instead. No policy grants loopback or metadata access.
type DestinationPolicy struct {
	Restricted   bool            `json:"restricted"`
	Origins      []TrustedOrigin `json:"origins"`
	PrivateHosts []string        `json:"-"`
	PrivateCIDRs []netip.Prefix  `json:"-"`
}

func ParseDestinationPolicy(raw string) (DestinationPolicy, error) {
	var policy DestinationPolicy
	if raw == "" {
		return policy, nil
	}
	if len(raw) > 64<<10 {
		return policy, errors.New("destination policy exceeds size limit")
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&policy); err != nil {
		return policy, errors.New("destination policy is invalid")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return policy, errors.New("destination policy has trailing input")
	}
	if err := policy.Validate(); err != nil {
		return policy, err
	}
	return policy, nil
}

func (p DestinationPolicy) Validate() error {
	if len(p.Origins) > 64 {
		return errors.New("destination origin limit exceeded")
	}
	seen := map[string]bool{}
	for _, origin := range p.Origins {
		parsed, err := parseOrigin(origin.URL)
		if err != nil || parsed.Path != "" && parsed.Path != "/" || parsed.RawQuery != "" || parsed.Fragment != "" || len(origin.CIDRs) == 0 || len(origin.CIDRs) > 32 {
			return errors.New("trusted destination origin is invalid")
		}
		identity := originIdentity(parsed)
		if seen[identity] {
			return errors.New("trusted destination origin is duplicated")
		}
		seen[identity] = true
		for _, prefix := range origin.CIDRs {
			if !prefix.IsValid() || prefix != prefix.Masked() || prefix.Addr().Is4In6() {
				return errors.New("trusted destination CIDR is invalid")
			}
			// Plaintext origins are confined to explicitly private networks.
			if parsed.Scheme == "http" && !prefix.Addr().IsPrivate() {
				return errors.New("plaintext destination must use private CIDRs")
			}
		}
	}
	if p.Restricted && len(p.Origins) == 0 {
		return errors.New("restricted destination policy has no origins")
	}
	return nil
}

func parseOrigin(raw string) (*url.URL, error) {
	value, err := url.Parse(raw)
	if err != nil || value.Hostname() == "" || value.User != nil || value.Opaque != "" || (value.Scheme != "https" && value.Scheme != "http") || strings.ContainsAny(raw, "\x00\\") {
		return nil, errors.New("destination URL is invalid")
	}
	// Reject ambiguous DNS spellings rather than giving aliases new authority.
	if strings.HasSuffix(value.Hostname(), ".") || strings.Contains(value.Hostname(), "%") {
		return nil, errors.New("destination hostname is invalid")
	}
	port := value.Port()
	if port != "" {
		if number, err := strconv.Atoi(port); err != nil || number < 1 || number > 65535 || strconv.Itoa(number) != port {
			return nil, errors.New("destination port is invalid")
		}
	}
	return value, nil
}

func originIdentity(value *url.URL) string {
	port := value.Port()
	if port == "" {
		if value.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return value.Scheme + "://" + net.JoinHostPort(strings.ToLower(value.Hostname()), port)
}

func (p DestinationPolicy) origin(value *url.URL) *TrustedOrigin {
	for index := range p.Origins {
		candidate, _ := parseOrigin(p.Origins[index].URL)
		if candidate != nil && originIdentity(candidate) == originIdentity(value) {
			return &p.Origins[index]
		}
	}
	return nil
}

func (p DestinationPolicy) permitsURL(value *url.URL) bool {
	return p.origin(value) != nil || !p.Restricted && value.Scheme == "https"
}

// AllowsAddress is also used by the delivery resolver's existing policy API.
func (p DestinationPolicy) AllowsAddress(host string, address netip.Addr) bool {
	address = address.Unmap()
	if !safeDestinationAddress(address) {
		return false
	}
	if !address.IsPrivate() && !isCGNAT(net.IP(address.AsSlice())) {
		return true
	}
	for _, pattern := range p.PrivateHosts {
		pattern = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(pattern), "."))
		host = strings.ToLower(strings.TrimSuffix(host, "."))
		if pattern == host || strings.HasPrefix(pattern, "*.") && strings.HasSuffix(host, pattern[1:]) && host != pattern[2:] {
			for _, prefix := range p.PrivateCIDRs {
				if prefix.Contains(address) {
					return true
				}
			}
		}
	}
	return false
}

func safeDestinationAddress(address netip.Addr) bool {
	return address.IsValid() && !address.IsUnspecified() && !address.IsLoopback() && !address.IsLinkLocalUnicast() && !address.IsMulticast()
}

func (p DestinationPolicy) allows(value *url.URL, address netip.Addr) bool {
	address = address.Unmap()
	if !safeDestinationAddress(address) {
		return false
	}
	if origin := p.origin(value); origin != nil {
		for _, prefix := range origin.CIDRs {
			if prefix.Contains(address) {
				return true
			}
		}
		return false
	}
	return !p.Restricted && value.Scheme == "https" && p.AllowsAddress(value.Hostname(), address)
}

func (p DestinationPolicy) ValidateURL(ctx context.Context, raw string) error {
	return p.validateURL(ctx, raw, net.DefaultResolver.LookupNetIP)
}

func (p DestinationPolicy) validateURL(ctx context.Context, raw string, lookup destinationLookup) error {
	value, err := parseOrigin(raw)
	if err != nil || !p.permitsURL(value) {
		return errors.New("destination URL is not permitted")
	}
	_, err = p.addresses(ctx, value, lookup)
	return err
}

type destinationLookup func(context.Context, string, string) ([]netip.Addr, error)

func (p DestinationPolicy) addresses(ctx context.Context, value *url.URL, lookup destinationLookup) ([]netip.Addr, error) {
	addresses, err := lookup(ctx, "ip", value.Hostname())
	if err != nil || len(addresses) == 0 {
		return nil, errors.New("destination hostname could not be resolved")
	}
	for _, address := range addresses {
		if !p.allows(value, address) {
			return nil, errors.New("destination resolves outside approved network")
		}
	}
	return addresses, nil
}

type DestinationOptions struct {
	Timeout          time.Duration
	TLSConfig        *tls.Config
	MaxResponseBytes int64
	MaxRedirects     int
	ProxyURL         string
}

// NewDestinationClient validates on every request and redirect, resolves every
// connection independently, and dials the checked IP directly. Ambient proxy
// environment is ignored. The caller's policy is copied and cannot be widened
// by catalog content or by subsequent changes to caller-owned slices.
func NewDestinationClient(policy DestinationPolicy, options DestinationOptions) (*http.Client, error) {
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	if options.MaxResponseBytes > 512<<20 || options.Timeout > 5*time.Minute || options.MaxRedirects > 10 {
		return nil, errors.New("destination client bounds are invalid")
	}
	if options.TLSConfig != nil {
		options.TLSConfig = options.TLSConfig.Clone()
	}
	policy = cloneDestinationPolicy(policy)
	return newDestinationClient(policy, options, net.DefaultResolver.LookupNetIP, (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext)
}

func cloneDestinationPolicy(policy DestinationPolicy) DestinationPolicy {
	policy.PrivateHosts = append([]string(nil), policy.PrivateHosts...)
	policy.PrivateCIDRs = append([]netip.Prefix(nil), policy.PrivateCIDRs...)
	policy.Origins = append([]TrustedOrigin(nil), policy.Origins...)
	for index := range policy.Origins {
		policy.Origins[index].CIDRs = append([]netip.Prefix(nil), policy.Origins[index].CIDRs...)
	}
	return policy
}

func newDestinationClient(policy DestinationPolicy, options DestinationOptions, lookup destinationLookup, dial func(context.Context, string, string) (net.Conn, error)) (*http.Client, error) {
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = DefaultExternalTimeout
	}
	redirects := options.MaxRedirects
	if redirects <= 0 {
		redirects = 3
	}
	transport := SafeTransport(options.TLSConfig)
	transport.MaxResponseHeaderBytes = 1 << 20
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, errors.New("destination dial address is invalid")
		}
		scheme, _ := ctx.Value(destinationSchemeKey{}).(string)
		if scheme == "" {
			scheme = "https"
		}
		if options.ProxyURL != "" {
			proxy, _ := parseOrigin(options.ProxyURL)
			if proxy != nil && originIdentity(proxy) == "https://"+net.JoinHostPort(strings.ToLower(host), port) {
				scheme = "https"
			}
		}

		value := &url.URL{Scheme: scheme, Host: net.JoinHostPort(host, port)}
		if !policy.permitsURL(value) {
			return nil, errors.New("destination connection is not permitted")
		}
		addresses, err := policy.addresses(ctx, value, lookup)
		if err != nil {
			return nil, err
		}
		for _, ip := range addresses {
			conn, err := dial(ctx, network, net.JoinHostPort(ip.Unmap().String(), port))
			if err == nil {
				return conn, nil
			}
		}
		return nil, errors.New("destination connection failed")
	}
	if options.ProxyURL != "" {
		proxy, err := parseOrigin(options.ProxyURL)
		if err != nil || proxy.Scheme != "https" {
			return nil, errors.New("destination proxy is invalid")
		}
		if err := policy.validateURL(context.Background(), options.ProxyURL, lookup); err != nil {
			return nil, err
		}
		transport.Proxy = http.ProxyURL(proxy)
	}
	maximum := options.MaxResponseBytes
	if maximum <= 0 {
		maximum = 64 << 20
	}
	base := &destinationTransport{policy: policy, lookup: lookup, base: transport}
	client := &http.Client{Timeout: timeout, Transport: &responseLimitTransport{base: base, maximum: maximum}}
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) >= redirects {
			return errors.New("destination redirect limit exceeded")
		}
		if len(via) > 0 && originIdentity(via[0].URL) != originIdentity(request.URL) {
			request.Header.Del("Authorization")
			request.Header.Del("Proxy-Authorization")
			return errors.New("cross-origin destination redirect is not permitted")
		}
		return policy.validateURL(request.Context(), request.URL.String(), lookup)
	}
	return client, nil
}

type destinationTransport struct {
	policy DestinationPolicy
	lookup destinationLookup
	base   http.RoundTripper
}

func (t *destinationTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if err := t.policy.validateURL(request.Context(), request.URL.String(), t.lookup); err != nil {
		return nil, err
	}
	return t.base.RoundTrip(request.WithContext(context.WithValue(request.Context(), destinationSchemeKey{}, request.URL.Scheme)))
}

type destinationSchemeKey struct{}

// Clone freezes operator authority when composing a long-lived consumer.
func (p DestinationPolicy) Clone() DestinationPolicy { return cloneDestinationPolicy(p) }
