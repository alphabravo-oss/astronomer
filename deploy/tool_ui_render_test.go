package deploy

import (
	"strings"
	"testing"
)

func TestToolUIRoutesAllPathsToIsolatedServerAndIssuesWildcard(t *testing.T) {
	for _, gateway := range []bool{false, true} {
		mode := "ingress.enabled=true"
		if gateway {
			mode = "gateway.enabled=true"
		}
		rendered := helmTemplate(t, mode, "toolUI.domain=tools.example.test", "config.serverURL=https://console.example.test", "tls.source=letsEncrypt", "tls.letsEncrypt.email=ops@example.test", "tls.letsEncrypt.cloudflare.apiTokenSecretName=cloudflare-dns")
		docs := parseRenderedDocs(t, rendered)
		kind := "Ingress"
		if gateway {
			kind = "HTTPRoute"
		}
		tool := findRenderedDoc(t, docs, kind, "astronomer-tools")
		if tool == nil {
			t.Fatal("missing tool route")
		}
		// Both the routing and certificate SAN must include the wildcard. The
		// token is only a Secret reference; chart values never embed credentials.
		for _, want := range []string{"*.tools.example.test", "TOOL_UI_DOMAIN:", "dns01:", "cloudflare:", "apiTokenSecretRef:", "name: \"cloudflare-dns\""} {
			if !strings.Contains(rendered, want) {
				t.Fatalf("missing %s", want)
			}
		}
		if strings.Contains(rendered, "http01:") {
			t.Fatal("wildcard attempted HTTP-01")
		}
	}
}

func TestToolUIRejectsHTTPOnlyAndWildcardHTTP01(t *testing.T) {
	for _, sets := range [][]string{
		{"toolUI.domain=tools.example.test", "tls.source=none"},
		{"toolUI.domain=tools.example.test", "tls.source=letsEncrypt", "tls.letsEncrypt.email=ops@example.test"},
	} {
		sets = append(sets, testRenderSecretKeySet, testRenderEncryptionKeySet)
		if output := helmTemplateExpectError(t, nil, sets...); !strings.Contains(output, "toolUI.domain requires") {
			t.Fatalf("unexpected error: %s", output)
		}
	}
}
