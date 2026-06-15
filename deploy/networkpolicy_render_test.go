package deploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNetworkPolicyRendersNamespaceDefaultDeny(t *testing.T) {
	out := helmTemplate(t)

	for _, want := range []string{
		"name: astronomer-default-deny",
		"app.kubernetes.io/component: network-policy",
		"podSelector: {}",
		"- Ingress",
		"- Egress",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered chart missing %q:\n%s", want, out)
		}
	}
}

func TestNetworkPolicySupportsGranularExternalEgressCIDRs(t *testing.T) {
	valuesPath := filepath.Join(t.TempDir(), "networkpolicy-values.yaml")
	values := []byte(`
networkPolicy:
  externalEgressCIDRs: []
  externalHTTPSEgressCIDRs:
    - 10.50.0.0/16
  externalPostgresEgressCIDRs:
    - 10.20.0.0/16
  externalRedisEgressCIDRs:
    - 10.30.0.0/16
  kubernetesAPIEgressCIDRs:
    - 10.40.0.0/16
  identityProviderEgressCIDRs:
    - 10.60.0.0/16
dex:
  enabled: true
`)
	if err := os.WriteFile(valuesPath, values, 0o600); err != nil {
		t.Fatalf("write values override: %v", err)
	}

	out := helmTemplateWithValueFiles(t, []string{valuesPath})
	if strings.Contains(out, `cidr: "0.0.0.0/0"`) {
		t.Fatalf("granular override still rendered broad legacy egress:\n%s", out)
	}

	for _, want := range []string{
		`cidr: "10.50.0.0/16"`,
		`cidr: "10.20.0.0/16"`,
		`cidr: "10.30.0.0/16"`,
		`cidr: "10.40.0.0/16"`,
		`cidr: "10.60.0.0/16"`,
		"port: 443",
		"port: 5432",
		"port: 6379",
		"port: 6443",
		"port: 389",
		"port: 636",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered chart missing %q:\n%s", want, out)
		}
	}
}

func TestNetworkPolicyRendersExpectedComponentPolicies(t *testing.T) {
	docs := parseRenderedDocs(t, helmTemplate(t, "dex.enabled=true"))

	defaultDeny := findRenderedDoc(t, docs, "NetworkPolicy", "astronomer-default-deny")
	if selector := nestedMap(defaultDeny, "spec", "podSelector"); selector == nil || len(selector) != 0 {
		t.Fatalf("default-deny policy should select every pod with an empty selector: %#v", selector)
	}
	assertPolicyTypes(t, defaultDeny, "Ingress", "Egress")

	for _, tt := range []struct {
		name      string
		component string
	}{
		{name: "astronomer-frontend", component: "frontend"},
		{name: "astronomer-server", component: "server"},
		{name: "astronomer-worker", component: "worker"},
		{name: "astronomer-dex", component: "dex"},
		{name: "astronomer-postgres", component: "postgres"},
		{name: "astronomer-redis", component: "redis"},
	} {
		policy := findRenderedDoc(t, docs, "NetworkPolicy", tt.name)
		assertPolicyTypes(t, policy, "Ingress", "Egress")
		labels := nestedMap(policy, "spec", "podSelector", "matchLabels")
		if got := stringValue(labels["app.kubernetes.io/component"]); got != tt.component {
			t.Fatalf("%s selector component = %q, want %q", tt.name, got, tt.component)
		}
	}

	for _, name := range []string{"astronomer-postgres", "astronomer-redis"} {
		policy := findRenderedDoc(t, docs, "NetworkPolicy", name)
		rawEgress, _ := nestedMap(policy, "spec")["egress"].([]any)
		if len(rawEgress) != 0 {
			t.Fatalf("%s should not allow outbound egress, got %#v", name, rawEgress)
		}
	}
}

func TestProductionNetworkPolicyUsesGranularExternalDependencyCIDRs(t *testing.T) {
	prodValues := filepath.Join("chart", "values-production.yaml")
	out := helmTemplateWithValueFiles(t, []string{prodValues},
		"config.serverURL=https://astronomer.example.com",
		"gateway.hosts[0]=astronomer.example.com",
		"tls.source=secret",
		"tls.secretName=astronomer-tls",
		"postgres.external.dsnSecretRef.name=astronomer-postgres-dsn",
		"redis.external.address=redis.astronomer.svc.cluster.local:6379",
		"secrets.secretKey=prod-jwt-signing-key",
		"secrets.encryptionKey=prod-fernet-key",
		"bootstrap.email=admin@example.com",
		"dex.clientSecret=prod-dex-client-secret",
		"managementBackup.s3.bucket=astronomer-backups",
		"managementBackup.s3.credentialsSecretRef.name=astronomer-backup-creds",
		"networkPolicy.externalPostgresEgressCIDRs[0]=10.20.0.0/16",
		"networkPolicy.externalRedisEgressCIDRs[0]=10.30.0.0/16",
	)
	if strings.Contains(out, `cidr: "0.0.0.0/0"`) {
		t.Fatalf("production render should not include broad external egress:\n%s", out)
	}
	for _, want := range []string{`cidr: "10.20.0.0/16"`, `cidr: "10.30.0.0/16"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("production render missing %q:\n%s", want, out)
		}
	}

	docs := parseRenderedDocs(t, out)
	for _, absent := range []string{"astronomer-postgres", "astronomer-redis"} {
		if renderedDocExists(docs, "NetworkPolicy", absent) {
			t.Fatalf("production render should not include bundled %s NetworkPolicy when bundled Postgres/Redis are disabled", absent)
		}
	}
}

func assertPolicyTypes(t *testing.T, policy renderedDoc, wants ...string) {
	t.Helper()
	rawTypes, _ := nestedMap(policy, "spec")["policyTypes"].([]any)
	got := map[string]bool{}
	for _, raw := range rawTypes {
		got[stringValue(raw)] = true
	}
	for _, want := range wants {
		if !got[want] {
			t.Fatalf("%s missing policyType %q", stringAt(policy, "metadata", "name"), want)
		}
	}
}

func renderedDocExists(docs []renderedDoc, kind, name string) bool {
	for _, doc := range docs {
		if stringValue(doc["kind"]) == kind && stringAt(doc, "metadata", "name") == name {
			return true
		}
	}
	return false
}
