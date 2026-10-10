package main

import (
	"crypto/tls"
	"net/http"
	"net/netip"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

func registryToolOrigin(raw string) httpclient.TrustedOrigin {
	return httpclient.TrustedOrigin{URL: raw, CIDRs: []netip.Prefix{netip.MustParsePrefix("203.0.113.0/24")}}
}

func TestRegistryToolsRequireBothIndependentLiteralOriginApprovals(t *testing.T) {
	for _, restricted := range []bool{false, true} {
		policy := httpclient.DestinationPolicy{Restricted: restricted, Origins: []httpclient.TrustedOrigin{
			registryToolOrigin("https://GHCR.IO:443/"), registryToolOrigin("https://PKG-CONTAINERS.githubusercontent.com/"),
		}}
		trust := &tls.Config{MinVersion: tls.VersionTLS12}
		options, err := registryToolDestinationOptions(policy, trust)
		if err != nil {
			t.Fatal(err)
		}
		if len(options.RedirectOriginPairs) != 1 || options.RedirectOriginPairs[0].From != "https://ghcr.io" || options.RedirectOriginPairs[0].To != "https://pkg-containers.githubusercontent.com" {
			t.Fatalf("operator authority did not select the single literal directed pair: %+v", options.RedirectOriginPairs)
		}
		if options.AllowPublicCrossOriginRedirects || options.ProxyURL != "" || options.TLSConfig != trust || options.MaxResponseBytes != distribution.MaximumArchiveBytes {
			t.Fatal("registry adapter broadened transport authority or changed its existing TLS/response bounds")
		}
		client, err := newRegistryToolDestinationClient(policy, trust)
		if err != nil {
			t.Fatal(err)
		}
		defer client.CloseIdleConnections()
		// The canonical client must still reject reverse, unrelated and excessive
		// transitions before DNS or any connection. No network is needed here.
		source, _ := http.NewRequest(http.MethodGet, "https://ghcr.io/v2/test", nil)
		cdn, _ := http.NewRequest(http.MethodGet, "https://pkg-containers.githubusercontent.com/blob", nil)
		other, _ := http.NewRequest(http.MethodGet, "https://unapproved.invalid/blob", nil)
		if err := client.CheckRedirect(source, []*http.Request{cdn}); err == nil {
			t.Fatal("reverse CDN authority accepted")
		}
		if err := client.CheckRedirect(other, []*http.Request{source}); err == nil {
			t.Fatal("unapproved destination accepted")
		}
		if err := client.CheckRedirect(cdn, []*http.Request{source, cdn, cdn}); err == nil {
			t.Fatal("third redirect hop accepted")
		}
	}
}

func TestRegistryToolsKeepStrictRedirectDefaultWithoutExactPair(t *testing.T) {
	cases := map[string][]httpclient.TrustedOrigin{
		"public default":          nil,
		"empty query spelling":    {registryToolOrigin("https://ghcr.io?"), registryToolOrigin("https://pkg-containers.githubusercontent.com")},
		"registry only":           {registryToolOrigin("https://ghcr.io")},
		"CDN only":                {registryToolOrigin("https://pkg-containers.githubusercontent.com")},
		"different CDN":           {registryToolOrigin("https://ghcr.io"), registryToolOrigin("https://other.githubusercontent.com")},
		"registry different port": {registryToolOrigin("https://ghcr.io:444"), registryToolOrigin("https://pkg-containers.githubusercontent.com")},
		"CDN different port":      {registryToolOrigin("https://ghcr.io"), registryToolOrigin("https://pkg-containers.githubusercontent.com:444")},
		"HTTP registry":           {{URL: "http://ghcr.io", CIDRs: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}}, registryToolOrigin("https://pkg-containers.githubusercontent.com")},
	}
	for name, origins := range cases {
		t.Run(name, func(t *testing.T) {
			policy := httpclient.DestinationPolicy{Origins: origins}
			options, err := registryToolDestinationOptions(policy, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(options.RedirectOriginPairs) != 0 || options.AllowPublicCrossOriginRedirects {
				t.Fatal("missing exact operator approval gained cross-origin permission")
			}
			client, err := newRegistryToolDestinationClient(policy, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer client.CloseIdleConnections()
			// Later mutation of caller data cannot enable a pair on the frozen client.
			policy.Origins = []httpclient.TrustedOrigin{registryToolOrigin("https://ghcr.io"), registryToolOrigin("https://pkg-containers.githubusercontent.com")}
			source, _ := http.NewRequest(http.MethodGet, "https://ghcr.io/v2/test", nil)
			target, _ := http.NewRequest(http.MethodGet, "https://pkg-containers.githubusercontent.com/blob", nil)
			if err := client.CheckRedirect(target, []*http.Request{source}); err == nil {
				t.Fatal("strict default was widened after construction")
			}
		})
	}
}

func TestRegistryToolsRejectMalformedOperatorPolicy(t *testing.T) {
	for name, origins := range map[string][]httpclient.TrustedOrigin{
		"path":          {registryToolOrigin("https://ghcr.io/not-an-origin"), registryToolOrigin("https://pkg-containers.githubusercontent.com")},
		"credential":    {registryToolOrigin("https://fixture@ghcr.io"), registryToolOrigin("https://pkg-containers.githubusercontent.com")},
		"duplicate":     {registryToolOrigin("https://ghcr.io"), registryToolOrigin("https://GHCR.IO:443"), registryToolOrigin("https://pkg-containers.githubusercontent.com")},
		"missing CIDRs": {{URL: "https://ghcr.io"}, registryToolOrigin("https://pkg-containers.githubusercontent.com")},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := newRegistryToolDestinationClient(httpclient.DestinationPolicy{Restricted: true, Origins: origins}, nil); err == nil {
				t.Fatal("invalid operator policy accepted")
			}
		})
	}
}
