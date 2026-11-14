package config

import (
	"strings"
	"testing"
)

func TestSharedIdentityConfiguration(t *testing.T) {
	t.Parallel()
	valid := Config{IdentityMode: "both", IdentityIssuer: "https://identity.example", IdentityClientID: "astronomer", IdentityClientSecret: "private-test-canary", IdentityJWKSURL: "https://identity.example/oauth/v2/keys", IdentityAuthorizationURL: "https://identity.example/oauth/v2/authorize", IdentityTokenURL: "https://identity.example/oauth/v2/token", IdentityRedirectURI: "https://astronomer.example/api/v1/auth/callback/identity/"}
	for _, tc := range []struct {
		name string
		edit func(*Config)
		bad  bool
	}{
		{"both", func(*Config) {}, false},
		{"sso_only_rejected", func(c *Config) { c.IdentityMode = "sso" }, true},
		{"local_has_no_identity_dependency", func(c *Config) { *c = Config{} }, false},
		{"unknown_mode", func(c *Config) { c.IdentityMode = "typo" }, true},
		{"missing_secret", func(c *Config) { c.IdentityClientSecret = "" }, true},
		{"missing_jwks", func(c *Config) { c.IdentityJWKSURL = "" }, true},
		{"plaintext_token_endpoint", func(c *Config) { c.IdentityTokenURL = "http://identity.example/token" }, true},
		{"userinfo", func(c *Config) { c.IdentityIssuer = "https://private-test-canary@example.com" }, true},
		{"query", func(c *Config) { c.IdentityRedirectURI += "?unsafe=1" }, true},
		{"fragment", func(c *Config) { c.IdentityAuthorizationURL += "#unsafe" }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := valid
			tc.edit(&c)
			_, err := c.SharedIdentity()
			if (err != nil) != tc.bad {
				t.Fatalf("unexpected validation result: %v", err)
			}
			if err != nil && strings.Contains(err.Error(), "private-test-canary") {
				t.Fatal("credential in validation error")
			}
		})
	}
}
