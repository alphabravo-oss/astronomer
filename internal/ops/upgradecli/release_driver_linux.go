//go:build linux

package upgradecli

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/distribution/reference"

	"github.com/alphabravocompany/astronomer-go/internal/ops/upgrade"
)

// ReleaseDriver composes the real scoped adapters. It deliberately rejects
// external databases and disposable replacement until their independent backup,
// key/restore/DDL assessment adapters exist. Such modes remain on the legacy
// wrapper; successful unit/helper tests do not prove CLI parity or an upgrade.
// Its state is serialized, and only the exact initially captured Plan can run.
type ReleaseDriver struct {
	mu           sync.Mutex
	plan         upgrade.Plan
	release      *HelmRelease
	verification *ReleaseVerification
	backup       *ReleaseBackup
	target       *TargetWorkloads
}

func NewReleaseDriver(ctx context.Context, verification *ReleaseVerification, backup *ReleaseBackup, target *TargetWorkloads) (*ReleaseDriver, error) {
	if ctx == nil || verification == nil || verification.chart == nil || verification.chart.metadata == nil || backup == nil || backup.recovery == nil || backup.recovery.observation == nil || target == nil || target.observation == nil || target.readyz == nil || target.database == nil {
		return nil, errors.New("complete signed release, backup and target adapters required")
	}
	release := target.observation.release
	if release == nil || release.fence == nil || release.fence.commands == nil || release.plan.Validate() != nil || release.plan.Replace != nil || target.observation.source == nil || target.observation.readiness == nil || target.observation.readiness.fence != release.fence {
		return nil, errors.New("only initially bound nonreplacement bundled-database plans supported")
	}
	if backup.chart != verification.chart || backup.recovery.observation != target.observation || backup.source != target.observation.source || target.database.source != backup.source || verification.commands != release.fence.commands || target.readyz.release != release {
		return nil, errors.New("release adapters must share one initial command/chart/database owner")
	}
	if filepath.Join(verification.chart.metadata.directory, "release-manifest.json") != release.manifestFile || verification.chart.metadata.target != release.plan.Target {
		return nil, errors.New("release lifecycle does not use the authenticated target manifest")
	}
	driver := &ReleaseDriver{plan: release.plan, release: release, verification: verification, backup: backup, target: target}
	if err := driver.bindChartArguments(ctx); err != nil {
		return nil, err
	}
	return driver, ctx.Err()
}

// bindChartArguments compares every immutable captured argv byte with canonical
// arguments derived from fresh authenticated chart defaults and signed subjects.
// Neither a caller RuntimeSources map nor a different OCI chart repository can
// grant the complete Driver authority even if its own constructor was valid.
func (d *ReleaseDriver) bindChartArguments(ctx context.Context) error {
	if err := d.release.manifestBinding(); err != nil {
		return err
	}
	chart := d.verification.chart
	sources, err := chart.RuntimeSources(ctx)
	if err != nil {
		return err
	}
	major, err := declaredPostgresMajor(sources["postgres"])
	if err != nil || major != d.backup.source.options.ExpectedMajor {
		return errors.New("bundled upgrade requires the same authenticated PostgreSQL runtime major")
	}
	manifest, err := chart.metadata.Verify(ctx)
	if err != nil {
		return err
	}
	repository, digest, ok := strings.Cut(manifest.Astronomer.Chart.Reference, "@")
	if !ok || repository == "" || digest == "" {
		return errors.New("exact signed OCI chart repository required")
	}
	input := upgrade.HelmInputs{Tool: upgrade.HelmToolBinding{Path: d.release.tool.Path, Version: d.release.tool.Version, SHA256: d.release.tool.SHA256}, Plan: d.plan, ChartReference: "oci://" + repository, ManifestFile: d.release.manifestFile, Manifest: manifest, RuntimeSources: sources}
	dry, err := input.DryRunArguments()
	if err != nil || !slices.Equal(dry, d.release.arguments[upgrade.DryRun]) {
		return errors.New("captured dry-run arguments differ from authenticated target")
	}
	if d.plan.Mode == upgrade.Execute {
		apply, err := input.ApplyArguments()
		if err != nil || !slices.Equal(apply, d.release.arguments[upgrade.Apply]) {
			return errors.New("captured apply arguments differ from authenticated target")
		}
		rollback, err := input.RollbackArguments(d.release.fence.options.Revision)
		if err != nil || !slices.Equal(rollback, d.release.arguments[upgrade.Rollback]) {
			return errors.New("captured rollback arguments differ from original revision")
		}
	}
	return ctx.Err()
}

func (d *ReleaseDriver) Fence(ctx context.Context, identity upgrade.Identity, replacement *upgrade.DisposableLease) error {
	if d == nil || d.release == nil || d.release.fence == nil || ctx == nil || identity != d.plan.Identity || replacement != nil {
		return errors.New("initial nonreplacement Driver ownership required")
	}
	return d.release.fence.Fence(ctx, identity, nil)
}

