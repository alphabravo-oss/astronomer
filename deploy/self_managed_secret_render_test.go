package deploy

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReferenceOnlyChartRenderUsesNativeSecretReferences(t *testing.T) {
	chart := filepath.Join(repoRoot(t), "deploy", "chart")
	args := []string{"template", "astronomer", chart,
		"--set", "secrets.existingSecret=core-credentials",
		"--set", "secrets.secretKey=INLINE-SIGNING-CANARY",
		"--set", "secrets.encryptionKey=INLINE-ENCRYPTION-CANARY",
		"--set", "secrets.secretKeyKey=SIGNING_KEY",
		"--set", "secrets.encryptionKeyKey=FERNET_KEY",
		"--set", "bootstrap.existingSecret=bootstrap-credentials",
		"--set", "bootstrap.password=INLINE-BOOTSTRAP-CANARY",
		"--set", "bootstrap.existingSecretKey=initial-password",
		"--set", "postgres.bundled.enabled=true",
		"--set", "postgres.passwordSecretRef.name=database-credentials",
		"--set", "postgres.passwordSecretRef.key=password",
		"--set", "postgres.external.dsnSecretRef.name=database-credentials",
		"--set", "postgres.external.dsnSecretRef.key=dsn",
		"--set", "redis.bundled.enabled=false",
		"--set", "redis.external.urlSecretRef.name=redis-credentials",
		"--set", "redis.external.urlSecretRef.key=url",
		"--set", "dex.enabled=true",
		"--set", "dex.clientSecretRef.name=dex-credentials",
		"--set", "dex.clientSecretRef.key=clientSecret",
		"--set", "dex.clientSecret=INLINE-DEX-CANARY",
	}
	command := exec.Command("helm", args...)
	var stdout bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &bytes.Buffer{}
	err := command.Run()
	if err != nil {
		t.Fatalf("reference-only render failed: %v", err)
	}
	rendered := stdout.String()
	for _, want := range []string{
		"core-credentials", "SIGNING_KEY", "FERNET_KEY",
		"bootstrap-credentials", "initial-password",
		"database-credentials", "dsn", "password",
		"redis-credentials", "url",
		"dex-credentials", "clientSecret", "secretEnv: ASTRONOMER_DEX_CLIENT_SECRET",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("render missing %q", want)
		}
	}
	for _, forbidden := range []string{"name: astronomer-secrets\n", "name: astronomer-bootstrap\n", "clientSecret: |"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("render contains legacy inline/owned credential output %q", forbidden)
		}
	}
	for _, canary := range []string{"INLINE-SIGNING-CANARY", "INLINE-ENCRYPTION-CANARY", "INLINE-BOOTSTRAP-CANARY", "INLINE-DEX-CANARY"} {
		if strings.Contains(rendered, canary) {
			t.Fatalf("reference-backed render leaked legacy inline canary %s", canary)
		}
	}
}

func TestProductionReferenceOnlyValuesRenderEveryCredentialConsumer(t *testing.T) {
	chart := filepath.Join(repoRoot(t), "deploy", "chart")
	production := filepath.Join(chart, "values-production.yaml")
	args := []string{"template", "astronomer", chart, "-f", production,
		"--set", "config.serverURL=https://astronomer.example.com",
		"--set", "gateway.hosts[0]=astronomer.example.com",
		"--set", "tls.source=secret", "--set", "tls.secretName=astronomer-tls",
		"--set", "postgres.external.dsnSecretRef.name=database-credentials", "--set", "postgres.external.dsnSecretRef.key=dsn",
		"--set", "redis.external.urlSecretRef.name=redis-credentials", "--set", "redis.external.urlSecretRef.key=url",
		"--set", "secrets.existingSecret=core-credentials",
		"--set", "bootstrap.existingSecret=bootstrap-credentials",
		"--set", "bootstrap.email=admin@example.com",
		"--set", "dex.clientSecretRef.name=dex-credentials",
		"--set", "networkPolicy.externalPostgresEgressCIDRs[0]=10.20.0.0/16",
		"--set", "networkPolicy.externalRedisEgressCIDRs[0]=10.30.0.0/16",
		"--set", "networkPolicy.kubernetesAPIEgressCIDRs[0]=10.40.0.0/14",
		"--set", "managementBackup.s3.bucket=management-backups",
		"--set", "managementBackup.s3.credentialsSecretRef.name=backup-credentials",
		"--set", "managementBackup.encryptionKeyBackup.wrappingSecretRef.name=backup-wrap",
		"--set", "managementRestoreDrill.decryptCheck.wrappingSecretRef.name=backup-wrap",
	}
	command := exec.Command("helm", args...)
	var stdout bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &bytes.Buffer{}
	if err := command.Run(); err != nil {
		t.Fatalf("production reference-only render failed: %v", err)
	}
	rendered := stdout.String()
	for name, minimum := range map[string]int{
		"database-credentials":  5,
		"redis-credentials":     2,
		"core-credentials":      3,
		"bootstrap-credentials": 1,
		"dex-credentials":       1,
		"backup-credentials":    2,
		"backup-wrap":           2,
	} {
		count := strings.Count(rendered, "name: "+name) + strings.Count(rendered, `name: "`+name+`"`) + strings.Count(rendered, "secretName: "+name) + strings.Count(rendered, `secretName: "`+name+`"`)
		if count < minimum {
			t.Fatalf("%s referenced %d times, want at least %d consumers", name, count, minimum)
		}
	}
	for _, legacy := range []string{"name: astronomer-secrets\n", "name: astronomer-bootstrap\n"} {
		if strings.Contains(rendered, legacy) {
			t.Fatalf("reference-only production render owned legacy credential %q", legacy)
		}
	}
}
