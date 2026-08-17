package resolver

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"net/url"
	"sort"
	"strings"

	"github.com/google/uuid"
)

// StaticPolicyProvider is the chart/config-backed enterprise egress policy.
// Private destinations require a matching hostname and CIDR; a proxy is used
// only when a source explicitly names the reserved "default" proxy reference.
type StaticPolicyProvider struct {
	policy NetworkPolicy
	proxy  string
}

func NewStaticPolicyProvider(hostsJSON, cidrsJSON, proxy string) (*StaticPolicyProvider, error) {
	var hosts, cidrs []string
	if strings.TrimSpace(hostsJSON) != "" {
		if err := json.Unmarshal([]byte(hostsJSON), &hosts); err != nil {
			return nil, errors.New("delivery private source host policy must be a JSON string array")
		}
	}
	if strings.TrimSpace(cidrsJSON) != "" {
		if err := json.Unmarshal([]byte(cidrsJSON), &cidrs); err != nil {
			return nil, errors.New("delivery source CIDR policy must be a JSON string array")
		}
	}
	seenHosts := make(map[string]struct{}, len(hosts))
	canonicalHosts := make([]string, 0, len(hosts))
	for _, host := range hosts {
		host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
		base := strings.TrimPrefix(host, "*.")
		if host == "" || strings.ContainsAny(base, "/:@\\\r\n\x00") || strings.Contains(base, "*") || (!strings.Contains(base, ".") && base != "localhost") {
			return nil, errors.New("delivery private source host policy contains an invalid hostname")
		}
		if _, exists := seenHosts[host]; !exists {
			seenHosts[host] = struct{}{}
			canonicalHosts = append(canonicalHosts, host)
		}
	}
	sort.Strings(canonicalHosts)
	prefixes := make([]netip.Prefix, 0, len(cidrs))
	for _, raw := range cidrs {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(raw))
		if err != nil || !prefix.IsValid() {
			return nil, errors.New("delivery source CIDR policy contains an invalid prefix")
		}
		prefixes = append(prefixes, prefix.Masked())
	}
	sort.Slice(prefixes, func(i, j int) bool { return prefixes[i].String() < prefixes[j].String() })
	proxy = strings.TrimSpace(proxy)
	if proxy != "" {
		parsed, err := url.Parse(proxy)
		if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return nil, errors.New("delivery source proxy must be an absolute credential-free HTTPS URL")
		}
	}
	return &StaticPolicyProvider{policy: NetworkPolicy{AllowedPrivateHosts: canonicalHosts, AllowedPrivateCIDRs: prefixes}, proxy: proxy}, nil
}

func (p *StaticPolicyProvider) NetworkPolicy(context.Context, uuid.UUID, uuid.UUID) (NetworkPolicy, error) {
	if p == nil {
		return NetworkPolicy{}, errors.New("delivery source network policy is unavailable")
	}
	return p.policy, nil
}

func (p *StaticPolicyProvider) ProxyURL(_ context.Context, _ uuid.UUID, reference string) (string, error) {
	if p == nil {
		return "", errors.New("delivery source proxy policy is unavailable")
	}
	switch strings.TrimSpace(reference) {
	case "":
		return "", nil
	case "default":
		if p.proxy == "" {
			return "", &Error{Code: CodeNetworkDenied, Message: "default source proxy is not configured"}
		}
		return p.proxy, nil
	default:
		return "", &Error{Code: CodeNetworkDenied, Message: "source references an unknown proxy policy"}
	}
}

var _ PolicyProvider = (*StaticPolicyProvider)(nil)
