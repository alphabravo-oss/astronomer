//go:build linux

package upgradecli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"
	"sync"

	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
	"github.com/alphabravocompany/astronomer-go/internal/ops/upgrade"
)

// InvocationOptions contains explicit operator bindings. No ambient environment,
// PATH, current runtime tag, caller chart-source map or unverified Manifest can
// become authority. Initial identities must come from independent observations;
// all actual stages re-read the existing concrete fences. This is bundled-mode
// composition only; shell argument/approval/evidence parity remains separate.
type InvocationOptions struct {
	Plan                upgrade.Plan
	Commands            CommandOptions
	Helm, Kubectl, GH   Tool
	Fence               FenceOptions
	Database            DatabaseBackupOptions
	ReleaseDirectory    string
	TrustDirectory      string
	CheckpointDirectory string
	ContentPolicy       ArtifactPolicy
	RecoveryPolicy      ArtifactPolicy
	MinimumReadyNodes   uint32
	RepositoryPolicy    httpclient.DestinationPolicy
	RepositoryOptions   httpclient.DestinationOptions
}

// Invocation owns one complete concrete Driver and its private checkpoint lock.
// Close joins Run through this mutex and releases only public trust/client/lock
// resources. Backup, checkpoint and command originals are always retained.
type Invocation struct {
	mu       sync.Mutex
	plan     upgrade.Plan
	driver   *ReleaseDriver
	store    *upgrade.FileStore
	metadata *SignedReleaseMetadata
	chart    *TargetChartContent
	started  bool
	closed   bool
}

