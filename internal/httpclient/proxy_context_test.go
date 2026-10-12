package httpclient

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"testing"
	"time"
)

func TestDestinationProxyOriginHasFiniteOperatorShape(t *testing.T) {
	for _, value := range []string{"", "https://proxy.example", "https://proxy.example:8443/"} {
		if err := ValidateProxyOrigin(value); err != nil {
			t.Fatal("valid operator proxy origin rejected")
		}
	}
	for _, value := range []string{"http://proxy.example", "https://user:fixture@proxy.example", "https://proxy.example/connect", "https://proxy.example?mode=all", "https://proxy.example?", "https://proxy.example#fragment", "//proxy.example", "https://proxy.example:0", "https://proxy.example."} {
		if err := ValidateProxyOrigin(value); err == nil {
			t.Fatal("unsupported proxy origin admitted")
		}
	}
}

func TestDestinationProxyConstructionJoinsOwnedDNSCancellation(t *testing.T) {
	owner, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	entered, finished := make(chan struct{}), make(chan struct{})
	lookup := func(ctx context.Context, _, _ string) ([]netip.Addr, error) {
		close(entered)
		defer close(finished)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	dial := func(context.Context, string, string) (net.Conn, error) {
		t.Error("constructor attempted a proxy connection")
		return nil, errors.New("unexpected dial")
	}
	result := make(chan error, 1)
	go func() {
		_, err := newDestinationClientContext(owner, DestinationPolicy{}, DestinationOptions{ProxyURL: "https://proxy.example"}, lookup, dial)
		result <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("constructor did not enter owned DNS")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("constructor lost owned cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("constructor did not return after owned DNS cancellation")
	}
	select {
	case <-finished:
	default:
		t.Fatal("constructor returned before owned DNS completed")
	}
}

func TestDestinationProxyConstructionCannotPublishExpiredOwner(t *testing.T) {
	owner, cancel := context.WithCancel(context.Background())
	defer cancel()
	lookup := func(context.Context, string, string) ([]netip.Addr, error) {
		cancel() // Simulate a resolver that returns data while ignoring cancellation.
		return []netip.Addr{netip.MustParseAddr("203.0.113.7")}, nil
	}
	client, err := newDestinationClientContext(owner, DestinationPolicy{}, DestinationOptions{ProxyURL: "https://proxy.example"}, lookup, nil)
	if client != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("expired constructor published a usable client")
	}
	called := false
	lookup = func(context.Context, string, string) ([]netip.Addr, error) { called = true; return nil, nil }
	for _, ctx := range []context.Context{nil, owner} {
		if client, err := newDestinationClientContext(ctx, DestinationPolicy{}, DestinationOptions{ProxyURL: "https://proxy.example"}, lookup, nil); client != nil || err == nil || called {
			t.Fatal("missing or expired constructor owner started DNS")
		}
	}
}

func TestDestinationProxySelectionIgnoresAmbientAuthority(t *testing.T) {
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		t.Setenv(name, "https://ambient.invalid:8443")
	}
	t.Setenv("NO_PROXY", "*")
	lookup := func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("203.0.113.7")}, nil
	}
	request, err := http.NewRequest(http.MethodGet, "https://charts.example/index.yaml", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, configured := range []string{"", "https://proxy.example:8443"} {
		client, err := newDestinationClientContext(context.Background(), DestinationPolicy{}, DestinationOptions{ProxyURL: configured}, lookup, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer client.CloseIdleConnections()
		transport := client.Transport.(*responseLimitTransport).base.(*destinationTransport).base.(*http.Transport)
		if configured == "" {
			if transport.Proxy != nil {
				t.Fatal("ambient proxy acquired repository authority")
			}
			continue
		}
		proxy, err := transport.Proxy(request)
		if err != nil || proxy == nil || proxy.String() != configured {
			t.Fatal("ambient proxy bypassed the explicit operator proxy")
		}
	}
}
