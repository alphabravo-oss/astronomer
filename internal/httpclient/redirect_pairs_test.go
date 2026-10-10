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
	"sync/atomic"
	"testing"
	"time"
)

func TestRedirectOriginPairRequiresExplicitFiniteAuthority(t *testing.T) {
	policy := DestinationPolicy{Origins: []TrustedOrigin{
		{URL: "https://source.example", CIDRs: []netip.Prefix{netip.MustParsePrefix("203.0.113.0/24")}},
		{URL: "https://cdn.example", CIDRs: []netip.Prefix{netip.MustParsePrefix("203.0.113.0/24")}},
	}}
	valid := RedirectOriginPair{From: "https://SOURCE.example:443", To: "https://cdn.example/"}
	for name, options := range map[string]DestinationOptions{
		"unapproved destination": {RedirectOriginPairs: []RedirectOriginPair{{From: valid.From, To: "https://other.example"}}},
		"unapproved source":      {RedirectOriginPairs: []RedirectOriginPair{{From: "https://other.example", To: valid.To}}},
		"plaintext":              {RedirectOriginPairs: []RedirectOriginPair{{From: "http://source.example", To: valid.To}}},
		"path":                   {RedirectOriginPairs: []RedirectOriginPair{{From: valid.From, To: "https://cdn.example/path"}}},
		"query":                  {RedirectOriginPairs: []RedirectOriginPair{{From: valid.From, To: "https://cdn.example?query=1"}}},
		"empty query":            {RedirectOriginPairs: []RedirectOriginPair{{From: valid.From, To: "https://cdn.example?"}}},
		"userinfo":               {RedirectOriginPairs: []RedirectOriginPair{{From: "https://fixture@source.example", To: valid.To}}},
		"trailing dot":           {RedirectOriginPairs: []RedirectOriginPair{{From: "https://source.example.", To: valid.To}}},
		"same origin":            {RedirectOriginPairs: []RedirectOriginPair{{From: valid.From, To: "https://source.example"}}},
		"duplicate normalized":   {RedirectOriginPairs: []RedirectOriginPair{valid, {From: "https://source.example", To: "https://cdn.example:443"}}},
		"too many pairs":         {RedirectOriginPairs: make([]RedirectOriginPair, 9)},
		"public fallback":        {RedirectOriginPairs: []RedirectOriginPair{valid}, AllowPublicCrossOriginRedirects: true},
		"too many hops":          {RedirectOriginPairs: []RedirectOriginPair{valid}, MaxRedirects: 3},
		"negative hops":          {RedirectOriginPairs: []RedirectOriginPair{valid}, MaxRedirects: -1},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewDestinationClient(policy, options); err == nil {
				t.Fatal("invalid constructor redirect authority accepted")
			}
		})
	}
	for _, restricted := range []bool{false, true} {
		policy.Restricted = restricted
		if _, err := NewDestinationClient(policy, DestinationOptions{RedirectOriginPairs: []RedirectOriginPair{valid}}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFiniteOriginRedirectPinsConnectionsAndStripsCredentialsOnEveryHop(t *testing.T) {
	var mode, received, dials, lookups atomic.Int32
	credentials := []string{"Authorization", "Proxy-Authorization", "Cookie", "X-Registry-Credential"}
	cdn := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, header := range credentials {
			if r.Header.Get(header) != "" {
				t.Errorf("credential %s crossed or was restored after an origin change", header)
			}
		}
		if r.Header.Get("Range") != "bytes=0-0" {
			t.Error("range header was lost")
		}
		if r.URL.Path == "/first" {
			http.Redirect(w, r, "/second", http.StatusTemporaryRedirect)
			return
		}
		if mode.Load() == 1 {
			http.Redirect(w, r, "/third", http.StatusTemporaryRedirect)
			return
		}
		received.Add(1)
		w.Header().Set("Content-Range", "bytes 0-0/1")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = io.WriteString(w, "x")
	}))
	defer cdn.Close()
	_, cdnPort, _ := net.SplitHostPort(strings.TrimPrefix(cdn.URL, "https://"))
	cdnOrigin := "https://example.com:" + cdnPort
	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			t.Error("initial registry credential was unexpectedly removed")
		}
		target := cdnOrigin + "/first"
		if mode.Load() == 2 {
			target = "https://unapproved.example:" + cdnPort + "/first"
		}
		http.Redirect(w, r, target, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	_, sourcePort, _ := net.SplitHostPort(strings.TrimPrefix(source.URL, "https://"))
	sourceOrigin := "https://example.com:" + sourcePort
	policy := DestinationPolicy{Restricted: true, Origins: []TrustedOrigin{
		{URL: sourceOrigin, CIDRs: []netip.Prefix{netip.MustParsePrefix("203.0.113.0/24")}},
		{URL: cdnOrigin, CIDRs: []netip.Prefix{netip.MustParsePrefix("203.0.113.0/24")}},
	}}
	lookup := func(context.Context, string, string) ([]netip.Addr, error) {
		lookupNumber := lookups.Add(1)
		if mode.Load() == 3 && lookupNumber >= 3 || mode.Load() == 4 && lookupNumber >= 2 {
			return []netip.Addr{netip.MustParseAddr("203.0.113.7"), netip.MustParseAddr("10.43.1.7")}, nil
		}
		return []netip.Addr{netip.MustParseAddr("203.0.113.7")}, nil
	}
	dial := func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil || host != "203.0.113.7" {
			return nil, errors.New("fixture saw an unchecked connection")
		}
		var local string
		switch port {
		case sourcePort:
			local = strings.TrimPrefix(source.URL, "https://")
		case cdnPort:
			local = strings.TrimPrefix(cdn.URL, "https://")
		default:
			return nil, errors.New("fixture saw an unapproved port")
		}
		dials.Add(1)
		return (&net.Dialer{}).DialContext(ctx, network, local)
	}
	trust := source.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	trust.ServerName = "example.com"
	pairs := []RedirectOriginPair{{From: sourceOrigin, To: cdnOrigin}}
	sensitive := []string{"X-Registry-Credential"}
	client, err := newDestinationClient(policy, DestinationOptions{Timeout: time.Second, TLSConfig: trust, RedirectOriginPairs: pairs, SensitiveHeaders: sensitive}, lookup, dial)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	// Constructed authority and secret-header selection are snapshots.
	pairs[0].To = "https://unapproved.example:" + cdnPort
	sensitive[0] = "X-Unrelated"
	policy.Origins[0].CIDRs[0] = netip.MustParsePrefix("0.0.0.0/0")
	policy.Origins[1].CIDRs[0] = netip.MustParsePrefix("0.0.0.0/0")
	request := func() (*http.Response, error) {
		r, _ := http.NewRequest(http.MethodGet, sourceOrigin+"/blob", nil)
		for _, header := range credentials {
			r.Header.Set(header, "nonsecret-fixture-marker")
		}
		r.Header.Set("Range", "bytes=0-0")
		return client.Do(r)
	}
	response, err := request()
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || response.StatusCode != http.StatusPartialContent || string(body) != "x" || received.Load() != 1 || dials.Load() != 2 {
		t.Fatalf("approved two-hop range failed: status=%d body=%q received=%d dials=%d err=%v", response.StatusCode, body, received.Load(), dials.Load(), err)
	}
	for _, scenario := range []int32{1, 2, 3, 4} {
		mode.Store(scenario)
		lookups.Store(0)
		client.CloseIdleConnections()
		beforeDials := dials.Load()
		if response, err = request(); err == nil {
			_ = response.Body.Close()
			t.Fatalf("invalid redirect scenario %d succeeded", scenario)
		}
		if received.Load() != 1 {
			t.Fatal("invalid redirect delivered payload")
		}
		if (scenario == 2 || scenario == 3) && dials.Load() != beforeDials+1 || scenario == 4 && dials.Load() != beforeDials {
			t.Fatalf("unapproved origin or mixed DNS set reached connection: scenario=%d dials=%d prior=%d", scenario, dials.Load(), beforeDials)
		}
	}
}

