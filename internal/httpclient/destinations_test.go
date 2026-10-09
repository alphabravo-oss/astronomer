package httpclient

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestDestinationPolicyClosedOrigins(t *testing.T) {
	valid := `{"restricted":true,"origins":[{"url":"https://Registry.Corp:443","cidrs":["10.42.0.0/16","fd00::/8"]}]}`
	policy, err := ParseDestinationPolicy(valid)
	if err != nil {
		t.Fatal(err)
	}
	lookup := func(_ context.Context, _, _ string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("10.42.1.1"), netip.MustParseAddr("fd00::2")}, nil
	}
	for _, raw := range []string{"https://registry.corp/v2/", "https://registry.corp:443/v2/"} {
		if err := policy.validateURL(context.Background(), raw, lookup); err != nil {
			t.Fatal(err)
		}
	}
	for _, raw := range []string{"https://registry.corp:444/v2/", "http://registry.corp/v2/", "https://other.corp/v2/", "https://registry.corp./v2/", "https://user:password@registry.corp/v2/", "https://registry.corp:0443/v2/"} {
		if err := policy.validateURL(context.Background(), raw, lookup); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	for _, address := range []string{"10.43.0.1", "127.0.0.1", "::1", "169.254.169.254", "fe80::1", "::ffff:127.0.0.1", "224.0.0.1", "::"} {
		mixed := func(_ context.Context, _, _ string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("10.42.1.1"), netip.MustParseAddr(address)}, nil
		}
		if err := policy.validateURL(context.Background(), "https://registry.corp", mixed); err == nil {
			t.Fatalf("accepted DNS answer %s", address)
		}
	}
	for _, raw := range []string{`{"restricted":true,"origins":[]}`, `{"origins":[],"allow_private":true}`, `{"origins":[{"url":"https://registry.corp/path","cidrs":["10.0.0.0/8"]}]}`, `{"origins":[{"url":"http://registry.corp","cidrs":["0.0.0.0/0"]}]}`, valid + `{}`} {
		if _, err := ParseDestinationPolicy(raw); err == nil {
			t.Fatalf("accepted invalid policy %s", raw)
		}
	}
}

func TestDestinationClientPinsAddressAndRejectsRebinding(t *testing.T) {
	policy, err := ParseDestinationPolicy(`{"restricted":true,"origins":[{"url":"http://storage.corp:9000","cidrs":["10.0.0.0/8"]}]}`)
	if err != nil {
		t.Fatal(err)
	}
	calls, dials := 0, 0
	lookup := func(_ context.Context, _, _ string) ([]netip.Addr, error) {
		calls++
		ip := "10.42.1.7"
		if calls > 1 {
			ip = "169.254.169.254"
		}
		return []netip.Addr{netip.MustParseAddr(ip)}, nil
	}
	client, err := newDestinationClient(policy, DestinationOptions{Timeout: time.Second}, lookup, func(_ context.Context, _, _ string) (net.Conn, error) {
		dials++
		return nil, errors.New("unexpected dial")
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.Get("http://storage.corp:9000/bucket"); err == nil || dials != 0 || calls != 2 {
		t.Fatalf("rebind result err=%v calls=%d dials=%d", err, calls, dials)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "payload") }))
	defer server.Close()
	lookup = func(_ context.Context, _, _ string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("10.42.1.7")}, nil
	}
	captured := ""
	client, err = newDestinationClient(policy, DestinationOptions{Timeout: time.Second, MaxResponseBytes: 4}, lookup, func(ctx context.Context, network, address string) (net.Conn, error) {
		captured = address
		return (&net.Dialer{}).DialContext(ctx, network, strings.TrimPrefix(server.URL, "http://"))
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Get("http://storage.corp:9000/bucket")
	if err == nil {
		defer response.Body.Close()
		_, err = io.ReadAll(response.Body)
	}
	if captured != "10.42.1.7:9000" || err == nil {
		t.Fatalf("address=%s expected bounded response error: %v", captured, err)
	}
}

func TestDestinationRedirectStripsCredentialsAndDeniesNewOrigin(t *testing.T) {
	policy, err := ParseDestinationPolicy(`{"restricted":true,"origins":[{"url":"https://registry.corp","cidrs":["10.0.0.0/8"]},{"url":"https://other.corp","cidrs":["10.0.0.0/8"]}]}`)
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewDestinationClient(policy, DestinationOptions{})
	if err != nil {
		t.Fatal(err)
	}
	prior, _ := http.NewRequest("GET", "https://registry.corp/a", nil)
	next, _ := http.NewRequest("GET", "https://other.corp/a", nil)
	next.Header.Set("Authorization", "fixture-token")
	next.Header.Set("Proxy-Authorization", "fixture-token")
	if err = client.CheckRedirect(next, []*http.Request{prior}); err == nil || next.Header.Get("Authorization") != "" || next.Header.Get("Proxy-Authorization") != "" {
		t.Fatal("cross origin credential redirect accepted")
	}
	// A caller cannot widen the constructed client's copied policy later.
	policy.Origins[0].CIDRs[0] = netip.MustParsePrefix("0.0.0.0/0")
}
