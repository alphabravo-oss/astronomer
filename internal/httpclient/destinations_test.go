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
	for _, address := range []string{"10.43.0.1", "127.0.0.1", "::1", "169.254.169.254", "fe80::1", "::ffff:127.0.0.1", "fd00:ec2::254", "fd20:ce::254", "100.100.100.200", "224.0.0.1", "::"} {
		mixed := func(_ context.Context, _, _ string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("10.42.1.1"), netip.MustParseAddr(address)}, nil
		}
		if err := policy.validateURL(context.Background(), "https://registry.corp", mixed); err == nil {
			t.Fatalf("accepted DNS answer %s", address)
		}
	}
	for _, raw := range []string{`{"restricted":true,"origins":[]}`, `{"origins":[],"allow_private":true}`, `{"origins":[{"url":"https://registry.corp/path","cidrs":["10.0.0.0/8"]}]}`, `{"origins":[{"url":"http://registry.corp","cidrs":["0.0.0.0/0"]}]}`, valid + `{}`, `null`, `{"origins":[{"url":"http://registry.corp","cidrs":["10.0.0.0/7"]}]}`, `{"origins":[{"url":"http://registry.corp","cidrs":["172.16.0.0/11"]}]}`, `{"origins":[{"url":"http://registry.corp","cidrs":["192.168.0.0/15"]}]}`, `{"origins":[{"url":"http://registry.corp","cidrs":["fc00::/6"]}]}`} {
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

func TestDestinationClientCopiesAuthorityAndBlocksPrivateMetadata(t *testing.T) {
	for _, prefix := range []string{"10.42.1.0/24", "172.16.2.0/24", "192.168.7.0/24", "fd00:1234::/64"} {
		raw := `{"restricted":true,"origins":[{"url":"http://storage.corp:9000","cidrs":["` + prefix + `"]}]}`
		if _, err := ParseDestinationPolicy(raw); err != nil {
			t.Fatal(err)
		}
	}
	for _, ip := range []string{"fd00:ec2::254", "fd20:ce::254", "100.100.100.200"} {
		policy, err := ParseDestinationPolicy(`{"restricted":true,"origins":[{"url":"https://metadata.corp","cidrs":["fc00::/7","100.64.0.0/10"]}]}`)
		if err != nil {
			t.Fatal(err)
		}
		lookup := func(context.Context, string, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr(ip)}, nil
		}
		if err = policy.validateURL(context.Background(), "https://metadata.corp", lookup); err == nil {
			t.Fatalf("approved origin permitted cloud metadata %s", ip)
		}
	}
	policy, err := ParseDestinationPolicy(`{"restricted":true,"origins":[{"url":"http://storage.corp:9000","cidrs":["10.42.1.0/24"]}]}`)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	lookup := func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("10.43.1.7")}, nil
	}
	// Use the production copying constructor with a literal origin; mutate the
	// caller's slices and assert the constructed transport retains the old rule.
	policy.Origins[0].URL = "http://10.43.1.7:9000"
	client, err := NewDestinationClient(policy, DestinationOptions{Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	policy.Origins[0].CIDRs[0] = netip.MustParsePrefix("10.0.0.0/8")
	transport := client.Transport.(*responseLimitTransport).base.(*destinationTransport)
	transport.lookup = lookup
	transport.base = roundTripFixture(func(*http.Request) (*http.Response, error) { calls++; return nil, errors.New("unexpected connection") })
	if _, err = client.Get("http://10.43.1.7:9000/bucket"); err == nil || calls != 0 {
		t.Fatal("caller mutation widened immutable client policy")
	}
}

type roundTripFixture func(*http.Request) (*http.Response, error)

func (f roundTripFixture) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestPublicCDNRedirectConnectsWithoutCredentialsAndBlocksPrivate(t *testing.T) {
	received := 0
	destination := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received++
		for _, header := range []string{"Authorization", "Proxy-Authorization", "Cookie", "X-Internal-Credential"} {
			if r.Header.Get(header) != "" {
				t.Errorf("credential header %s crossed origin", header)
			}
		}
		_, _ = io.WriteString(w, "verified fixture payload")
	}))
	defer destination.Close()
	destinationPort := strings.Split(strings.TrimPrefix(destination.URL, "https://"), ":")[1]
	redirectTo := "https://example.com:" + destinationPort + "/chart"
	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, redirectTo, http.StatusFound) }))
	defer source.Close()
	sourcePort := strings.Split(strings.TrimPrefix(source.URL, "https://"), ":")[1]
	lookup := func(_ context.Context, _, host string) ([]netip.Addr, error) {
		if host == "private.corp" {
			return []netip.Addr{netip.MustParseAddr("10.42.1.7")}, nil
		}
		return []netip.Addr{netip.MustParseAddr("203.0.113.7")}, nil
	}
	dials := 0
	dial := func(ctx context.Context, network, address string) (net.Conn, error) {
		dials++
		host, port, _ := net.SplitHostPort(address)
		if host != "203.0.113.7" {
			t.Fatalf("unchecked/private connected IP %s", host)
		}
		var local string
		if port == sourcePort {
			local = strings.TrimPrefix(source.URL, "https://")
		} else if port == destinationPort {
			local = strings.TrimPrefix(destination.URL, "https://")
		} else {
			t.Fatal("unexpected port")
		}
		return (&net.Dialer{}).DialContext(ctx, network, local)
	}
	trust := source.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	trust.ServerName = "example.com"
	policy := DestinationPolicy{PrivateHosts: []string{"private.corp"}, PrivateCIDRs: []netip.Prefix{netip.MustParsePrefix("10.42.1.0/24")}}
	client, err := newDestinationClient(policy, DestinationOptions{Timeout: time.Second, TLSConfig: trust, AllowPublicCrossOriginRedirects: true, SensitiveHeaders: []string{"X-Internal-Credential"}}, lookup, dial)
	if err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequest("GET", "https://example.com:"+sourcePort+"/chart", nil)
	for _, header := range []string{"Authorization", "Proxy-Authorization", "Cookie", "X-Internal-Credential"} {
		request.Header.Set(header, "nonsecret-fixture-marker")
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || string(body) != "verified fixture payload" || received != 1 || dials != 2 {
		t.Fatalf("redirect result body=%q received=%d dials=%d error=%v", body, received, dials, err)
	}
	redirectTo = "https://private.corp:" + destinationPort + "/metadata"
	if _, err = client.Get("https://example.com:" + sourcePort + "/chart"); err == nil || received != 1 || dials != 2 {
		t.Fatalf("private redirect reached socket/destination: err=%v received=%d dials=%d", err, received, dials)
	}
	// Strict distribution/S3 defaults still deny even a public CDN redirect.
	strict, err := newDestinationClient(DestinationPolicy{}, DestinationOptions{Timeout: time.Second, TLSConfig: trust}, lookup, dial)
	if err != nil {
		t.Fatal(err)
	}
	redirectTo = "https://example.com:" + destinationPort + "/chart"
	if _, err = strict.Get("https://example.com:" + sourcePort + "/chart"); err == nil || received != 1 {
		t.Fatal("strict consumer silently accepted cross-origin fallback")
	}
}
