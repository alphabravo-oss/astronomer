// Package deploy — chart-render coverage for the P0 chart DR items:
//
//	F1  server↔migrate image-tag skew guard
//	F8  bootstrap-password GitOps stability (existingSecret escape hatch)
//	F4  encryption-key backup + restore-drill decrypt proof
//
// All tests shell out to `helm` and auto-skip when it isn't on PATH.
package deploy

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func drProdValues(t *testing.T) string {
	t.Helper()
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	return filepath.Join(filepath.Dir(here), "chart", "values-production.yaml")
}

// ── F1: server↔migrate image-tag skew guard ────────────────────────────────

// A skewed pair (server tag != migrate tag) can leave the DB below the server
// binary's schema floor and 503 the plane permanently. The chart must refuse
// the render — not silently produce a mismatched Deployment.
func TestF1_MismatchedServerMigrateTagsFailsRender(t *testing.T) {
	errOut := helmTemplateExpectError(t, nil,
		"image.server.tag=commit-A",
		"image.migrate.tag=commit-B",
	)
	if !strings.Contains(errOut, "image-skew guard (F1) failed") {
		t.Fatalf("mismatched server/migrate tags did not trip the F1 guard:\n%s", errOut)
	}
}

// Matching tags (the built-from-one-commit norm) render cleanly.
func TestF1_MatchingTagsRender(t *testing.T) {
	out := helmTemplate(t, "image.server.tag=v9", "image.migrate.tag=v9")
	if !strings.Contains(out, "astronomer-go-server:v9") {
		t.Fatalf("expected server image at tag v9 in render:\n%s", out)
	}
}

// The escape hatch (allowSchemaSkew=true) lets a deliberate hotfix through and
// swaps the hard failure for a NOTES.txt warning.
func TestF1_AllowSchemaSkewOverride(t *testing.T) {
	out := helmTemplate(t,
		"image.server.tag=commit-A",
		"image.migrate.tag=commit-B",
		"image.allowSchemaSkew=true",
	)
	// Render succeeds and the server Deployment still names the override tag.
	if !strings.Contains(out, "astronomer-go-server:commit-A") {
		t.Fatalf("allowSchemaSkew override should render the server image:\n%s", out)
	}
}

// ── F8: bootstrap-password GitOps stability ─────────────────────────────────

// The default (dev) render keeps the chart-managed bootstrap Secret so the
// local install UX is unchanged.
func TestF8_DefaultRendersChartBootstrapSecret(t *testing.T) {
	out := helmTemplate(t)
	if !strings.Contains(out, "name: astronomer-bootstrap") {
		t.Fatalf("default render should manage the <release>-bootstrap Secret:\n%s", out)
	}
}

// bootstrap.existingSecret is the GitOps-safe escape hatch: the chart manages
// NO bootstrap Secret and the server wires ASTRONOMER_BOOTSTRAP_PASSWORD to the
// operator-provided Secret.
func TestF8_ExistingSecretSuppressesChartSecretAndRewiresServer(t *testing.T) {
	out := helmTemplate(t, "bootstrap.existingSecret=my-bootstrap")
	if strings.Contains(out, "name: astronomer-bootstrap\n") {
		t.Fatalf("existingSecret set — chart must not render its own bootstrap Secret:\n%s", out)
	}
	// The server env must reference the operator's Secret name.
	if !strings.Contains(out, "name: my-bootstrap") {
		t.Fatalf("server ASTRONOMER_BOOTSTRAP_PASSWORD not rewired to existingSecret:\n%s", out)
	}
}