func PrepareInvocation(ctx context.Context, options InvocationOptions) (*Invocation, error) {
	if ctx == nil || options.Plan.Validate() != nil || options.Plan.Replace != nil || options.Fence.Identity != options.Plan.Identity || options.Helm.Kind != Helm || options.Kubectl.Kind != Kubectl || options.GH.Kind != GitHubCLI {
		return nil, errors.New("explicit initially bound bundled release invocation required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(options.ReleaseDirectory) || !filepath.IsAbs(options.TrustDirectory) || !filepath.IsAbs(options.CheckpointDirectory) || options.ContentPolicy.MaximumBytes <= 0 || options.RecoveryPolicy.MaximumBytes <= 0 || options.Database.ArtifactPolicy.MaximumBytes <= 0 {
		return nil, errors.New("explicit private artifact/checkpoint scopes and budgets required")
	}
	bounded, cancel := context.WithTimeout(ctx, options.Plan.Timeout)
	defer cancel()
	commands, err := NewCommands(options.Commands)
	if err != nil {
		return nil, err
	}
	for _, tool := range []Tool{options.Helm, options.Kubectl, options.GH} {
		if err := commands.VerifyTool(bounded, tool); err != nil {
			return nil, err
		}
	}
	fence, err := NewOwnershipFence(commands, options.Kubectl, options.Fence)
	if err != nil {
		return nil, err
	}
	if err := fence.Fence(bounded, options.Plan.Identity, nil); err != nil {
		return nil, err
	}
	invocation := &Invocation{plan: options.Plan}
	complete := false
	defer func() {
		if !complete {
			_ = invocation.Close()
		}
	}()
	invocation.metadata, err = NewSignedReleaseMetadata(bounded, options.ReleaseDirectory, options.TrustDirectory, options.Plan.Target, options.ContentPolicy)
	if err != nil {
		return nil, err
	}
	invocation.chart, err = NewTargetChartContent(bounded, invocation.metadata, options.RepositoryPolicy, options.RepositoryOptions)
	if err != nil {
		return nil, err
	}
	bounds, _, err := invocation.chart.Verify(bounded)
	if err != nil {
		return nil, err
	}
	// Schema values originate in the exact authenticated target chart. Retain
	// the independent observed major as a compatibility veto, never auto-upgrade.
	if !chartSupportsDatabase(bounds, options.Database.ExpectedMajor) {
		return nil, errors.New("observed database major is outside authenticated target chart")
	}
	options.Database.MinimumSchema = bounds.Minimum
	options.Database.TargetSchema = bounds.Target
	source, err := NewSourceDatabaseBackup(fence, options.Database)
	if err != nil {
		return nil, err
	}
	manifest, err := invocation.metadata.Verify(bounded)
	if err != nil {
		return nil, err
	}
	runtimeSources, err := invocation.chart.RuntimeSources(bounded)
	if err != nil {
		return nil, err
	}
	manifestBytes, err := invocation.metadata.read(bounded, "release-manifest.json")
	if err != nil {
		return nil, err
	}
	manifestHash := sha256.Sum256(manifestBytes)
	chartRepository, _, ok := strings.Cut(manifest.Astronomer.Chart.Reference, "@")
	if !ok {
		return nil, errors.New("exact signed chart repository required")
	}
	release, err := NewHelmRelease(bounded, fence, options.Helm, HelmReleaseOptions{Plan: options.Plan, ManifestFile: filepath.Join(options.ReleaseDirectory, "release-manifest.json"), ManifestSHA256: "sha256:" + hex.EncodeToString(manifestHash[:]), ChartReference: "oci://" + chartRepository, RuntimeSources: runtimeSources})
	if err != nil {
		return nil, err
	}
	readiness, err := NewReadiness(fence, options.MinimumReadyNodes)
	if err != nil {
		return nil, err
	}
	observation, err := NewHelmStateObservation(release, readiness, source)
	if err != nil {
		return nil, err
	}
	recovery, err := NewHelmRecovery(observation, options.RecoveryPolicy)
	if err != nil {
		return nil, err
	}
	backup, err := NewReleaseBackup(invocation.chart, recovery, source, options.Database.ArtifactPolicy)
	if err != nil {
		return nil, err
	}
	targetDatabase, err := NewTargetDatabaseObservation(source)
	if err != nil {
		return nil, err
	}
	readyz, err := NewReadyzObservation(release)
	if err != nil {
		return nil, err
	}
	target, err := NewTargetWorkloadsWithDatabase(observation, readyz, targetDatabase)
	if err != nil {
		return nil, err
	}
	verification, err := NewReleaseVerification(bounded, invocation.chart, commands, options.GH)
	if err != nil {
		return nil, err
	}
	invocation.driver, err = NewReleaseDriver(bounded, verification, backup, target)
	if err != nil {
		return nil, err
	}
	invocation.store, err = upgrade.OpenFileStore(options.CheckpointDirectory, options.Plan)
	if err != nil {
		return nil, err
	}
	if err := bounded.Err(); err != nil {
		return nil, err
	}
	complete = true
	return invocation, nil
}

// Run uses the actual concrete Driver through the canonical durable Machine.
// Existing checkpoints receive the Machine's explicit reconciliation/assessment
// behavior; preparation does not grant resume replay or new mutation authority.
func (i *Invocation) Run(ctx context.Context) upgrade.Result {
	if i == nil || ctx == nil {
		return upgrade.Result{ExitCode: 1, Result: "failed", FailedStage: upgrade.Verify}
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.closed || i.started || i.driver == nil || i.store == nil {
		return upgrade.Result{ExitCode: 1, Result: "failed", Mode: i.plan.Mode, FailedStage: upgrade.Verify}
	}
	i.started = true
	previous, err := i.store.Load(ctx)
	if err != nil {
		result := upgrade.Result{ExitCode: 1, Result: "failed", Mode: i.plan.Mode, FailedStage: upgrade.Verify}
		if errors.Is(err, context.Canceled) {
			result.ExitCode = 130
		}
		if errors.Is(err, context.DeadlineExceeded) {
			result.ExitCode = 124
		}
		return result
	}
	return (upgrade.Machine{Driver: i.driver, Store: i.store}).Run(ctx, i.plan, previous)
}

func (i *Invocation) Close() error {
	if i == nil {
		return nil
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.closed {
		return nil
	}
	i.closed = true
	if i.chart != nil {
		i.chart.Close()
	}
	if i.metadata != nil {
		i.metadata.Close()
	}
	if i.store != nil {
		return i.store.Close()
	}
	return nil
}
