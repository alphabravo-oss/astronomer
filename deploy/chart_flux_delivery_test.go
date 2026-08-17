package deploy

import (
	"strings"
	"testing"
)

func TestFluxNativeDeliveryConfigIsProjectedWithoutManagementControllers(t *testing.T) {
	out := helmTemplate(t)
	for _, want := range []string{
		`DELIVERY_ENABLED: "true"`,
		`DELIVERY_FLUX_VERSION: "v2.9.3"`,
		`DELIVERY_PUBLIC_REGISTRY: "ghcr.io"`,
		"DELIVERY_PRIVATE_REGISTRY:",
		"DELIVERY_FLUX_DISTRIBUTION_CERTIFICATE_IDENTITY:",
		"DELIVERY_FLUX_DISTRIBUTION_OIDC_ISSUER:",
		`DELIVERY_FLUX_DISTRIBUTION_REQUIRE_SIGNATURE: "true"`,
		"DELIVERY_BUNDLE_CERTIFICATE_IDENTITY:",
		"DELIVERY_BUNDLE_OIDC_ISSUER:",
		`DELIVERY_BUNDLE_REQUIRE_SIGNATURE: "true"`,
		"DELIVERY_ROLLOUT_MAX_CONCURRENT_CLUSTERS:",
		"DELIVERY_STATUS_COALESCE_WINDOW_SECONDS:",
		`DELIVERY_SOURCE_MAX_ARTIFACT_BYTES: "536870912"`,
		`DELIVERY_SOURCE_MAX_HELM_CHART_BYTES: "104857600"`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("delivery ConfigMap contract missing %q", want)
		}
	}

	for _, doc := range parseRenderedDocs(t, out) {
		if stringValue(doc["kind"]) != "Deployment" && stringValue(doc["kind"]) != "StatefulSet" {
			continue
		}
		name := stringAt(doc, "metadata", "name")
		for _, controller := range []string{"source-controller", "kustomize-controller", "helm-controller"} {
			if strings.Contains(name, controller) {
				t.Fatalf("management chart rendered downstream controller workload %s/%s", stringValue(doc["kind"]), name)
			}
		}
	}
}

func TestDeliveryValuesSchemaRejectsUnsafeReleaseInputs(t *testing.T) {
	tests := []struct {
		name string
		set  string
		want string
	}{
		{name: "engine cannot be disabled", set: "delivery.enabled=false", want: "/delivery/enabled"},
		{name: "signatures are mandatory", set: "delivery.artifacts.fluxDistribution.trustPolicy.requireSignature=false", want: "requireSignature"},
		{name: "mutable tags are rejected", set: "delivery.artifacts.fluxDistribution.ociRepository=ghcr.io/example/distribution:latest", want: "ociRepository"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errOut := helmTemplateExpectError(t, nil, tt.set)
			if !strings.Contains(errOut, tt.want) {
				t.Fatalf("schema rejection missing %q:\n%s", tt.want, errOut)
			}
		})
	}
}

func TestProductionDeliveryRejectsWorldOpenSourceEgress(t *testing.T) {
	sets := append([]string{}, productionWiringSets...)
	sets = append(sets,
		"managementBackup.enabled=false",
		"delivery.sourceResolution.egressCIDRs[0]=0.0.0.0/0",
	)
	errOut := helmTemplateExpectError(t, []string{"chart/values-production.yaml"}, sets...)
	if !strings.Contains(errOut, "/delivery/sourceResolution/egressCIDRs/0") {
		t.Fatalf("production broad source-egress rejection missing exact path:\n%s", errOut)
	}
}

func TestProductionDeliveryAcceptsVerifiedDisconnectedDistribution(t *testing.T) {
	sets := append([]string{}, productionWiringSets...)
	sets = append(sets,
		"managementBackup.enabled=false",
		"delivery.artifacts.fluxDistribution.ociRepository=",
		"delivery.artifacts.fluxDistribution.disconnectedAssetPath=/opt/astronomer/release/flux-distribution.tar.gz",
	)
	out := helmTemplateWithValueFiles(t, []string{"chart/values-production.yaml"}, sets...)
	if !strings.Contains(out, `DELIVERY_FLUX_DISTRIBUTION_ASSET_PATH: "/opt/astronomer/release/flux-distribution.tar.gz"`) {
		t.Fatal("verified disconnected distribution path was not projected")
	}
}

func TestDeliverySourceProxyCAAndEgressAreSecretBackedAndBounded(t *testing.T) {
	out := helmTemplate(t,
		"delivery.sourceResolution.proxy.enabled=true",
		"delivery.sourceResolution.proxy.urlSecretRef.name=source-proxy",
		"delivery.sourceResolution.proxy.urlSecretRef.key=url",
		"delivery.sourceResolution.ca.existingSecret=source-ca",
		"delivery.sourceResolution.ca.key=ca.crt",
		"delivery.sourceResolution.egressCIDRs[0]=203.0.113.0/24",
		"delivery.sourceResolution.allowSSH=true",
	)
	for _, want := range []string{
		"name: DELIVERY_SOURCE_PROXY_URL",
		`name: "source-proxy"`,
		`key: "url"`,
		"name: DELIVERY_SOURCE_CA_FILE",
		`secretName: "source-ca"`,
		`key: "ca.crt"`,
		`cidr: "203.0.113.0/24"`,
		"port: 22",
		`DELIVERY_SOURCE_EGRESS_CIDRS: "[\"203.0.113.0/24\"]"`,
		`DELIVERY_SOURCE_ALLOW_SSH: "true"`,
		`kube_read "delivery source proxy Secret key"`,
		`kube_read "delivery source CA Secret key"`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("source-resolution render missing %q", want)
		}
	}
	if strings.Contains(out, "http://user:password@") {
		t.Fatal("source proxy credentials were rendered inline")
	}
}

func TestFreshV1DatabasePreflightIsReadOnlyAndExact(t *testing.T) {
	docs := parseRenderedDocs(t, helmTemplate(t,
		"gateway.enabled=false",
		"tls.source=none",
		"postgres.bundled.enabled=false",
		"postgres.external.dsn=postgres://user:password@db.example.invalid/astronomer?sslmode=require",
	))
	job := findRenderedDoc(t, docs, "Job", "astronomer-preflight")
	container := findContainer(t, podSpecFor(job), "initContainers", "db-preflight")
	command := stringListValue(container["command"])
	if len(command) != 3 {
		t.Fatalf("db preflight command = %#v", command)
	}
	script := command[2]
	for _, want := range []string{
		"fresh_install_required:",
		"SELECT count(*), COALESCE(max(version), 0), COALESCE(bool_or(dirty), false)",
		"expected one clean schema_migrations row at version 1",
		"delivery_assignment_receipts",
		"delivery_controller_inventory",
		"never deletes, reformats, or upgrades a pre-v1 database",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("fresh-v1 DB preflight missing %q", want)
		}
	}
	upper := strings.ToUpper(script)
	for _, mutation := range []string{"DROP TABLE", "TRUNCATE ", "DELETE FROM", "INSERT INTO", "UPDATE SCHEMA_MIGRATIONS"} {
		if strings.Contains(upper, mutation) {
			t.Fatalf("fresh-v1 DB preflight contains mutating SQL %q", mutation)
		}
	}
}
