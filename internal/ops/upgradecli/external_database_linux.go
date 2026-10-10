//go:build linux

package upgradecli

import (
	"context"
	"errors"
	"strconv"

	"github.com/alphabravocompany/astronomer-go/internal/ops/process"
)

// ExternalDatabaseOptions preserves the old wrapper's explicit operator
// confirmation. SchemaVersion is an operator assertion, never a live SQL read,
// a mutation lease or a claim that a custom-format backup was restored. Helm's
// target hook still independently verifies the application's configured DB.
type ExternalDatabaseOptions struct {
	BackupConfirmed      bool
	SchemaVersion        uint32
	ObservedValuesSHA256 [32]byte
}
type externalBackupBinding struct {
	selection    ExternalDatabaseOptions
	bounds       ChartSchemaBounds
	confirmation process.ArtifactCapture
}

func validateExternalSelection(fence *OwnershipFence, selection ExternalDatabaseOptions, bounds ChartSchemaBounds) error {
	if fence == nil || fence.options.DatabasePod != "" || !selection.BackupConfirmed || selection.ObservedValuesSHA256 == ([32]byte{}) || selection.SchemaVersion < bounds.Minimum || selection.SchemaVersion > bounds.Target {
		return errors.New("explicit external backup confirmation/schema and absent bundled selection required")
	}
	return nil
}

func NewExternalReleaseBackup(chart *TargetChartContent, recovery *HelmRecovery, selection ExternalDatabaseOptions, policy ArtifactPolicy) (*ReleaseBackup, error) {
	if chart == nil || chart.metadata == nil || recovery == nil || recovery.observation == nil || recovery.observation.release == nil || recovery.observation.source != nil || recovery.observation.release.fence.options.DatabasePod != "" || policy.MaximumBytes <= 0 || !selection.BackupConfirmed || selection.ObservedValuesSHA256 == ([32]byte{}) || selection.SchemaVersion == 0 {
		return nil, errors.New("one explicitly external release/recovery owner required")
	}
	return &ReleaseBackup{chart: chart, recovery: recovery, policy: policy, external: &externalBackupBinding{selection: selection}, captures: map[string]process.ArtifactCapture{}}, nil
}

func (b *ReleaseBackup) commands() *Commands { return b.recovery.observation.release.fence.commands }

// Both observations require the complete canonical pod inventory to remain
// empty. A failed query, recreated namespace/revision, or newly bundled DB
// cannot inherit the earlier operator confirmation.
func (b *ReleaseBackup) externalAbsent(ctx context.Context) error {
	if b == nil || ctx == nil || b.external == nil || b.source != nil || b.recovery == nil || b.recovery.observation == nil {
		return errors.New("explicit external observation owner/context required")
	}
	r := b.recovery.observation.release
	if r == nil || r.fence == nil || r.fence.options.DatabasePod != "" {
		return errors.New("external release binding differs")
	}
	if err := r.fence.Fence(ctx, r.plan.Identity, nil); err != nil {
		return err
	}
	id := r.plan.Identity
	if err := recheckHelmDatabaseBackend(ctx, r.fence.execute, r.tool, r.fence.options.Kubeconfig, id, "external", b.external.selection.ObservedValuesSHA256); err != nil {
		return err
	}
	args := []string{"--kubeconfig", r.fence.options.Kubeconfig, "--context", id.Context, "get", "pods", "--namespace", id.Namespace, "--selector", "app.kubernetes.io/instance=" + id.Release + ",app.kubernetes.io/component=postgres", "--output", "go-template=" + initialDatabaseTemplate}
	raw, err := r.fence.execute(ctx, r.fence.kubectl, args)
	if err != nil {
		return err
	}
	rows, err := readinessRows(raw)
	if err != nil || len(rows) != 0 {
		return errors.New("external selection no longer has a complete empty bundled-pod inventory")
	}
	if err := r.fence.Fence(ctx, id, nil); err != nil {
		return err
	}
	if err := recheckHelmDatabaseBackend(ctx, r.fence.execute, r.tool, r.fence.options.Kubeconfig, id, "external", b.external.selection.ObservedValuesSHA256); err != nil {
		return err
	}
	return ctx.Err()
}

