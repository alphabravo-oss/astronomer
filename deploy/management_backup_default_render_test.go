package deploy

import (
	"path/filepath"
	"strings"
	"testing"
)

// Backup + restore drill default ON (P1 item "backup-default-on"), but inert
// until S3 credentials are wired. The chart gates the CronJob render on
// managementBackup.s3.credentialsSecretRef.name so an unconfigured install
// (the default values.yaml render) produces no backup workload instead of
// hard-failing. Wiring the creds in turns both CronJobs on with no other flags.
func TestManagementBackup_DefaultOnButInertWithoutCredentials(t *testing.T) {
	// Default values.yaml only: enabled=true but no bucket/creds. Must render
	// cleanly with NO backup or restore-drill CronJob.
	out := helmTemplate(t)
	if strings.Contains(out, "name: astronomer-management-backup") {
		t.Fatalf("backup CronJob rendered without credentials wired:\n%s", out)
	}
	if strings.Contains(out, "name: astronomer-restore-drill") {
		t.Fatalf("restore-drill CronJob rendered without credentials wired:\n%s", out)
	}

	// Wiring only the credentials secret (no enabled flags, proving they
	// default on) must render BOTH CronJobs.
	withCreds := helmTemplate(t,
		"managementBackup.s3.bucket=astronomer-backups",
		"managementBackup.s3.credentialsSecretRef.name=astronomer-backup-creds",
	)
	if !strings.Contains(withCreds, "name: astronomer-management-backup") {
		t.Fatalf("backup CronJob did not render with credentials wired:\n%s", withCreds)
	}
	if !strings.Contains(withCreds, "name: astronomer-restore-drill") {
		t.Fatalf("restore-drill CronJob did not render with credentials wired:\n%s", withCreds)
	}
}

// The render gate requires an s3.bucket in addition to credentials. Wiring
// credentials but forgetting the bucket (a fresh-install footgun, since both
// default empty) must NOT render a CronJob that would only fail at runtime.
func TestManagementBackup_NoBucketRendersNoCronJob(t *testing.T) {
	// Credentials wired, bucket left empty: neither CronJob may render, and
	// the NOTES must warn that the backup is unconfigured.
	out := helmTemplate(t,
		"managementBackup.s3.credentialsSecretRef.name=astronomer-backup-creds",
	)
	if strings.Contains(out, "name: astronomer-management-backup") {
		t.Fatalf("backup CronJob rendered with no bucket set:\n%s", out)
	}
	if strings.Contains(out, "name: astronomer-restore-drill") {
		t.Fatalf("restore-drill CronJob rendered with no bucket set:\n%s", out)
	}

	// Symmetric footgun: bucket set but credentials missing also renders
	// nothing.
	noCreds := helmTemplate(t,
		"managementBackup.s3.bucket=astronomer-backups",
	)
	if strings.Contains(noCreds, "name: astronomer-management-backup") {
		t.Fatalf("backup CronJob rendered with no credentials set:\n%s", noCreds)
	}
	if strings.Contains(noCreds, "name: astronomer-restore-drill") {
		t.Fatalf("restore-drill CronJob rendered with no credentials set:\n%s", noCreds)
	}
}

// Dev (k3d) opts out explicitly, so even with the upstream default flipped on
// the local smoke-test render carries no backup workload.
func TestManagementBackup_K3dOptsOut(t *testing.T) {
	out := helmTemplateWithValueFiles(t, []string{filepath.Join("chart", "values-k3d.yaml")})
	if strings.Contains(out, "name: astronomer-management-backup") {
		t.Fatalf("k3d render unexpectedly contains backup CronJob:\n%s", out)
	}
	if strings.Contains(out, "name: astronomer-restore-drill") {
		t.Fatalf("k3d render unexpectedly contains restore-drill CronJob:\n%s", out)
	}
}
