//go:build linux

package upgradecli

import (
	"context"
	"errors"
	"github.com/alphabravocompany/astronomer-go/internal/ops/process"
	"github.com/alphabravocompany/astronomer-go/internal/ops/upgrade"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These fixtures qualify real private ownership/reconciliation and one owned
// mutation-before-ack child; synthetic catalog rows are not PostgreSQL DDL proof.
func replacementFixture(t *testing.T) (*DisposableReplacement, *string) {
	t.Helper()
	fence, lease := fenceFixture(t)
	lease.RestoreDatabaseName = "astronomer_upgrade_verify_fixture"
	source, err := NewSourceDatabaseBackup(fence, DatabaseBackupOptions{Database: lease.DatabaseName, RestoreDatabase: lease.RestoreDatabaseName, DatabasePodUID: lease.DatabasePodUID, Owner: lease.Owner, ExpectedMajor: 18, MinimumSchema: 66, TargetSchema: 78, ArtifactPolicy: ArtifactPolicy{MaximumBytes: 1024}})
	if err != nil {
		t.Fatal(err)
	}
	backup := &ReleaseBackup{source: source, recovery: &HelmRecovery{observation: &HelmStateObservation{}}, temporary: &TemporaryDatabase{marker: "astronomer-upgrade:" + lease.Owner, oid: 700}}
	v, err := NewDisposableReplacement(backup, lease)
	if err != nil {
		t.Fatal(err)
	}
	v.liveOID, v.restoreOID, v.captured = 600, 700, true
	mode := "old"
	original := fence.execute
	fence.execute = func(ctx context.Context, tool Tool, args []string) ([]byte, error) {
		if strings.Contains(strings.Join(args, "\n"), replacementCatalogScript) {
			switch mode {
			case "old":
				return []byte(lease.DatabaseName + "|600|t|t\n" + lease.RestoreDatabaseName + "|700|t|t\n"), nil
			case "renamed":
				marker := args[len(args)-2] == v.backup.temporary.marker
				if marker {
					return []byte(lease.DatabaseName + "|700|t|t\n"), nil
				}
				return []byte(lease.DatabaseName + "|700|t|f\n"), nil
			case "partial":
				return []byte(lease.RestoreDatabaseName + "|700|t|t\n"), nil
			case "recreated":
				return []byte(lease.DatabaseName + "|701|t|t\n"), nil
			case "foreign":
				return []byte(lease.DatabaseName + "|700|f|t\n"), nil
			case "unmarked":
				return []byte(lease.DatabaseName + "|700|t|f\n"), nil
			}
			return nil, errors.New("unknown catalog fixture")
		}
		return original(ctx, tool, args)
	}
	return v, &mode
}
func TestDisposableReplacementAssessmentPreservesExactCapturedOIDAndMarker(t *testing.T) {
	for _, mode := range []string{"old", "renamed", "partial", "recreated", "foreign", "unmarked"} {
		t.Run(mode, func(t *testing.T) {
			v, selected := replacementFixture(t)
			*selected = mode
			outcome, err := v.Reconcile(context.Background())
			if mode == "old" {
				if err != nil || !outcome.Known || outcome.Replaced {
					t.Fatal("unchanged original identity misclassified")
				}
			} else if mode == "renamed" {
				if err != nil || !outcome.Known || !outcome.Replaced || !v.replaced {
					t.Fatal("exact retained restored OID not assessed")
				}
			} else if err == nil || outcome.Known || v.replaced {
				t.Fatal("partial/recreated/foreign identity granted mutation acknowledgement")
			}
		})
	}
}
func TestDisposableReplacementReconcileAssessesJoinedMutationBeforeAcknowledgement(t *testing.T) {
	if os.Getenv("OPS_REPLACEMENT_CHILD") == "1" {
		if err := os.WriteFile(os.Getenv("OPS_REPLACEMENT_MARKER"), []byte("renamed"), 0600); err != nil {
			os.Exit(2)
		}
		os.Exit(43)
	}
	v, mode := replacementFixture(t)
	directory := v.backup.source.fence.commands.options.CaptureDirectory
	marker := filepath.Join(directory, "synthetic-child-mutation")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	spec := process.Spec{Executable: executable, Args: []string{"-test.run=^TestDisposableReplacementReconcileAssessesJoinedMutationBeforeAcknowledgement$"}, Environment: []process.Environment{{Name: "OPS_REPLACEMENT_CHILD", Literal: "1"}, {Name: "OPS_REPLACEMENT_MARKER", Literal: marker}}, Directory: directory, CaptureDirectory: directory, ID: "replacement_fixture", Timeout: 2 * time.Second, TerminationGrace: 100 * time.Millisecond, MaximumCaptureBytes: 1024}
	v.sqlAttempted = true
	result, err := process.Run(context.Background(), spec)
	if err == nil || result.ExitCode != 43 || !result.LeaderReaped {
		t.Fatal("owned failed mutation child was not retained/joined")
	}
	raw, err := os.ReadFile(marker)
	if err != nil || string(raw) != "renamed" {
		t.Fatal("actual mutation-before-ack marker missing")
	}
	*mode = string(raw)
	outcome, err := v.Reconcile(context.Background())
	if err != nil || !outcome.Known || !outcome.Replaced {
		t.Fatal("failed child mistaken for proof of no mutation", outcome, err)
	}
	if _, err := os.Lstat(marker); err != nil {
		t.Fatal("recovery mutation observation removed")
	}
}
func TestDisposableReplacementValueBindingRejectsCallerMutationAndCanceledFence(t *testing.T) {
	v, _ := replacementFixture(t)
	original := v.lease
	caller := original
	caller.DatabaseName = "foreign"
	if v.lease != original {
		t.Fatal("caller value mutation changed owner")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	outcome, err := v.Reconcile(ctx)
	if !errors.Is(err, context.Canceled) || outcome.Known {
		t.Fatal("expired observation acknowledged replacement")
	}
	a := upgrade.Plan{Replace: &original}
	b := upgrade.Plan{Replace: &caller}
	if sameReleasePlan(a, b) {
		t.Fatal("different replacement identity admitted")
	}
	b.Replace = &original
	if !sameReleasePlan(a, b) {
		t.Fatal("equal deep-copied replacement plan rejected")
	}
}
