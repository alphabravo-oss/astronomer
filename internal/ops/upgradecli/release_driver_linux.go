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

// ReleaseDriver composes the co-owned release, backup, readiness and target
// adapters. Bundled replacement requires an independent disposable OID owner;
// CNPG uses a separate isolated restore Pod and never grants source DROP.
// External mode retains explicit operator assertions without claiming a copy.
// Actual official CLI/cluster/restore/parity qualification remains separate.
// Its state is serialized, and only the exact initially captured Plan can run.
type ReleaseDriver struct {
	mu           sync.Mutex
	plan         upgrade.Plan
	release      *HelmRelease
	verification *ReleaseVerification
	backup       *ReleaseBackup
	target       *TargetWorkloads
	replacement  *DisposableReplacement
}

func NewReleaseDriver(ctx context.Context, verification *ReleaseVerification, backup *ReleaseBackup, target *TargetWorkloads) (*ReleaseDriver, error) {
	if ctx == nil || verification == nil || verification.chart == nil || verification.chart.metadata == nil || backup == nil || backup.recovery == nil || backup.recovery.observation == nil || target == nil || target.observation == nil || target.readyz == nil || (target.database == nil && (backup.external == nil || target.external != backup)) {
		return nil, errors.New("complete signed release, backup and target adapters required")
	}
	release := target.observation.release
	if release == nil || release.fence == nil || release.fence.commands == nil || release.plan.Validate() != nil || (target.observation.source == nil && backup.external == nil) || target.observation.readiness == nil || target.observation.readiness.fence != release.fence {
		return nil, errors.New("only initially bound co-owned release/database plans supported")
	}
	if backup.chart != verification.chart || backup.recovery.observation != target.observation || backup.source != target.observation.source || (target.database != nil && target.database.source != backup.source) || verification.commands != release.fence.commands || target.readyz.release != release {
		return nil, errors.New("release adapters must share one initial command/chart/database owner")
	}
	if filepath.Join(verification.chart.metadata.directory, "release-manifest.json") != release.manifestFile || verification.chart.metadata.target != release.plan.Target {
		return nil, errors.New("release lifecycle does not use the authenticated target manifest")
	}
	var err error
	driver := &ReleaseDriver{plan: release.plan, release: release, verification: verification, backup: backup, target: target}
	if release.plan.Replace != nil {
		driver.replacement, err = NewDisposableReplacement(backup, release.plan.Replace)
		if err != nil {
			return nil, err
		}
	}
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
	if d.backup.external == nil && d.backup.source.cnpg == nil {
		major, err := declaredPostgresMajor(sources["postgres"])
		if err != nil || major != d.backup.source.options.ExpectedMajor {
			return errors.New("bundled upgrade requires the same authenticated PostgreSQL runtime major")
		}
	}
	if d.backup.source != nil && d.backup.source.cnpg != nil {
		_, major, err := chart.CNPGImage(ctx)
		if err != nil || major != d.backup.source.options.ExpectedMajor {
			return errors.New("CNPG upgrade requires the same authenticated CNPG operand major")
		}
	}
	manifest, err := chart.metadata.Verify(ctx)
	if err != nil {
		return err
	}
	repository, digest, ok := strings.Cut(manifest.Astronomer.Chart.Reference, "@")
	if !ok || repository == "" || digest == "" {
		return errors.New("exact signed OCI chart repository required")
	}
	chartReference := "oci://" + repository
	if d.release.chartInput != nil {
		if err := d.release.chartBinding(ctx); err != nil {
			return err
		}
		chartReference = d.release.chartReference
	} else if d.release.chartReference != "" && d.release.chartReference != chartReference {
		return errors.New("remote chart selection differs from authenticated target")
	}
	input := upgrade.HelmInputs{Tool: upgrade.HelmToolBinding{Path: d.release.tool.Path, Version: d.release.tool.Version, SHA256: d.release.tool.SHA256}, Plan: d.plan, ChartReference: chartReference, ManifestFile: d.release.manifestFile, Manifest: manifest, RuntimeSources: sources}
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
	if d == nil || d.release == nil || d.release.fence == nil || ctx == nil || identity != d.plan.Identity || (replacement == nil) != (d.plan.Replace == nil) || (replacement != nil && *replacement != *d.plan.Replace) {
		return errors.New("initial Driver ownership required")
	}
	return d.release.fence.Fence(ctx, identity, d.plan.Replace)
}

