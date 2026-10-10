package httpclient

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"strings"
	"testing"
)

func TestTypedProxyRoutingPreservesExplicitCredentialsAndBypassWithoutAmbientAuthority(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://ambient.invalid:3128")
	t.Setenv("NO_PROXY", "*")
	policy := DestinationPolicy{Origins: []TrustedOrigin{{URL: "http://proxy.example:3128", CIDRs: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}}}}
	value := ProxyRouting{URL: "http://operator:fixture-password@proxy.example:3128", NoProxy: "bypass.example,.direct.example,192.168.1.0/24"}
	frozen, err := freezeProxyRouting(value, policy)
	if err != nil {
		t.Fatal("explicit operator proxy routing rejected")
	}
	for _, authority := range frozen.authorities {
		if authority.User != nil {
			t.Fatal("credential material retained in authority URL")
		}
	}
	cases := []struct {
		target string
		proxy  bool
	}{
		{"https://charts.example/index.yaml", true},
		{"https://bypass.example/index.yaml", false},
		{"https://nested.direct.example/index.yaml", false},
		{"https://192.168.1.10/index.yaml", false},
	}
	for _, tc := range cases {
		request, _ := http.NewRequest(http.MethodGet, tc.target, nil)
		chosen, err := frozen.selectProxy(request)
		if err != nil || (chosen != nil) != tc.proxy {
			t.Fatal("explicit bypass selection changed")
		}
		if chosen != nil {
			username := chosen.User.Username()
			password, _ := chosen.User.Password()
			if username != "operator" || password != "fixture-password" {
				t.Fatal("explicit proxy credentials changed")
			}
		}
	}
	// The caller's subsequent mutation cannot widen the frozen routing.
	value.URL = "http://replacement.invalid:3128"
	value.NoProxy = "*"
	request, _ := http.NewRequest(http.MethodGet, "https://charts.example/index.yaml", nil)
	chosen, _ := frozen.selectProxy(request)
	if chosen == nil || chosen.Hostname() != "proxy.example" {
		t.Fatal("caller mutation widened proxy routing")
	}
}

func TestTypedProxyRoutingRefusesPublicPlaintextAndMalformedCredentialAuthority(t *testing.T) {
	for _, value := range []ProxyRouting{
		{URL: "http://proxy.example:3128"},
		{URL: "https://operator:fixture-password@proxy.example/path"},
		{URL: "https://proxy.example?mode=all"},
		{URL: "https://proxy.example?"},
		{URL: "https://proxy.example#fragment"},
		{URL: "https://proxy.example."},
		{URL: "https://proxy.example:0"},
		{URL: "https://proxy.example", NoProxy: strings.Repeat("a", 8193)},
		{URL: "https://proxy.example", NoProxy: "valid.example\n*"},
		{NoProxy: "*"},
	} {
		if _, err := freezeProxyRouting(value, DestinationPolicy{}); err == nil {
			t.Fatal("unsupported proxy shape admitted")
		}
	}
	// Existing generic configuration still rejects credentials/plaintext.
	for _, raw := range []string{"http://proxy.example:3128", "https://operator:fixture-password@proxy.example"} {
		if ValidateProxyOrigin(raw) == nil {
			t.Fatal("typed compatibility broadened legacy generic proxy defaults")
		}
	}
}

