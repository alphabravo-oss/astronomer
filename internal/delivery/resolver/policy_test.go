package resolver

import (
	"context"
	"reflect"
	"testing"

	"github.com/google/uuid"
)

func TestStaticPolicyProviderCanonicalizesExactEnterpriseAllowlist(t *testing.T) {
	provider, err := NewStaticPolicyProvider(
		`["Git.Corp.Example.","*.svc.corp.example","git.corp.example"]`,
		`["10.42.1.7/16","2001:db8:42::7/48"]`,
		"https://proxy.corp.example:8443",
	)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := provider.NetworkPolicy(context.Background(), uuid.New(), uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := policy.AllowedPrivateHosts, []string{"*.svc.corp.example", "git.corp.example"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("hosts = %v, want %v", got, want)
	}
	if got, want := policy.AllowedPrivateCIDRs[0].String(), "10.42.0.0/16"; got != want {
		t.Fatalf("first CIDR = %q, want %q", got, want)
	}
	if got, err := provider.ProxyURL(context.Background(), uuid.New(), "default"); err != nil || got != "https://proxy.corp.example:8443" {
		t.Fatalf("ProxyURL() = %q, %v", got, err)
	}
	if _, err := provider.ProxyURL(context.Background(), uuid.New(), "tenant-controlled"); !HasCode(err, CodeNetworkDenied) {
		t.Fatalf("unknown proxy error = %v", err)
	}
}

func TestStaticPolicyProviderRejectsBroadOrCredentialBearingEntries(t *testing.T) {
	tests := []struct {
		name, hosts, cidrs, proxy string
	}{
		{name: "host wildcard", hosts: `["*"]`, cidrs: `[]`},
		{name: "host path", hosts: `["git.example.test/path"]`, cidrs: `[]`},
		{name: "malformed host JSON", hosts: `"git.example.test"`, cidrs: `[]`},
		{name: "malformed CIDR", hosts: `[]`, cidrs: `["10.0.0.1"]`},
		{name: "proxy credentials", hosts: `[]`, cidrs: `[]`, proxy: "https://user:secret@proxy.example.test"},
		{name: "plaintext proxy", hosts: `[]`, cidrs: `[]`, proxy: "http://proxy.example.test"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewStaticPolicyProvider(test.hosts, test.cidrs, test.proxy); err == nil {
				t.Fatal("expected policy validation error")
			}
		})
	}
}