func (d *ReleaseDriver) Step(ctx context.Context, stage upgrade.Stage, plan upgrade.Plan) (upgrade.Outcome, error) {
	if d == nil || d.release == nil || d.release.fence == nil || ctx == nil {
		return upgrade.Outcome{Known: true}, errors.New("explicit Driver step context required")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if plan != d.plan || plan.Replace != nil {
		return upgrade.Outcome{Known: true}, errors.New("immutable initial Driver plan differs")
	}
	if err := d.Fence(ctx, plan.Identity, nil); err != nil {
		return upgrade.Outcome{Known: true}, err
	}
	switch stage {
	case upgrade.Verify:
		if err := d.bindChartArguments(ctx); err != nil {
			return upgrade.Outcome{Known: true}, err
		}
		return upgrade.Outcome{Known: true}, d.verification.Verify(ctx)
	case upgrade.Backup:
		if err := d.target.database.Capture(ctx); err != nil {
			return upgrade.Outcome{Known: true}, err
		}
		return upgrade.Outcome{Known: true}, d.backup.Capture(ctx)
	case upgrade.DryRun, upgrade.Apply:
		// Repeat real signer/attestation/image/chart checks and sealed readable
		// backup/source schema gates immediately before the joined Helm action.
		// Verify does not replay backup creation; Capture is independently one-shot.
		if err := d.verification.Verify(ctx); err != nil {
			return upgrade.Outcome{Known: true}, err
		}
		if err := d.bindChartArguments(ctx); err != nil {
			return upgrade.Outcome{Known: true}, err
		}
		if err := d.backup.VerifyBeforeMutation(ctx); err != nil {
			return upgrade.Outcome{Known: true}, err
		}
		if err := d.target.observation.readiness.Check(ctx); err != nil {
			return upgrade.Outcome{Known: true}, err
		}
		return d.release.RunStage(ctx, stage)
	case upgrade.Observe:
		// Application is acknowledged only by RunStage/actual reconciliation. A
		// failed observation cannot invent a positive mutation result; Machine
		// retains its already acknowledged Apply state independently.
		return upgrade.Outcome{Known: true}, d.target.Check(ctx)
	case upgrade.Rollback:
		// A later target observation may fail because its schema migrated.
		// Do not require the original pre-Apply schema or delete the backup to
		// authorize rollback of the anchored Helm revision. The state machine
		// invokes this only after acknowledged/assessed application, under fresh
		// namespace/release/tool/input fences. This is not database reversal.
		return d.release.RunStage(ctx, stage)
	default:
		return upgrade.Outcome{Known: true}, errors.New("stage is outside the composed release Driver")
	}
}

func (d *ReleaseDriver) Reconcile(ctx context.Context, state upgrade.Checkpoint) (upgrade.Outcome, error) {
	if d == nil || d.release == nil || d.release.fence == nil || ctx == nil || state.Identity != d.plan.Identity || state.Target != d.plan.Target || state.Replaced || state.Pending == upgrade.ReplaceDatabase {
		return upgrade.Outcome{}, errors.New("initial nonreplacement checkpoint observation required")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.Fence(ctx, d.plan.Identity, nil); err != nil {
		return upgrade.Outcome{}, err
	}
	return d.target.observation.ReconcileHelm(ctx)
}

func (d *ReleaseDriver) Cleanup(ctx context.Context, plan upgrade.Plan, policy upgrade.CleanupPolicy) error {
	if d == nil || d.release == nil || d.release.fence == nil || ctx == nil || plan != d.plan || !policy.OwnedEphemeralOnly || !policy.PreserveBackup || !policy.PreserveCheckpoint {
		return errors.New("initial Driver plan and recovery-preserving cleanup policy required")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.Fence(ctx, plan.Identity, nil); err != nil {
		return err
	}
	// The backup adapter retires only its independently captured temporary DB
	// OID/role/marker in the supported current schema range. All private recovery,
	// command originals, dump/checksum inventory and checkpoint remain retained.
	return d.backup.cleanupWithTarget(ctx, d.target.database)
}

var _ upgrade.Driver = (*ReleaseDriver)(nil)

// declaredPostgresMajor reads only a source reference already authenticated by
// RuntimeSources. It is a compatibility veto, not proof of the running image or
// permission to migrate a major. Tags without a closed numeric major fail closed.
func declaredPostgresMajor(raw string) (uint32, error) {
	named, err := reference.ParseNormalizedNamed(raw)
	if err != nil {
		return 0, errors.New("exact tagged PostgreSQL runtime source required")
	}
	tagged, ok := named.(reference.Tagged)
	if !ok {
		return 0, errors.New("declared PostgreSQL runtime major required")
	}
	if _, digested := named.(reference.Digested); digested {
		return 0, errors.New("unambiguous PostgreSQL source tag required")
	}
	tag := tagged.Tag()
	end := strings.IndexAny(tag, ".-")
	if end < 0 {
		end = len(tag)
	}
	value, err := strconv.ParseUint(tag[:end], 10, 32)
	if err != nil || value == 0 || strconv.FormatUint(value, 10) != tag[:end] {
		return 0, errors.New("closed numeric PostgreSQL source major required")
	}
	return uint32(value), nil
}
