//go:build linux

package upgradecli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/ops/process"
)

// Inventory tests exercise private retained bytes only. They do not claim a
// synthetic dump is readable or replace the actual source/temporary/OCI gates.
func retainedBackupInventoryFixture(t *testing.T) *ReleaseBackup {
	t.Helper()
	commands, _ := commandFixture(t)
	commands.artifactCapacity = func(string, int64) error { return nil }
	b := &ReleaseBackup{source: &SourceDatabaseBackup{fence: &OwnershipFence{commands: commands}}, recovery: &HelmRecovery{policy: ArtifactPolicy{MaximumBytes: 1024}, observation: &HelmStateObservation{release: &HelmRelease{fence: &OwnershipFence{commands: commands}}}}, policy: ArtifactPolicy{MaximumBytes: 1024}, captures: make(map[string]process.ArtifactCapture)}
	for _, name := range releaseBackupNames {
		artifact := artifactFixture(t, commands.options.CaptureDirectory, name, []byte("retained synthetic "+name))
		b.captures[name] = *artifact.StdoutArtifact
	}
	capture, err := writeBackupBytes(context.Background(), commands, "BACKUP_SHA256SUMS", b.checksumBytes(), 16<<10)
	if err != nil {
		t.Fatal(err)
	}
	b.inventory = capture
	return b
}
func TestCompleteBackupInventoryRetainsAllLegacyExactNamesAndHashes(t *testing.T) {
	b := retainedBackupInventoryFixture(t)
	if err := b.verifyInventory(context.Background()); err != nil {
		t.Fatal("complete retained file inventory rejected", err)
	}
	raw, err := os.ReadFile(b.inventory.Path)
	if err != nil {
		t.Fatal(err)
	}
	rows := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	if len(rows) != 8 {
		t.Fatal("complete legacy backup file set lost")
	}
	for i, name := range releaseBackupNames {
		expected := strings.TrimPrefix(b.captures[name].SHA256, "sha256:") + "  " + name
		if rows[i] != expected {
			t.Fatal("canonical checksum row changed")
		}
	}
	if _, err := b.ChecksumDigest(); err == nil {
		t.Fatal("private files alone acknowledged complete readable backup")
	}
	if err := b.VerifyBeforeMutation(context.Background()); err == nil {
		t.Fatal("inventory bypassed actual chart/restore/release proofs")
	}
}
func TestCompleteBackupInventoryRejectsTamperedAliasedMissingOrExtraFiles(t *testing.T) {
	for _, mode := range []string{"changed", "aliased", "missing", "extra", "inventory", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			b := retainedBackupInventoryFixture(t)
			ctx := context.Background()
			c := b.captures["astronomer.pgcustom"]
			switch mode {
			case "changed":
				if err := os.WriteFile(c.Path, []byte("changed bytes"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "aliased":
				if err := os.Rename(c.Path, c.Path+".original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(c.Path+".original", c.Path); err != nil {
					t.Fatal(err)
				}
			case "missing":
				delete(b.captures, "history.json")
			case "extra":
				b.captures["unowned.yaml"] = c
			case "inventory":
				if err := os.WriteFile(b.inventory.Path, append(b.checksumBytes(), []byte("injected\n")...), 0o600); err != nil {
					t.Fatal(err)
				}
			case "canceled":
				var stop context.CancelFunc
				ctx, stop = context.WithCancel(ctx)
				stop()
			}
			if err := b.verifyInventory(ctx); err == nil {
				t.Fatal("changed or incomplete recovery inventory admitted")
			}
			if _, err := os.Stat(b.inventory.Path); err != nil {
				t.Fatal("failed inventory verification removed recovery")
			}
		})
	}
}
func TestCompleteBackupGeneratedPrivateBytesRejectUnsafeReuseAndCancellation(t *testing.T) {
	commands, _ := commandFixture(t)
	commands.artifactCapacity = func(string, int64) error { return nil }
	capture, err := writeBackupBytes(context.Background(), commands, "retained.txt", []byte("private bytes"), 1024)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writeBackupBytes(context.Background(), commands, "retained.txt", []byte("overwrite"), 1024); err == nil {
		t.Fatal("existing private recovery overwritten")
	}
	actual, err := os.ReadFile(capture.Path)
	if err != nil || !bytes.Equal(actual, []byte("private bytes")) {
		t.Fatal("prior recovery bytes not retained", err)
	}
	ctx, stop := context.WithCancel(context.Background())
	stop()
	if _, err := writeBackupBytes(ctx, commands, "canceled.txt", []byte("x"), 1024); !errors.Is(err, context.Canceled) {
		t.Fatal("private writer cancellation lost", err)
	}
	for _, name := range []string{"../outside", ".", "bad\nname"} {
		if _, err := writeBackupBytes(context.Background(), commands, name, []byte("x"), 1024); err == nil {
			t.Fatal("unowned filename admitted")
		}
	}
	if _, err := writeBackupBytes(context.Background(), commands, "oversized.txt", []byte("more"), 1); err == nil {
		t.Fatal("explicit file cap ignored")
	}
	if err := os.Chmod(commands.options.CaptureDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := writeBackupBytes(context.Background(), commands, "public.txt", []byte("private"), 1024); err == nil {
		t.Fatal("private bytes written through public directory")
	}
	if _, err := os.Lstat(filepath.Join(commands.options.CaptureDirectory, "public.txt")); !os.IsNotExist(err) {
		t.Fatal("unsafe directory acquired output")
	}
}
func TestCompleteBackupConstructorRejectsUnrelatedStageOwnersBeforeCapture(t *testing.T) {
	if _, err := NewReleaseBackup(nil, nil, nil, ArtifactPolicy{MaximumBytes: 1024}); err == nil {
		t.Fatal("missing concrete stage composition admitted")
	}
	source, _ := databaseBackupFixture(t)
	observation, _ := releaseStateFixture(t)
	recovery, err := NewHelmRecovery(observation, ArtifactPolicy{MaximumBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	chart := &TargetChartContent{metadata: &SignedReleaseMetadata{target: observation.release.plan.Target}}
	if _, err := NewReleaseBackup(chart, recovery, source, source.options.ArtifactPolicy); err == nil {
		t.Fatal("unrelated release/database owners combined")
	}
	source.fence = recovery.observation.release.fence
	if _, err := NewReleaseBackup(chart, recovery, source, ArtifactPolicy{MaximumBytes: source.options.ArtifactPolicy.MaximumBytes + 1}); err == nil {
		t.Fatal("unbound database byte policy admitted")
	}
	source.options.ArtifactPolicy.MaximumBytes = 1024
	b, err := NewReleaseBackup(chart, recovery, source, ArtifactPolicy{MaximumBytes: 1024})
	if err != nil {
		t.Fatal("same stage owner source constructor failed", err)
	}
	if err := b.Capture(nil); err == nil {
		t.Fatal("nil context started backup")
	}
	ctx, stop := context.WithCancel(context.Background())
	stop()
	if err := b.Capture(ctx); !errors.Is(err, context.Canceled) || b.attempted {
		t.Fatal("pre-canceled context attempted backup", err)
	}
}
