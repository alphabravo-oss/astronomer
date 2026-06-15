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
		"dex.clientSecret=prod-dex-client-secret",
		"managementBackup.s3.bucket=astronomer-backups",
		"managementBackup.s3.credentialsSecretRef.name=astronomer-backup-creds",
		"networkPolicy.externalPostgresEgressCIDRs[0]=10.20.0.0/16",
		"networkPolicy.externalRedisEgressCIDRs[0]=10.30.0.0/16",
	)
	assertRenderedContains(t, out,
		"ENV: \"production\"",
		"SERVER_URL: \"https://astronomer.example.com\"",
		"name: astronomer-management-backup",
	)
}
