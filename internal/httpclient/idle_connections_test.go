package httpclient

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDestinationClientClosesIdleConnectionsThroughBothWrappers(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "owned TLS fixture")
	}))
	defer server.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(server.URL, "https://"))
	origin := "https://example.com:" + port
	policy := DestinationPolicy{Restricted: true, Origins: []TrustedOrigin{{URL: origin, CIDRs: []netip.Prefix{netip.MustParsePrefix("203.0.113.7/32")}}}}
	var dials atomic.Int32
	lookup := func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("203.0.113.7")}, nil
	}
	dial := func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != net.JoinHostPort("203.0.113.7", port) {
			t.Error("unchecked fixture destination dialed")
		}
		dials.Add(1)
		return (&net.Dialer{}).DialContext(ctx, network, strings.TrimPrefix(server.URL, "https://"))
	}
	trust := server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	trust.ServerName = "example.com"
	client, err := newDestinationClient(policy, DestinationOptions{Timeout: time.Second, TLSConfig: trust, MaxResponseBytes: 1024}, lookup, dial)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	fetch := func() {
		t.Helper()
		response, err := client.Get(origin + "/payload")
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil || string(body) != "owned TLS fixture" {
			t.Fatal("bounded TLS response failed")
		}
	}
	fetch()
	fetch()
	if dials.Load() != 1 {
		t.Fatalf("positive control did not reuse checked TLS connection: %d dials", dials.Load())
	}
	client.CloseIdleConnections()
	fetch()
	if dials.Load() != 2 {
		t.Fatalf("idle connection remained open behind wrappers: %d dials", dials.Load())
	}
}