func TestFiniteRedirectRejectsMetadataEvenWithExplicitCIDRAuthority(t *testing.T) {
	for _, address := range []string{"127.0.0.1", "::ffff:127.0.0.1", "169.254.169.254", "fd00:ec2::254", "fd20:ce::254", "100.100.100.200"} {
		t.Run(address, func(t *testing.T) {
			policy := DestinationPolicy{Restricted: true, Origins: []TrustedOrigin{
				{URL: "https://source.example", CIDRs: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0"), netip.MustParsePrefix("::/0")}},
				{URL: "https://cdn.example", CIDRs: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0"), netip.MustParsePrefix("::/0")}},
			}}
			lookup := func(context.Context, string, string) ([]netip.Addr, error) {
				return []netip.Addr{netip.MustParseAddr(address)}, nil
			}
			dials := 0
			client, err := newDestinationClient(policy, DestinationOptions{RedirectOriginPairs: []RedirectOriginPair{{From: "https://source.example", To: "https://cdn.example"}}}, lookup, func(context.Context, string, string) (net.Conn, error) {
				dials++
				return nil, errors.New("unexpected socket")
			})
			if err != nil {
				t.Fatal(err)
			}
			prior, _ := http.NewRequest(http.MethodGet, "https://source.example/blob", nil)
			next, _ := http.NewRequest(http.MethodGet, "https://cdn.example/blob", nil)
			if err = client.CheckRedirect(next, []*http.Request{prior}); err == nil || dials != 0 {
				t.Fatal("explicit finite redirect authority permitted metadata access")
			}
		})
	}
}

func TestFiniteRedirectDirectionAndHopLimit(t *testing.T) {
	policy := DestinationPolicy{Restricted: true, Origins: []TrustedOrigin{
		{URL: "https://source.example", CIDRs: []netip.Prefix{netip.MustParsePrefix("203.0.113.0/24")}},
		{URL: "https://cdn.example", CIDRs: []netip.Prefix{netip.MustParsePrefix("203.0.113.0/24")}},
	}}
	lookups := 0
	lookup := func(context.Context, string, string) ([]netip.Addr, error) {
		lookups++
		return []netip.Addr{netip.MustParseAddr("203.0.113.7")}, nil
	}
	client, err := newDestinationClient(policy, DestinationOptions{MaxRedirects: 1, RedirectOriginPairs: []RedirectOriginPair{{From: "https://source.example", To: "https://cdn.example"}}}, lookup, nil)
	if err != nil {
		t.Fatal(err)
	}
	source, _ := http.NewRequest(http.MethodGet, "https://source.example/blob", nil)
	cdn, _ := http.NewRequest(http.MethodGet, "https://cdn.example/blob", nil)
	if err = client.CheckRedirect(cdn, []*http.Request{source}); err != nil || lookups != 1 {
		t.Fatal("explicit directed first hop rejected")
	}
	if err = client.CheckRedirect(source, []*http.Request{cdn}); err == nil || lookups != 1 {
		t.Fatal("reverse transition gained authority")
	}
	if err = client.CheckRedirect(cdn, []*http.Request{source, cdn}); err == nil || lookups != 1 {
		t.Fatal("explicit one-hop bound was ignored")
	}
}