// In production an empty bootstrap.password (with no existingSecret) re-rolls
// randAlphaNum on every GitOps render → the chart must refuse.
func TestF8_ProductionWithoutBootstrapPasswordFails(t *testing.T) {
	sets := make([]string, 0, len(productionWiringSets)+2)
	for _, set := range productionWiringSets {
		if !strings.HasPrefix(set, "bootstrap.password=") {
			sets = append(sets, set)
		}
	}
	sets = append(sets,
		"managementBackup.s3.bucket=astronomer-backups",
		"managementBackup.s3.credentialsSecretRef.name=astronomer-backup-creds",
	)
	errOut := helmTemplateExpectError(t, []string{drProdValues(t)}, sets...)
	if !strings.Contains(errOut, "bootstrap.password or bootstrap.existingSecret must be set") {
		t.Fatalf("production render without a pinned bootstrap password did not fail on F8:\n%s", errOut)
	}
}

// ── F4: encryption-key backup + restore-drill decrypt proof ─────────────────

// Wiring only the backup S3 target is intentionally inert: plaintext backups
// are never an accepted fallback.
func TestF4_KeyBackupInertWithoutWrappingSecret(t *testing.T) {
	out := helmTemplate(t,
		"managementBackup.s3.bucket=astronomer-backups",
		"managementBackup.s3.credentialsSecretRef.name=astronomer-backup-creds",
	)
	docs := parseRenderedDocs(t, out)
	if renderedDocExists(docs, "CronJob", "astronomer-management-backup") || renderedDocExists(docs, "CronJob", "astronomer-restore-drill") {
		t.Fatalf("backup/restore workloads must stay inert until authenticated encryption is wired:\n%s", out)
	}
}

// With the wrapping secret wired the backup mounts the key Secret + passphrase
// and turns on the wrapped-upload path.
func TestF4_KeyBackupRendersWhenWrappingSecretWired(t *testing.T) {
	out := helmTemplate(t,
		"managementBackup.s3.bucket=astronomer-backups",
		"managementBackup.s3.credentialsSecretRef.name=astronomer-backup-creds",
		"managementBackup.encryption.sourceIdentity=test-installation",
		"managementBackup.encryption.wrappingSecretRef.name=astronomer-key-wrap",
	)
	for _, want := range []string{
		"name: encryption-keys",
		"name: wrapping-passphrase",
		`secretName: "astronomer-key-wrap"`,
		`secretName: "astronomer-secrets"`, // default key Secret = <release>-secrets
		"dr-crypto encrypt",
		"--purpose database-dump",
		"--purpose key-bundle",
		"dr-crypto manifest-create",
		"get-object-lock-configuration",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("key-backup render missing %q:\n%s", want, out)
		}
	}
}

// The restore drill also stays inert until the manifest-authentication secret
// and stable source identity are wired.
func TestF4_DrillDecryptCheckInertWithoutWrappingSecret(t *testing.T) {
	out := helmTemplate(t,
		"managementBackup.s3.bucket=astronomer-backups",
		"managementBackup.s3.credentialsSecretRef.name=astronomer-backup-creds",
	)
	if renderedDocExists(parseRenderedDocs(t, out), "CronJob", "astronomer-restore-drill") {
		t.Fatalf("restore drill must stay inert until authenticated encryption is wired:\n%s", out)
	}
}

// With the drill wrapping secret wired the decrypt-proof path renders: env
// toggle, passphrase mount, unwrap, and the Fernet HMAC verifier invocation.
func TestF4_DrillDecryptCheckRendersWhenWired(t *testing.T) {
	out := helmTemplate(t,
		"managementBackup.s3.bucket=astronomer-backups",
		"managementBackup.s3.credentialsSecretRef.name=astronomer-backup-creds",
		"managementBackup.encryption.sourceIdentity=test-installation",
		"managementBackup.encryption.wrappingSecretRef.name=astronomer-key-wrap",
	)
	for _, want := range []string{
		"- name: DECRYPT_CHECK_ENABLED",
		"name: wrapping-passphrase",
		"dr-crypto manifest-verify",
		"dr-crypto decrypt",
		"dr-crypto key-bundle-extract",
		"base64 -d | python3 -",
		"did NOT verify under the backed-up key",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("drill decrypt-check render missing %q:\n%s", want, out)
		}
	}
}
