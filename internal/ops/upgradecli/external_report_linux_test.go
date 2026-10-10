//go:build linux

package upgradecli

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/ops/process"
	"github.com/alphabravocompany/astronomer-go/internal/ops/upgrade"
)

// This manually sealed synthetic inventory qualifies wire assurance only;
// it does not establish external backup, live schema, successful Run or restore.
func TestExternalSanitizedReportNeverClaimsVerifiedDatabaseCopy(t *testing.T) {
	release := helmReleaseFixture(t)
	commands := release.fence.commands
	backup := &ReleaseBackup{external: &externalBackupBinding{}, recovery: &HelmRecovery{observation: &HelmStateObservation{release: release}}, policy: ArtifactPolicy{MaximumBytes: 1024}, captures: map[string]process.ArtifactCapture{}}
	for _, name := range releaseBackupNames[:6] {
		capture := artifactFixture(t, commands.options.CaptureDirectory, name, []byte("synthetic retained "+name))
		backup.captures[name] = *capture.StdoutArtifact
	}
	var err error
	backup.inventory, err = writeBackupBytes(context.Background(), commands, "BACKUP_SHA256SUMS", backup.checksumBytes(), 16<<10)
	if err != nil {
		t.Fatal(err)
	}
	backup.complete = true
	invocation := &Invocation{plan: release.plan, driver: &ReleaseDriver{plan: release.plan, release: release, backup: backup}, completed: true, lastResult: upgrade.Result{Mode: release.plan.Mode, Result: "passed", ExitCode: 0}}
	before := invocation.lastResult
	raw, err := invocation.SanitizedReport(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]json.RawMessage
	var report SanitizedReportV1
	if json.Unmarshal(raw, &wire) != nil || len(wire) != 12 || json.Unmarshal(raw, &report) != nil || report.DatabaseRestoreMode != "operator_confirmed_external" || report.Result != "passed" || invocation.lastResult != before {
		t.Fatal("external confirmation gained restore credit or changed Run outcome")
	}
	if _, exists := backup.captures["astronomer.pgcustom"]; exists {
		t.Fatal("fixture accidentally retained a database copy")
	}
}
