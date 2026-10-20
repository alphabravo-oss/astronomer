//go:build linux

package upgradecli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/alphabravocompany/astronomer-go/pkg/safeconv"

	"github.com/alphabravocompany/astronomer-go/internal/ops/process"
)

var releaseBackupNames = []string{"values-all.yaml", "values-user.yaml", "manifest-before.yaml", "history.json", "release-secrets.yaml", "target-release-compatibility.json", "astronomer.pgcustom", "astronomer.pgcustom.list"}

// ReleaseBackup is the concrete bundled-database Backup stage. It composes the
// initially owned old revision's five legacy captures, exact target-chart
// compatibility, a custom-format dump, actual archive-list/temporary restore and
// same clean original schema, then seals the complete legacy checksum inventory.
// It grants no signer/attestation/image/full-Verify or replacement authority.
// No method deletes private backup material. External database confirmations
// are deliberately not accepted as equivalent to this actual restore proof.
type ReleaseBackup struct {
	chart       *TargetChartContent
	recovery    *HelmRecovery
	source      *SourceDatabaseBackup
	external    *externalBackupBinding
	policy      ArtifactPolicy
	mu          sync.Mutex
	attempted   bool
	complete    bool
	captures    map[string]process.ArtifactCapture
	inventory   process.ArtifactCapture
	temporary   *TemporaryDatabase
	cnpgRestore *CloudNativePGRestore
}

func NewReleaseBackup(chart *TargetChartContent, recovery *HelmRecovery, source *SourceDatabaseBackup, policy ArtifactPolicy) (*ReleaseBackup, error) {
	if chart == nil || chart.metadata == nil || recovery == nil || recovery.observation == nil || recovery.observation.release == nil || source == nil || source.fence == nil || source.fence != recovery.observation.release.fence || chart.metadata.target != recovery.observation.release.plan.Target || policy.MaximumBytes <= 0 || policy.MaximumBytes != source.options.ArtifactPolicy.MaximumBytes {
		return nil, errors.New("initially co-owned target chart, release and database backup required")
	}
	return &ReleaseBackup{chart: chart, recovery: recovery, source: source, policy: policy, captures: make(map[string]process.ArtifactCapture)}, nil
}

