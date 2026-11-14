package deploy

import (
	"strings"
	"testing"
)

func TestLocalAlwaysAndOptionalIdentityUseNoBroker(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"local", "both"} {
		t.Run(mode, func(t *testing.T) {
			sets := []string{"identity.mode=" + mode}
			if mode == "both" {
				sets = append(sets, "identity.issuer=https://identity.example.test", "identity.clientID=astronomer", "identity.clientSecretRef.name=identity-client", "identity.jwksURL=https://identity.example.test/keys", "identity.authorizationURL=https://identity.example.test/authorize", "identity.tokenURL=https://identity.example.test/token", "identity.redirectURI=https://product.example.test/api/v1/auth/callback/identity/", "identity.ca.existingSecret=identity-ca")
			}
			out := helmTemplate(t, sets...)
			for _, forbidden := range []string{"astronomer-dex", "dexidp/dex", "DEX_BUNDLED", "AUTH_LOCAL_PASSWORD_ONLY"} {
				if strings.Contains(out, forbidden) {
					t.Fatalf("render retains retired broker contract %s", forbidden)
				}
			}
			docs := parseRenderedDocs(t, out)
			server := findRenderedDoc(t, docs, "Deployment", "astronomer-server")
			container := findContainer(t, podSpecFor(server), "containers", "server")
			env, _ := container["env"].([]any)
			foundSecret := false
			for _, entry := range env {
				e, _ := entry.(map[string]any)
				if e["name"] == "IDENTITY_CLIENT_SECRET" {
					foundSecret = stringAt(e, "valueFrom", "secretKeyRef", "name") == "identity-client"
					if e["value"] != nil {
						t.Fatal("identity secret rendered inline")
					}
				}
			}
			if foundSecret != (mode == "both") {
				t.Fatal("optional SSO secret wiring differs")
			}
			if mode == "local" && strings.Contains(out, "identity-ca") {
				t.Fatal("local login unexpectedly depends on identity CA")
			}
		})
	}
}
