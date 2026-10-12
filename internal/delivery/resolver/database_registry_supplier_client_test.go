//go:build unix

package resolver

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

func TestDatabasePublicationSupplierRequiresBothIndependentLiteralOrigins(t *testing.T) {
	for _, test := range []struct {
		name    string
		origins []string
		allowed bool
	}{
		{"neither", nil, false}, {"supplier-only", []string{"https://ghcr.io"}, false},
		{"cdn-only", []string{"https://pkg-containers.githubusercontent.com"}, false},
		{"unrelated-cdn", []string{"https://ghcr.io", "https://other.example"}, false},
		{"both-exact", []string{"https://ghcr.io", "https://pkg-containers.githubusercontent.com"}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			options, requests, _ := registryTrustFixture(t)
			for _, origin := range test.origins {
				options.Network.Origins = append(options.Network.Origins, httpclient.TrustedOrigin{URL: origin, CIDRs: []netip.Prefix{netip.MustParsePrefix("1.1.1.1/32")}})
			}
			transport, err := CaptureDatabaseRegistryTransport(t.Context(), options)
			if err != nil {
				t.Fatal(err)
			}
			defer transport.Close()
			roots, policy := transport.Fingerprints()
			before := transport.Network()
			client, err := transport.PublicationSupplierClient(t.Context())
			if (err == nil) != test.allowed {
				t.Fatal("supplier pair did not require exact independently approved origins")
			}
			if client != nil {
				defer client.CloseIdleConnections()
				if _, ok := client.Transport.(databaseSupplierRoundTripper); !ok {
					t.Fatal("owned supplier client exposed mutable TLS transport")
				}
			}
			afterRoots, afterPolicy := transport.Fingerprints()
			if roots != afterRoots || policy != afterPolicy || !reflect.DeepEqual(before, transport.Network()) || requests.Load() != 0 {
				t.Fatal("supplier constructor changed base transport identity or contacted a registry")
			}
		})
	}
}

func TestDatabasePublicationSupplierKeepsCurrentPhysicalRootOwner(t *testing.T) {
	options, requests, _ := registryTrustFixture(t)
	for _, origin := range []string{"https://ghcr.io", "https://pkg-containers.githubusercontent.com"} {
		options.Network.Origins = append(options.Network.Origins, httpclient.TrustedOrigin{URL: origin, CIDRs: []netip.Prefix{netip.MustParsePrefix("1.1.1.1/32")}})
	}
	transport, err := CaptureDatabaseRegistryTransport(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer transport.Close()
	client, err := transport.PublicationSupplierClient(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := transport.PublicationSupplierClient(canceled); !errors.Is(err, context.Canceled) {
		t.Fatal("supplier constructor swallowed cancellation")
	}
	request, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, options.Network.Origins[0].URL, nil)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if requests.Load() != 1 {
		t.Fatal("same captured CA did not serve owned TLS peer")
	}
	if err := os.WriteFile(filepath.Join(options.TrustDirectory, "registry.pem"), []byte("changed roots"), 0600); err != nil {
		t.Fatal(err)
	}
	response, err = client.Do(request)
	if response != nil {
		_ = response.Body.Close()
	}
	if err == nil || requests.Load() != 1 {
		t.Fatal("changed mounted roots reached the network")
	}
	transport.Close()
	if _, err := transport.PublicationSupplierClient(t.Context()); err == nil {
		t.Fatal("closed owner issued supplier client")
	}
}