// Capture is not replayable after the first attempt. Unknown/failed temporary
// creation and every earlier original/copy remain retained for assessment. Only
// success of all concrete gates can mark this complete; no caller bool or raw
// schema number can acknowledge the backup stage.
func (b *ReleaseBackup) Capture(ctx context.Context) error {
	if b == nil || ctx == nil {
		return errors.New("explicit complete backup context and binding required")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.attempted {
		return errors.New("backup attempt retained; reconcile rather than replay")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	b.attempted = true
	if b.external != nil {
		return b.captureExternal(ctx)
	}
	bounds, compatibility, err := b.chart.Verify(ctx)
	if err != nil {
		return err
	}
	if bounds.Minimum != b.source.options.MinimumSchema || bounds.Target != b.source.options.TargetSchema || !chartSupportsDatabase(bounds, b.source.options.ExpectedMajor) {
		return errors.New("database backup options differ from exact verified target chart")
	}
	if err := b.recovery.Capture(ctx); err != nil {
		return err
	}
	b.recovery.mu.Lock()
	for name, capture := range b.recovery.artifacts {
		b.captures[name] = capture
	}
	b.recovery.mu.Unlock()
	dump, _, err := b.source.Dump(ctx)
	if err != nil {
		return err
	}
	var temporary *TemporaryDatabase
	if dump.cnpg != nil {
		restore, err := NewCloudNativePGRestore(ctx, b.chart, dump.cnpg, b.source.cnpgRestore)
		if err != nil {
			return err
		}
		b.cnpgRestore = restore
		if err := restore.Verify(ctx); err != nil {
			return err
		}
		temporary = restore.temporary
	} else {
		var err error
		temporary, err = NewTemporaryDatabase(dump)
		if err != nil {
			return err
		}
		b.temporary = temporary
		if err := temporary.Verify(ctx); err != nil {
			return err
		}
	}
	b.temporary = temporary
	if !temporary.restored || len(temporary.archiveList) == 0 {
		return errors.New("actual readable temporary restore not established")
	}
	directory := b.commands().options.CaptureDirectory
	if err := b.source.fence.commands.artifactCapacity(directory, b.policy.MaximumBytes); err != nil {
		return err
	}
	copied, err := copyRecoveryArtifact(ctx, directory, "astronomer.pgcustom", dump.artifact, b.policy.MaximumBytes)
	if err != nil {
		return err
	}
	b.captures["astronomer.pgcustom"] = copied
	compatibilityCapture, err := writeBackupBytes(ctx, b.source.fence.commands, "target-release-compatibility.json", compatibility, 1<<20)
	if err != nil {
		return err
	}
	b.captures["target-release-compatibility.json"] = compatibilityCapture
	listCapture, err := writeBackupBytes(ctx, b.source.fence.commands, "astronomer.pgcustom.list", temporary.archiveList, b.source.fence.commands.options.MaximumCaptureBytes)
	if err != nil {
		return err
	}
	b.captures["astronomer.pgcustom.list"] = listCapture
	if err := b.verifyFiles(ctx); err != nil {
		return err
	}
	// Do not publish a complete checksum inventory after an identity/schema/target
	// race. These are fresh observations, not a global lock against operators.
	if err := b.recovery.Verify(ctx); err != nil {
		return err
	}
	if err := temporary.sourceState(ctx); err != nil {
		return err
	}
	current, raw, err := b.chart.Verify(ctx)
	if err != nil {
		return err
	}
	if current.Minimum != bounds.Minimum || current.Target != bounds.Target || current.Reversible != bounds.Reversible || !sameChartMajors(current.SupportedMajors, bounds.SupportedMajors) || sha256.Sum256(raw) != sha256.Sum256(compatibility) {
		return errors.New("verified target chart changed during backup")
	}
	inventory, err := writeBackupBytes(ctx, b.source.fence.commands, "BACKUP_SHA256SUMS", b.checksumBytes(), 16<<10)
	if err != nil {
		return err
	}
	b.inventory = inventory
	if err := b.verifyInventory(ctx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	b.complete = true
	return nil
}

// VerifyBeforeMutation checks the exact sealed private files and fresh original
// release/source/target identities. It is used before Apply/Replace, never as a
// claim that an acknowledged mutation rolled back or that old revision checks
// should still pass afterward. After Apply the retained files stay available.
func (b *ReleaseBackup) VerifyBeforeMutation(ctx context.Context) error {
	if b == nil || ctx == nil {
		return errors.New("explicit retained backup context required")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.external != nil {
		return b.verifyExternalBeforeMutation(ctx)
	}
	if !b.complete || b.temporary == nil || !b.temporary.restored {
		return errors.New("complete readable backup not acknowledged")
	}
	if err := b.verifyInventory(ctx); err != nil {
		return err
	}
	if err := b.recovery.Verify(ctx); err != nil {
		return err
	}
	if err := b.temporary.sourceState(ctx); err != nil {
		return err
	}
	bounds, _, err := b.chart.Verify(ctx)
	if err != nil {
		return err
	}
	if bounds.Minimum != b.source.options.MinimumSchema || bounds.Target != b.source.options.TargetSchema || !chartSupportsDatabase(bounds, b.source.options.ExpectedMajor) {
		return errors.New("current target database compatibility differs")
	}
	return ctx.Err()
}

// Cleanup retires only the temporary database's independently retained OID/role/
// marker, never the dump/capture/checksum/checkpoint. Cleanup requires the
// source to remain clean within its initially bound minimum/target range and
// the exact temporary OID/role/marker; unknown ownership preserves the handle.
func (b *ReleaseBackup) Cleanup(ctx context.Context) error {
	if b == nil || ctx == nil {
		return errors.New("explicit backup cleanup context required")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cnpgRestore != nil {
		return b.cnpgRestore.Cleanup(ctx)
	}
	if b.temporary == nil {
		return ctx.Err()
	}
	return b.temporary.Cleanup(ctx)
}

// ChecksumDigest is the acknowledged sealing hash for sanitized historical
// evidence. It is not a fresh-file or mutation authority; uses must call the
// context-bound validation method independently.
func (b *ReleaseBackup) ChecksumDigest() (string, error) {
	if b == nil {
		return "", errors.New("retained backup required")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.complete {
		return "", errors.New("complete backup inventory not acknowledged")
	}
	return b.inventory.SHA256, nil
}
func (b *ReleaseBackup) verifyFiles(ctx context.Context) error {
	names := releaseBackupNames
	if b.external != nil {
		names = releaseBackupNames[:6]
	}
	if len(b.captures) != len(names) {
		return errors.New("complete legacy backup file set missing")
	}
	directory := b.commands().options.CaptureDirectory
	for _, name := range names {
		capture, ok := b.captures[name]
		if !ok || filepath.Base(capture.Path) != name || !capture.Acknowledged {
			return errors.New("exact retained backup file binding missing")
		}
		maximum := b.policy.MaximumBytes
		if name != "astronomer.pgcustom" {
			maximum = b.recovery.policy.MaximumBytes
		}
		switch name {
		case "target-release-compatibility.json":
			maximum = 1 << 20
		case "astronomer.pgcustom.list":
			maximum = b.commands().options.MaximumCaptureBytes
		}
		if err := verifyArtifactInput(ctx, directory, &capture, maximum); err != nil {
			return err
		}
	}
	return ctx.Err()
}
func (b *ReleaseBackup) verifyInventory(ctx context.Context) error {
	if err := b.verifyFiles(ctx); err != nil {
		return err
	}
	directory := b.commands().options.CaptureDirectory
	if filepath.Base(b.inventory.Path) != "BACKUP_SHA256SUMS" {
		return errors.New("backup inventory file binding missing")
	}
	if err := verifyArtifactInput(ctx, directory, &b.inventory, 16<<10); err != nil {
		return err
	}
	// Exact ordered filenames and hashes remain the legacy sha256sum format.
	// Raw aliases cannot inject filename escapes, duplicate rows or omissions.
	expected := b.checksumBytes()
	actual, err := readCapture(directory, "BACKUP_SHA256SUMS", 16<<10, b.inventory.SHA256)
	if err != nil {
		return err
	}
	if string(actual) != string(expected) {
		return errors.New("complete backup checksum inventory differs")
	}
	return ctx.Err()
}
func (b *ReleaseBackup) checksumBytes() []byte {
	var text strings.Builder
	names := releaseBackupNames
	if b.external != nil {
		names = releaseBackupNames[:6]
	}
	for _, name := range names {
		capture := b.captures[name]
		text.WriteString(strings.TrimPrefix(capture.SHA256, "sha256:"))
		text.WriteString("  ")
		text.WriteString(name)
		text.WriteByte('\n')
	}
	return []byte(text.String())
}
func chartSupportsDatabase(bounds ChartSchemaBounds, major uint32) bool {
	for _, v := range bounds.SupportedMajors {
		if v == major {
			return true
		}
	}
	return false
}
func sameChartMajors(a, b []uint32) bool {
	if len(a) != len(b) {
		return false
	}
	for i, v := range a {
		if v != b[i] {
			return false
		}
	}
	return true
}

// Bounded generated recovery metadata is private, exclusive and synced. Failed
// partial writes remain in place and are never overwritten on a second attempt.
// This helper creates retained recovery material, not a public JSON report.
func writeBackupBytes(ctx context.Context, c *Commands, name string, raw []byte, maximum int64) (process.ArtifactCapture, error) {
	var empty process.ArtifactCapture
	if ctx == nil || c == nil || c.artifactCapacity == nil || filepath.Base(name) != name || name == "." || strings.ContainsAny(name, "\x00\t\r\n") || len(raw) == 0 || maximum <= 0 || int64(len(raw)) > maximum {
		return empty, errors.New("finite exact private backup content required")
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	directory := c.options.CaptureDirectory
	if err := c.artifactCapacity(directory, maximum); err != nil {
		return empty, err
	}
	before, err := os.Lstat(directory)
	if err != nil || !before.IsDir() || before.Mode().Perm() != 0o700 {
		return empty, errors.New("private backup directory required")
	}
	owner, ok := before.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != safeconv.Uint32(os.Geteuid()) {
		return empty, errors.New("private backup directory owner differs")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return empty, errors.New("open private backup scope")
	}
	defer func(close func() error) { _ = close() }(root.Close)
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(before, opened) {
		return empty, errors.New("private backup directory changed before write")
	}
	output, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return empty, errors.New("create exclusive private backup content")
	}
	defer func(close func() error) { _ = close() }(output.Close)
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	n, err := output.Write(raw)
	if err != nil || n != len(raw) {
		return empty, errors.New("retain private backup bytes")
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	if err := output.Sync(); err != nil {
		return empty, errors.New("sync private backup content")
	}
	if err := output.Close(); err != nil {
		return empty, errors.New("close private backup content")
	}
	current, err := os.Lstat(directory)
	if err != nil || !os.SameFile(before, current) || current.Mode().Perm() != 0o700 {
		return empty, errors.New("private backup directory changed during write")
	}
	hash := sha256.Sum256(raw)
	capture := process.ArtifactCapture{Path: filepath.Join(directory, name), SizeBytes: int64(len(raw)), SHA256: "sha256:" + hex.EncodeToString(hash[:]), Acknowledged: true}
	if err := verifyArtifactInput(ctx, directory, &capture, maximum); err != nil {
		return empty, err
	}
	return capture, ctx.Err()
}
