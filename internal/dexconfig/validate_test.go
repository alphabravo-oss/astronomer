package dexconfig

import (
	"strings"
	"testing"
)

func TestEveryRegisteredConnectorUsesClosedTypedContract(t *testing.T) {
	valid := map[string]map[string]any{
		"oidc":      {"issuer": "https://idp.example.com", "clientID": "id", "clientSecret": "secret"},
		"okta":      {"issuer": "https://tenant.okta.com", "clientID": "id", "clientSecret": "secret"},
		"microsoft": {"tenant": "tenant", "clientID": "id", "clientSecret": "secret"},
		"github":    {"clientID": "id", "clientSecret": "secret"},
		"gitlab":    {"baseURL": "https://gitlab.example.com", "clientID": "id", "clientSecret": "secret"},
		"bitbucket": {"clientID": "id", "clientSecret": "secret"},
		"google":    {"clientID": "id", "clientSecret": "secret"},
		"saml":      {"ssoURL": "https://idp.example.com/saml", "entityIssuer": "urn:example:idp", "nameIDPolicyFormat": "persistent"},
		"ldap":      {"host": "ldap.example.com:636", "bindDN": "cn=svc", "bindPW": "secret", "userSearch": map[string]any{"baseDN": "dc=example", "username": "uid", "idAttr": "uid", "emailAttr": "mail"}, "groupSearch": map[string]any{"baseDN": "ou=groups,dc=example", "userMatchers": []any{map[string]any{"userAttr": "DN", "groupAttr": "member"}}}},
		"oauth":     {"clientID": "id", "clientSecret": "secret", "tokenURL": "https://idp.example.com/token", "authorizationURL": "https://idp.example.com/authorize", "userInfoURL": "https://idp.example.com/userinfo"},
	}
	if len(valid) != len(Registry()) {
		t.Fatalf("fixtures=%d registry=%d", len(valid), len(Registry()))
	}
	for connectorType, config := range valid {
		t.Run(connectorType, func(t *testing.T) {
			if err := ValidateConnector(connectorType, config); err != nil {
				t.Fatal(err)
			}
			clone := make(map[string]any, len(config)+1)
			for key, value := range config {
				clone[key] = value
			}
			clone["futurePassword"] = "canary"
			if err := ValidateConnector(connectorType, clone); err == nil {
				t.Fatal("unknown secret-shaped field accepted")
			}
		})
	}
}

func TestCanonicalURLPolicyRejectsAmbiguousAndCredentialShapedURLs(t *testing.T) {
	for _, raw := range []string{
		"https://user:pass@example.com/path", "https://example.com/path#fragment", "https://example.com/path?token=x",
		"https://example.com/path?", "https://example.com/%70ath", "HTTPS://example.com/path", "https://EXAMPLE.com/path",
		"https://example.com:443/path", "https://example.com/a/../b", " https://example.com/path",
	} {
		t.Run(strings.NewReplacer(":", "_", "/", "_", "?", "_").Replace(raw), func(t *testing.T) {
			if err := ValidateURL(raw, false); err == nil {
				t.Fatalf("accepted %q", raw)
			}
		})
	}
	if err := ValidateURL("https://example.com/dex/", false); err != nil {
		t.Fatal(err)
	}
	if err := ValidateURL("http://localhost:5555/callback", true); err != nil {
		t.Fatal(err)
	}
}

func TestStaticExpiryExtraAndListenerContracts(t *testing.T) {
	clients := []map[string]any{{"id": "platform", "redirectURIs": []any{"https://platform.example/callback"}, "secret": "secret", "trustedPeers": []any{"cli"}}, {"id": "cli", "redirectURIs": []any{"http://localhost:5555/callback"}, "public": true}}
	if err := ValidateStaticClients(clients); err != nil {
		t.Fatal(err)
	}
	if err := ValidateExpiry(map[string]any{"idTokens": "1h", "refreshTokens": map[string]any{"reuseInterval": "3s"}}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateExtra(map[string]any{"logger": map[string]any{"level": "info", "format": "json"}, "grpc": map[string]any{"addr": "127.0.0.1:5557"}}); err != nil {
		t.Fatal(err)
	}
	for _, listener := range []string{"localhost", "localhost:0", "localhost:65536", ":5556"} {
		if err := ValidateListener(listener); err == nil {
			t.Fatalf("accepted listener %q", listener)
		}
	}
}

func TestRuntimeYAMLRejectsInvalidConnectorBeforeExecution(t *testing.T) {
	raw := []byte(`issuer: https://dex.example.com
storage: {type: kubernetes, config: {inCluster: true}}
web: {http: "0.0.0.0:5556"}
staticClients:
  - {id: platform, redirectURIs: [https://platform.example/callback], secret: secret}
connectors:
  - type: ldap
    id: ldap
    name: LDAP
    config: {host: "ldap.example.com:70000", bindDN: cn=svc, bindPW: secret, userSearch: {baseDN: dc=example, username: uid, idAttr: uid, emailAttr: mail}}
`)
	if err := ValidateRuntimeYAML(raw, 1<<20); err == nil {
		t.Fatal("invalid owned Secret would pass preflight")
	}
}