func (d *ReleaseDriver) Step(ctx context.Context, stage upgrade.Stage, plan upgrade.Plan) (upgrade.Outcome, error) {
	if d == nil || d.release == nil || d.release.fence == nil || ctx == nil {
		return upgrade.Outcome{Known: true}, errors.New("explicit Driver step context required")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if !sameReleasePlan(plan, d.plan) {
		return upgrade.Outcome{Known: true}, errors.New("immutable initial Driver plan differs")
	}
	if err := d.Fence(ctx, plan.Identity, plan.Replace); err != nil {
		return upgrade.Outcome{Known: true}, err
	}
	switch stage {
	case upgrade.Verify:
		if err := d.bindChartArguments(ctx); err != nil {
			return upgrade.Outcome{Known: true}, err
		}
		return upgrade.Outcome{Known: true}, d.verification.Verify(ctx)
	case upgrade.Backup:
		if d.target.database != nil {
			if err := d.target.database.Capture(ctx); err != nil {
				return upgrade.Outcome{Known: true}, err
			}
		}
		if err := d.backup.Capture(ctx); err != nil {
			return upgrade.Outcome{Known: true}, err
		}
		if d.replacement != nil {
			return upgrade.Outcome{Known: true}, d.replacement.Capture(ctx)
		}
		return upgrade.Outcome{Known: true}, nil
	case upgrade.ReplaceDatabase:
		if d.replacement == nil {
			return upgrade.Outcome{Known: true}, errors.New("exact disposable replacement owner missing")
		}
		return d.replacement.Replace(ctx)
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
		var quiesced *DisposableReplacement
		if stage == upgrade.Apply && d.replacement != nil {
			outcome, err := d.replacement.Reconcile(ctx)
			if err != nil || !outcome.Known || !outcome.Replaced {
				return upgrade.Outcome{Known: true}, errors.New("Apply requires freshly assessed owned restored replacement OID")
			}
			quiesced = d.replacement
		}
		if err := d.target.observation.readiness.check(ctx, quiesced); err != nil {
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
	if d == nil || d.release == nil || d.release.fence == nil || ctx == nil || state.Identity != d.plan.Identity || state.Target != d.plan.Target {
		return upgrade.Outcome{}, errors.New("initial nonreplacement checkpoint observation required")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.Fence(ctx, d.plan.Identity, d.plan.Replace); err != nil {
		return upgrade.Outcome{}, err
	}
	var replaced upgrade.Outcome
	if d.replacement != nil {
		if !d.replacement.captured {
			return upgrade.Outcome{}, errors.New("replacement resume requires retained original OID assessment")
		}
		var err error
		replaced, err = d.replacement.Reconcile(ctx)
		if err != nil || !replaced.Known {
			return upgrade.Outcome{}, err
		}
		if state.Pending == upgrade.ReplaceDatabase {
			return replaced, nil
		}
	}
	outcome, err := d.target.observation.reconcileHelm(ctx, d.replacement != nil)
	if err != nil {
		return upgrade.Outcome{}, err
	}
	outcome.Replaced = replaced.Replaced
	return outcome, nil
}

func (d *ReleaseDriver) Cleanup(ctx context.Context, plan upgrade.Plan, policy upgrade.CleanupPolicy) error {
	if d == nil || d.release == nil || d.release.fence == nil || ctx == nil || !sameReleasePlan(plan, d.plan) || !policy.OwnedEphemeralOnly || !policy.PreserveBackup || !policy.PreserveCheckpoint {
		return errors.New("initial Driver plan and recovery-preserving cleanup policy required")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.Fence(ctx, plan.Identity, plan.Replace); err != nil {
		return err
	}
	// The backup adapter retires only its independently captured temporary DB
	// OID/role/marker in the supported current schema range. All private recovery,
	// command originals, dump/checksum inventory and checkpoint remain retained.
	if d.replacement != nil {
		if err := d.replacement.RestoreReplicas(ctx); err != nil {
			return err
		}
	}
	if d.backup.external != nil {
		return d.backup.externalAbsent(ctx)
	}
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
