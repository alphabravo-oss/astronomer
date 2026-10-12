//go:build linux

package upgradecli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/ops/upgrade"
)

// These are public wire/private retained-byte tests. The manually acknowledged
// synthetic fixture does not establish a readable dump, real Run success,
// signature/provenance acceptance, official CLI or cluster/restore qualification.
func sanitizedReportFixture(t *testing.T) *Invocation {
	t.Helper()
	release := helmReleaseFixture(t)
	backup := retainedBackupInventoryFixture(t)
	backup.complete = true
	return &Invocation{plan: release.plan, driver: &ReleaseDriver{plan: release.plan, release: release, backup: backup}, completed: true, lastResult: upgrade.Result{ExitCode: 0, Result: "passed", Mode: release.plan.Mode}}
}

func TestSanitizedReportRetainsExactLegacyV1ShapeAndOnlySelectedPublicFields(t *testing.T) {
	for _, outcome := range []struct {
		code   int
		result string
		public string
	}{{0, "passed", "passed"}, {1, "failed", "failed"}, {1, "outcome_unknown", "failed"}, {1, "resume_assessment_required", "failed"}, {1, "cleanup_failed", "failed"}} {
		t.Run(outcome.result, func(t *testing.T) {
			invocation := sanitizedReportFixture(t)
			invocation.lastResult.ExitCode, invocation.lastResult.Result = outcome.code, outcome.result
			invocation.lastResult.RollbackAttempted = outcome.code != 0
			start := time.Now().UTC()
			raw, err := invocation.SanitizedReport(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			var actual map[string]json.RawMessage
			if json.Unmarshal(raw, &actual) != nil || len(actual) != 12 {
				t.Fatal("legacy public field set changed")
			}
			expected := []string{"schema_version", "release", "namespace", "target_version", "mode", "result", "api_identity_sha256", "release_manifest_sha256", "backup_manifest_sha256", "database_restore_mode", "rollback_attempted", "completed_at"}
			for _, key := range expected {
				if _, ok := actual[key]; !ok {
					t.Fatal("legacy public field missing", key)
				}
			}
			var report SanitizedReportV1
			if json.Unmarshal(raw, &report) != nil || report.SchemaVersion != 1 || report.Result != outcome.public || report.Mode != invocation.plan.Mode || report.DatabaseRestoreMode != "verified_copy" || report.RollbackAttempted != (outcome.code != 0) {
				t.Fatal("actual returned outcome projection differs")
			}
			endpoint := sha256.Sum256([]byte(invocation.plan.Identity.APIServer))
			if report.APIIdentitySHA256 != "sha256:"+hex.EncodeToString(endpoint[:]) || report.ReleaseManifestSHA256 != "sha256:"+hex.EncodeToString(invocation.driver.release.manifestHash[:]) || report.BackupManifestSHA256 != invocation.driver.backup.inventory.SHA256 {
				t.Fatal("exact captured report hashes differ")
			}
			completed, err := time.Parse(time.RFC3339, report.CompletedAt)
			if err != nil || completed.Before(start.Truncate(time.Second)) || completed.After(time.Now().UTC()) {
				t.Fatal("actual UTC completion time not captured")
			}
			for _, private := range []string{invocation.plan.Identity.APIServer, invocation.plan.Identity.Context, invocation.plan.Identity.NamespaceUID, invocation.plan.Identity.ReleaseUID, invocation.driver.release.manifestFile, invocation.driver.backup.inventory.Path, "retained synthetic ", "synthetic manifest bytes"} {
				if private != "" && strings.Contains(string(raw), private) {
					t.Fatal("private field leaked into fixed public report")
				}
			}
		})
	}
}

func TestSanitizedReportCannotPublishUnfinishedChangedAliasedOrCanceledInputs(t *testing.T) {
	for _, mode := range []string{"unfinished", "closed", "unsealed", "changed-manifest", "recreated-manifest", "changed-backup", "canceled", "replacement"} {
		t.Run(mode, func(t *testing.T) {
			invocation := sanitizedReportFixture(t)
			ctx := context.Background()
			switch mode {
			case "unfinished":
				invocation.completed = false
			case "closed":
				invocation.closed = true
			case "unsealed":
				invocation.driver.backup.complete = false
			case "changed-manifest":
				if err := os.WriteFile(invocation.driver.release.manifestFile, []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			case "recreated-manifest":
				p := invocation.driver.release.manifestFile
				raw, err := os.ReadFile(p)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(p, p+".original"); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, raw, 0600); err != nil {
					t.Fatal(err)
				}
			case "changed-backup":
				if err := os.WriteFile(invocation.driver.backup.inventory.Path, []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "replacement":
				invocation.plan.Replace = &upgrade.DisposableLease{}
			}
			raw, err := invocation.SanitizedReport(ctx)
			if err == nil || raw != nil {
				t.Fatal("incomplete or changed public report published")
			}
			if mode == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatal("report cancellation identity lost")
			}
			if _, err := os.Stat(invocation.driver.backup.inventory.Path); err != nil {
				t.Fatal("failure removed private recovery")
			}
		})
	}
}

func TestInvocationActualVerifyFailureCannotInventPublicBackupReport(t *testing.T) {
	invocation, _, _ := invocationRunFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result := invocation.Run(ctx)
	if result.ExitCode != 95 || !invocation.completed || invocation.lastResult != result {
		t.Fatal("actual returned Run outcome was not retained", result)
	}
	if raw, err := invocation.SanitizedReport(ctx); err == nil || raw != nil {
		t.Fatal("failed actual Verify invented readable backup/public report")
	}
}