func TestTypedProxyBypassNeverGrantsRepositoryOrigin(t *testing.T) {
	policy := DestinationPolicy{Restricted: true, Origins: []TrustedOrigin{{URL: "http://proxy.example:3128", CIDRs: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}}}}
	frozen, err := freezeProxyRouting(ProxyRouting{URL: "http://proxy.example:3128", NoProxy: "*"}, policy)
	if err != nil {
		t.Fatal(err)
	}
	reached := false
	transport := &destinationTransport{policy: policy, lookup: func(context.Context, string, string) ([]netip.Addr, error) {
		t.Fatal("unapproved target started DNS")
		return nil, nil
	}, base: roundTripFunc(func(*http.Request) (*http.Response, error) {
		reached = true
		return nil, errors.New("unexpected transfer")
	}), proxyRouting: frozen}
	request, _ := http.NewRequest(http.MethodGet, "https://unapproved.example/index.yaml", nil)
	if _, err := transport.RoundTrip(request); err == nil || reached {
		t.Fatal("bypass granted repository authority")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestTypedProxyConstructorJoinsDNSAndRefusesAmbiguousRouting(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if client, err := newDestinationClientContext(ctx, DestinationPolicy{}, DestinationOptions{ProxyRouting: &ProxyRouting{URL: "https://proxy.example"}}, nil, nil); client != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("expired typed proxy owner published a client")
	}
	if _, err := newDestinationClientContext(context.Background(), DestinationPolicy{}, DestinationOptions{ProxyURL: "https://legacy.example", ProxyRouting: &ProxyRouting{URL: "https://explicit.example"}}, nil, nil); err == nil {
		t.Fatal("two proxy authority inputs admitted")
	}
}

func TestTypedProxyRoutesKeepDifferentOperatorEndpointsAndAvoidUnusedProxyDNS(t *testing.T) {
	routing, err := freezeProxyRouting(ProxyRouting{HTTPURL: "https://http-proxy.example", HTTPSURL: "https://https-proxy.example", NoProxy: "direct.example"}, DestinationPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ target, proxy string }{{"http://charts.example/chart.tgz", "http-proxy.example"}, {"https://charts.example/chart.tgz", "https-proxy.example"}, {"https://direct.example/chart.tgz", ""}} {
		request, _ := http.NewRequest(http.MethodGet, tc.target, nil)
		selected, err := routing.selectProxy(request)
		if err != nil || selected == nil && tc.proxy != "" || selected != nil && selected.Hostname() != tc.proxy {
			t.Fatal("distinct operator proxy routes changed")
		}
	}
	if _, err := freezeProxyRouting(ProxyRouting{URL: "https://both.example", HTTPURL: "https://other.example"}, DestinationPolicy{}); err == nil {
		t.Fatal("ambiguous routing configuration admitted")
	}
	lookedUp := false
	client, err := newDestinationClientContext(context.Background(), DestinationPolicy{}, DestinationOptions{ProxyRouting: &ProxyRouting{URL: "https://unused.example", NoProxy: "*"}}, func(context.Context, string, string) ([]netip.Addr, error) {
		lookedUp = true
		return nil, errors.New("unused DNS called")
	}, nil)
	if err != nil || client == nil || lookedUp {
		t.Fatal("unused bypass proxy initiated DNS during construction")
	}
}

func TestOperatorProxyCaptureHasExplicitUpperLowerAndEmptyBypassPrecedence(t *testing.T) {
	upper := ProxyRouting{HTTPURL: "http://upper.example:3128", HTTPSURL: "https://secure-upper.example", NoProxy: "upper.example"}
	lower := ProxyRouting{HTTPURL: "http://lower.example:3128", HTTPSURL: "https://secure-lower.example", NoProxy: "lower.example"}
	value := CaptureOperatorProxyRouting("", nil, upper, lower)
	if value.HTTPURL != upper.HTTPURL || value.HTTPSURL != upper.HTTPSURL || value.NoProxy != upper.NoProxy || value.URL != "" {
		t.Fatal("upper-case operator capture precedence changed")
	}
	value = CaptureOperatorProxyRouting("", nil, ProxyRouting{}, lower)
	if value.HTTPURL != lower.HTTPURL || value.HTTPSURL != lower.HTTPSURL || value.NoProxy != lower.NoProxy {
		t.Fatal("lower-case proxy fallback changed")
	}
	empty := ""
	value = CaptureOperatorProxyRouting("https://explicit.example", &empty, upper, lower)
	if value.URL != "https://explicit.example" || value.HTTPURL != "" || value.HTTPSURL != "" || value.NoProxy != "" {
		t.Fatal("explicit Helm override or empty bypass lost precedence")
	}
	value = CaptureOperatorProxyRouting("", nil, ProxyRouting{NoProxy: "*"}, ProxyRouting{})
	if value.NoProxy != "" {
		t.Fatal("inert inherited bypass created a proxy requirement")
	}
}
