package deploy

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func helmTemplateExpectError(t *testing.T, valueFiles []string, sets ...string) string {
	return helmTemplateExpectErrorWithFlags(t, valueFiles, nil, sets...)
}

func helmTemplateExpectErrorWithFlags(t *testing.T, valueFiles, flags []string, sets ...string) string {
	t.Helper()
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skipf("helm binary not on PATH (%v); skipping chart-render test", err)
	}
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	chartDir := filepath.Join(filepath.Dir(here), "chart")
	valuesFile := filepath.Join(chartDir, "values.yaml")
	args := []string{"template", "astronomer", chartDir, "-f", valuesFile}
	args = append(args, flags...)
	for _, file := range valueFiles {
		args = append(args, "-f", file)
	}
	for _, s := range sets {
		args = append(args, "--set", s)
	}
	cmd := exec.Command("helm", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err == nil {
		t.Fatalf("helm template unexpectedly succeeded:\n%s", stdout.String())
	}
	return stderr.String()
}

func TestValuesSchemaRejectsInvalidTypes(t *testing.T) {
	errOut := helmTemplateExpectError(t, nil, "server.replicaCount=not-a-number")
	if !strings.Contains(errOut, "/server/replicaCount") || !strings.Contains(errOut, "got string, want integer") {
		t.Fatalf("schema error did not mention invalid server.replicaCount type:\n%s", errOut)
	}
}

func TestValuesSchemaRequiresProductionWiring(t *testing.T) {
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	prodValues := filepath.Join(filepath.Dir(here), "chart", "values-production.yaml")
	errOut := helmTemplateExpectError(t, []string{prodValues})
	for _, want := range []string{
		"/postgres/external",
		"/redis/external/address",
		"/config/serverURL",
		"/secrets/secretKey",
		"/bootstrap/email",
		"/managementBackup/s3/bucket",
		"/networkPolicy/externalPostgresEgressCIDRs",
		"/networkPolicy/externalRedisEgressCIDRs",
		"/networkPolicy/kubernetesAPIEgressCIDRs",
		"/gateway/hosts",
		"/tls/secretName",
		"/dex/clientSecret",
	} {
		if !strings.Contains(errOut, want) {
			t.Fatalf("schema error missing %q:\n%s", want, errOut)
		}
	}
}

func TestValuesSchemaAcceptsProductionWiring(t *testing.T) {
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	prodValues := filepath.Join(filepath.Dir(here), "chart", "values-production.yaml")
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
		// F8: production render requires a pinned bootstrap password (or an
		// existingSecret) so a GitOps re-render can't rotate the admin password.
		"bootstrap.password=prod-admin-initial",
		"dex.clientSecret=prod-dex-client-secret",
		"managementBackup.s3.bucket=astronomer-backups",
		"managementBackup.s3.credentialsSecretRef.name=astronomer-backup-creds",
		// OPS-01: production preflight requires key-wrap when backups are on.
		"managementBackup.encryptionKeyBackup.wrappingSecretRef.name=astronomer-key-wrap",
		"networkPolicy.externalPostgresEgressCIDRs[0]=10.20.0.0/16",
		"networkPolicy.externalRedisEgressCIDRs[0]=10.30.0.0/16",
		"networkPolicy.kubernetesAPIEgressCIDRs[0]=10.43.0.1/32",
		"networkPolicy.kubernetesAPIEgressCIDRs[1]=10.40.0.0/16",
	)
	assertRenderedContains(t, out,
		"ENV: \"production\"",
		"SERVER_URL: \"https://astronomer.example.com\"",
		"name: astronomer-management-backup",
	)
}

func TestProductionPreflightNetworkPolicyRequiresNarrowAPICIDRs(t *testing.T) {
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	prodValues := filepath.Join(filepath.Dir(here), "chart", "values-production.yaml")
	setsWithoutAPI := make([]string, 0, len(productionWiringSets)+1)
	for _, set := range productionWiringSets {
		if !strings.HasPrefix(set, "networkPolicy.kubernetesAPIEgressCIDRs") {
			setsWithoutAPI = append(setsWithoutAPI, set)
		}
	}
	setsWithoutAPI = append(setsWithoutAPI, "managementBackup.enabled=false")

	schemaErr := helmTemplateExpectError(t, []string{prodValues}, setsWithoutAPI...)
	if !strings.Contains(schemaErr, "/networkPolicy/kubernetesAPIEgressCIDRs") {
		t.Fatalf("production schema error does not identify missing API CIDRs:\n%s", schemaErr)
	}

	renderErr := helmTemplateExpectErrorWithFlags(t, []string{prodValues}, []string{"--skip-schema-validation"}, setsWithoutAPI...)
	for _, want := range []string{
		"networkPolicy.kubernetesAPIEgressCIDRs must contain",
		"kubernetes.default Service ClusterIP",
		"CNI DNAT ordering varies",
	} {
		if !strings.Contains(renderErr, want) {
			t.Fatalf("production render-time preflight error missing %q:\n%s", want, renderErr)
		}
	}

	singleAPI := append([]string{}, setsWithoutAPI...)
	singleAPI = append(singleAPI, "networkPolicy.kubernetesAPIEgressCIDRs[0]=10.43.0.1/32")
	singleErr := helmTemplateExpectError(t, []string{prodValues}, singleAPI...)
	if !strings.Contains(singleErr, "/networkPolicy/kubernetesAPIEgressCIDRs") || !strings.Contains(singleErr, "minItems: got 1, want 2") {
		t.Fatalf("production schema must require Service and endpoint CIDR entries:\n%s", singleErr)
	}

	for _, cidr := range []string{"0.0.0.0/0", "::/0"} {
		t.Run("reject "+cidr, func(t *testing.T) {
			broadSets := append([]string{}, productionWiringSets...)
			broadSets = append(broadSets,
				"managementBackup.enabled=false",
				"networkPolicy.externalEgressCIDRs[0]="+cidr,
			)
			broadErr := helmTemplateExpectError(t, []string{prodValues}, broadSets...)
			for _, want := range []string{"must not contain 0.0.0.0/0 or ::/0", "not a production fallback"} {
				if !strings.Contains(broadErr, want) {
					t.Fatalf("production broad-CIDR rejection missing %q:\n%s", want, broadErr)
				}
			}
		})
	}
}

func TestEventRelayQueueCapacitySchemaAndServerWiring(t *testing.T) {
	errOut := helmTemplateExpectError(t, nil, "server.eventRelayQueueCapacity=65537")
	if !strings.Contains(errOut, "eventRelayQueueCapacity") || !strings.Contains(errOut, "maximum") {
		t.Fatalf("event relay hard-max schema error missing capacity details:\n%s", errOut)
	}

	out := helmTemplate(t, "server.eventRelayQueueCapacity=2048")
	for _, want := range []string{
		"name: EVENT_RELAY_QUEUE_CAPACITY",
		`value: "2048"`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("server render missing %q", want)
		}
	}
}
