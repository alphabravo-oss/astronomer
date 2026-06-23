package auth

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseTokenScopes(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want []string
	}{
		{"nil_raw", "", nil},
		{"empty_array", "[]", nil},
		{"null", "null", nil},
		{"single", `["read"]`, []string{ScopeReadOnly}},
		{"multi", `["read","clusters:write"]`, []string{ScopeReadOnly, ScopeWriteClusters}},
		{"whitespace_trim", `[" read "," "]`, []string{ScopeReadOnly}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseTokenScopes(json.RawMessage(tc.raw))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("len = %d, want %d (%v vs %v)", len(got), len(tc.want), got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("got[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestParseTokenScopes_BadJSON(t *testing.T) {
	if _, err := ParseTokenScopes(json.RawMessage(`{"oops":true}`)); err == nil {
		t.Fatal("expected error for non-array JSON")
	}
}

func TestScopeAllowsRequest(t *testing.T) {
	cases := []struct {
		name     string
		scopes   []string
		required string
		want     bool
	}{
		{"empty_required", []string{ScopeReadOnly}, "", true},
		{"empty_scopes_legacy", nil, ScopeWriteClusters, true}, // backward compat
		{"explicit_match", []string{ScopeWriteClusters}, ScopeWriteClusters, true},
		{"miss", []string{ScopeReadOnly}, ScopeWriteClusters, false},
		{"admin_grants_all", []string{ScopeAdmin}, ScopeWriteRBAC, true},
		{"wildcard_grants_all", []string{ScopeWildcard}, ScopeWriteRBAC, true},
		{"miss_among_many", []string{ScopeReadOnly, ScopeWriteProjects}, ScopeWriteClusters, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ScopeAllowsRequest(tc.scopes, tc.required); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIsReadOnlyScopeSet(t *testing.T) {
	cases := []struct {
		name   string
		scopes []string
		want   bool
	}{
		{"empty_is_legacy_not_readonly", nil, false},
		{"read_only", []string{ScopeReadOnly}, true},
		{"read_plus_write", []string{ScopeReadOnly, ScopeWriteClusters}, false},
		{"write_only", []string{ScopeWriteProjects}, false},
		{"admin", []string{ScopeAdmin}, false},
		{"wildcard", []string{ScopeWildcard}, false},
		{"unknown_read_tier", []string{"metrics"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsReadOnlyScopeSet(tc.scopes); got != tc.want {
				t.Errorf("IsReadOnlyScopeSet(%v) = %v, want %v", tc.scopes, got, tc.want)
			}
		})
	}
}

func TestScopeForMethod(t *testing.T) {
	if ScopeForMethod("GET") != ScopeReadOnly {
		t.Errorf("GET should be read-only")
	}
	if ScopeForMethod("HEAD") != ScopeReadOnly {
		t.Errorf("HEAD should be read-only")
	}
	if got := ScopeForMethod("POST"); got != "" {
		t.Errorf("POST returned %q, want empty (route must opt in)", got)
	}
}

func TestParseAllowedCIDRs(t *testing.T) {
	cases := []struct {
		name      string
		raw       string
		wantCount int
		wantErr   bool
	}{
		{"empty", "", 0, false},
		{"whitespace_only", "   ", 0, false},
		{"single_cidr", "10.0.0.0/8", 1, false},
		{"bare_ipv4_auto_promotes", "10.1.2.3", 1, false},
		{"bare_ipv6_auto_promotes", "2001:db8::1", 1, false},
		{"multi", "10.0.0.0/8,192.168.1.5/32", 2, false},
		{"multi_with_whitespace", " 10.0.0.0/8 , 192.168.1.5/32 ", 2, false},
		{"empty_entries_skipped", "10.0.0.0/8,,", 1, false},
		{"bad_cidr", "not-a-cidr", 0, true},
		{"bad_mask", "10.0.0.0/99", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseAllowedCIDRs(tc.raw)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tc.wantErr)
			}
			if len(got) != tc.wantCount {
				t.Errorf("got %d nets, want %d", len(got), tc.wantCount)
			}
		})
	}
}

func TestIPAllowed_IPv4(t *testing.T) {
	nets, err := ParseAllowedCIDRs("10.0.0.0/8,192.168.1.5/32")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	cases := []struct {
		ip   string
		want bool
	}{
		{"10.5.6.7", true},
		{"192.168.1.5", true},
		{"192.168.1.6", false},
		{"172.16.0.1", false},
		{"::1", false},
	}
	for _, tc := range cases {
		t.Run(tc.ip, func(t *testing.T) {
			if got := IPAllowed(nets, net.ParseIP(tc.ip)); got != tc.want {
				t.Errorf("IPAllowed(%s) = %v, want %v", tc.ip, got, tc.want)
			}
		})
	}
}

func TestIPAllowed_IPv6(t *testing.T) {
	nets, err := ParseAllowedCIDRs("2001:db8::/32,::1")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	cases := []struct {
		ip   string
		want bool
	}{
		{"2001:db8::1", true},
		{"2001:db8:abcd::1", true},
		{"::1", true},
		{"2001:dead::1", false},
		{"10.0.0.1", false},
	}
	for _, tc := range cases {
		t.Run(tc.ip, func(t *testing.T) {
			if got := IPAllowed(nets, net.ParseIP(tc.ip)); got != tc.want {
				t.Errorf("IPAllowed(%s) = %v, want %v", tc.ip, got, tc.want)
			}
		})
	}
}

func TestIPAllowed_NilRemoteFailsClosed(t *testing.T) {
	nets, _ := ParseAllowedCIDRs("10.0.0.0/8")
	if IPAllowed(nets, nil) {
		t.Fatal("nil remote IP must fail closed")
	}
}

func TestRemoteIPForRequest_RealIPInRemoteAddr(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/t", nil)
	// Simulate chimiddleware.RealIP behaviour — XFF promoted into RemoteAddr.
	r.RemoteAddr = "203.0.113.7:54321"
	got := RemoteIPForRequest(r)
	if got == nil || got.String() != "203.0.113.7" {
		t.Fatalf("got %v, want 203.0.113.7", got)
	}
}

func TestRemoteIPForRequest_XFFFallback(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/t", nil)
	r.RemoteAddr = "" // simulate the case where the test setup never wired RealIP
	r.Header.Set("X-Forwarded-For", "198.51.100.5, 10.0.0.1")
	got := RemoteIPForRequest(r)
	if got == nil || got.String() != "198.51.100.5" {
		t.Fatalf("got %v, want 198.51.100.5", got)
	}
}

func TestRemoteIPForRequest_IPv6(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/t", nil)
	r.RemoteAddr = "[2001:db8::1]:443"
	got := RemoteIPForRequest(r)
	if got == nil || got.String() != "2001:db8::1" {
		t.Fatalf("got %v, want 2001:db8::1", got)
	}
}
