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
		"--set", "secrets.secretKeyKey=SIGNING_KEY",
		"--set", "secrets.encryptionKeyKey=FERNET_KEY",
		"--set", "secrets.secretKey=INLINE-SIGNING-CANARY",
		"--set", "secrets.encryptionKey=INLINE-ENCRYPTION-CANARY",
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
		"--set", "dex.runtimeSecretName=dex-runtime",
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
		"redis-credentials", "url", "dex-runtime",
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
	for _, canary := range []string{"INLINE-SIGNING-CANARY", "INLINE-ENCRYPTION-CANARY", "INLINE-BOOTSTRAP-CANARY"} {
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
		"--set", "secrets.secretKeyKey=SIGNING_KEY",
		"--set", "secrets.encryptionKeyKey=FERNET_KEY",
		"--set", "bootstrap.existingSecret=bootstrap-credentials",
		"--set", "bootstrap.email=admin@example.com",
		"--set", "dex.runtimeSecretName=dex-runtime",
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
		"dex-runtime":           2,
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
	for _, canonicalItem := range []string{
		`find /var/run/astronomer-keys -mindepth 1 -maxdepth 1 -exec rm -rf -- {} +`,
		`cp -aL /var/run/astronomer-keys-source/. /var/run/astronomer-keys/`,
		`/var/run/astronomer-keys-source/SIGNING_KEY" /var/run/astronomer-keys/SECRET_KEY`,
		`/var/run/astronomer-keys-source/FERNET_KEY" /var/run/astronomer-keys/ASTRONOMER_ENCRYPTION_KEY`,
	} {
		if !strings.Contains(rendered, canonicalItem) {
			t.Fatalf("backup key projection missing canonical item %q", canonicalItem)
		}
	}
	cleanup := strings.Index(rendered, `find /var/run/astronomer-keys -mindepth 1`)
	copyAll := strings.Index(rendered, `cp -aL /var/run/astronomer-keys-source/.`)
	if cleanup < 0 || copyAll < 0 || cleanup > copyAll {
		t.Fatal("backup key staging is not cleaned before the complete Secret copy")
	}
	// A Secret volume without items projects every key. The copy-all staging
	// step above therefore retains an unrelated synthetic core key alongside
	// the two canonical aliases without exposing any value in test output.
	sourceStart := strings.Index(rendered, "- name: encryption-keys-source\n              secret:")
	if sourceStart < 0 {
		t.Fatal("backup has no complete source Secret projection")
	}
	sourceEnd := strings.Index(rendered[sourceStart:], "- name: wrapping-passphrase")
	if sourceEnd < 0 {
		t.Fatal("backup source Secret projection is malformed")
	}
	if strings.Contains(rendered[sourceStart:sourceStart+sourceEnd], "items:") {
		t.Fatal("backup source projection filters unrelated core keys")
	}
	overrideArgs := append(append([]string{}, args...), "--set", "managementBackup.encryptionKeyBackup.secretName=independent-key-bundle")
	overrideCommand := exec.Command("helm", overrideArgs...)
	var overrideStdout bytes.Buffer
	overrideCommand.Stdout = &overrideStdout
	overrideCommand.Stderr = &bytes.Buffer{}
	if err := overrideCommand.Run(); err != nil {
		t.Fatalf("independent backup bundle render failed: %v", err)
	}
	overrideRendered := overrideStdout.String()
	for _, want := range []string{
		`secretName: "independent-key-bundle"`,
		`cp -aL /var/run/astronomer-keys-source/. /var/run/astronomer-keys/`,
		`cp -L /var/run/astronomer-keys-source/SECRET_KEY /var/run/astronomer-keys/SECRET_KEY`,
		`cp -L /var/run/astronomer-keys-source/ASTRONOMER_ENCRYPTION_KEY /var/run/astronomer-keys/ASTRONOMER_ENCRYPTION_KEY`,
	} {
		if !strings.Contains(overrideRendered, want) {
			t.Fatalf("independent backup bundle missing %q", want)
		}
	}
	for _, forbidden := range []string{"astronomer-keys-source/SIGNING_KEY", "astronomer-keys-source/FERNET_KEY"} {
		if strings.Contains(overrideRendered, forbidden) {
			t.Fatalf("independent canonical bundle incorrectly used runtime key name %q", forbidden)
		}
	}
}