// Called only under the existing backup mutex and after its one-shot admission.
// The six legacy recovery/checksum files retain their wire names and order. A
// separate private confirmation records its weaker assurance honestly.
func (b *ReleaseBackup) captureExternal(ctx context.Context) error {
	bounds, compatibility, err := b.chart.Verify(ctx)
	if err != nil {
		return err
	}
	if err := validateExternalSelection(b.recovery.observation.release.fence, b.external.selection, bounds); err != nil {
		return err
	}
	if err := b.externalAbsent(ctx); err != nil {
		return err
	}
	if err := b.recovery.Capture(ctx); err != nil {
		return err
	}
	b.recovery.mu.Lock()
	for name, capture := range b.recovery.artifacts {
		b.captures[name] = capture
	}
	b.recovery.mu.Unlock()
	capture, err := writeBackupBytes(ctx, b.commands(), "target-release-compatibility.json", compatibility, 1<<20)
	if err != nil {
		return err
	}
	b.captures["target-release-compatibility.json"] = capture
	confirmation := []byte("assurance=operator-confirmed-external-backup\nasserted-clean-schema=" + strconv.FormatUint(uint64(b.external.selection.SchemaVersion), 10) + "\nlive-schema-verified-by-target-hook=false\n")
	b.external.confirmation, err = writeBackupBytes(ctx, b.commands(), "external-backup-confirmation.txt", confirmation, 1<<10)
	if err != nil {
		return err
	}
	b.external.bounds = bounds
	if err := b.verifyFiles(ctx); err != nil {
		return err
	}
	if err := b.recovery.Verify(ctx); err != nil {
		return err
	}
	if err := b.externalAbsent(ctx); err != nil {
		return err
	}
	b.inventory, err = writeBackupBytes(ctx, b.commands(), "BACKUP_SHA256SUMS", b.checksumBytes(), 16<<10)
	if err != nil {
		return err
	}
	if err := b.verifyInventory(ctx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	b.complete = true
	return nil
}

func (b *ReleaseBackup) verifyExternalBeforeMutation(ctx context.Context) error {
	if !b.complete || b.external == nil || b.temporary != nil || b.source != nil {
		return errors.New("sealed explicitly confirmed external recovery required")
	}
	if err := b.verifyInventory(ctx); err != nil {
		return err
	}
	if err := verifyArtifactInput(ctx, b.commands().options.CaptureDirectory, &b.external.confirmation, 1<<10); err != nil {
		return err
	}
	if err := b.recovery.Verify(ctx); err != nil {
		return err
	}
	bounds, _, err := b.chart.Verify(ctx)
	if err != nil {
		return err
	}
	original := b.external.bounds
	if bounds.Minimum != original.Minimum || bounds.Target != original.Target || bounds.Reversible != original.Reversible || !sameChartMajors(bounds.SupportedMajors, original.SupportedMajors) {
		return errors.New("external target compatibility changed")
	}
	if err := validateExternalSelection(b.recovery.observation.release.fence, b.external.selection, bounds); err != nil {
		return err
	}
	return b.externalAbsent(ctx)
}

func NewExternalTargetWorkloads(observation *HelmStateObservation, readyz *ReadyzObservation, backup *ReleaseBackup) (*TargetWorkloads, error) {
	if observation == nil || observation.source != nil || observation.release == nil || observation.readiness == nil || observation.readiness.fence != observation.release.fence || readyz == nil || readyz.release != observation.release || backup == nil || backup.external == nil || backup.source != nil || backup.recovery == nil || backup.recovery.observation != observation {
		return nil, errors.New("one explicitly external complete target owner required")
	}
	return &TargetWorkloads{observation: observation, readyz: readyz, external: backup}, nil
}
