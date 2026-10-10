//go:build linux

package upgradecli

import (
	"context"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"

	"github.com/alphabravocompany/astronomer-go/internal/ops/process"
	"github.com/alphabravocompany/astronomer-go/internal/ops/upgrade"
	"github.com/alphabravocompany/astronomer-go/internal/releasecontract"
)

// HelmReleaseOptions follows signer/attestation, exact chart-byte equality,
// target chart runtime-role and backup/schema/readiness/PDB gates in composition.
// This constructor rebinds those manifest bytes and constructs canonical argv;
// knowing a checksum alone is not signature or backup proof.
type HelmReleaseOptions struct {
	Plan           upgrade.Plan
	ManifestFile   string
	ManifestSHA256 string
	ChartReference string
	RuntimeSources map[string]string
}

// HelmRelease is the concrete CLI portion of the Driver: dry-run, apply and
// rollback use the actual verified executable through owned joined Commands.
// Signer/backup/observe/replacement/reconciliation/cleanup remain separate typed
// stages; this type deliberately does not pretend to implement the full Driver.
type HelmRelease struct {
	fence            *OwnershipFence
	tool             Tool
	plan             upgrade.Plan
	manifestFile     string
	manifestIdentity os.FileInfo
	manifestHash     [32]byte
	chartReference   string
	chartInput       *metadataInput
	arguments        map[upgrade.Stage][]string
	execute          func(context.Context, Tool, []string) ([]byte, process.Result, error)
}

func NewHelmRelease(ctx context.Context, fence *OwnershipFence, tool Tool, options HelmReleaseOptions) (*HelmRelease, error) {
	if ctx == nil || fence == nil || tool.Kind != Helm || !filepath.IsAbs(options.ManifestFile) || options.Plan.Validate() != nil || options.Plan.Identity != fence.options.Identity {
		return nil, errors.New("explicit bound release plan and ownership composition required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Retain the original disposable lease instead of a caller-owned pointer.
	if options.Plan.Replace != nil {
		lease := *options.Plan.Replace
		options.Plan.Replace = &lease
	}
	identity, hash, err := privateBoundInput(options.ManifestFile)
	if err != nil {
		return nil, err
	}
	if "sha256:"+hex.EncodeToString(hash[:]) != options.ManifestSHA256 {
		return nil, errors.New("verified release manifest bytes differ")
	}
	manifest, _, err := releasecontract.Load(options.ManifestFile, options.Plan.Target)
	if err != nil {
		return nil, errors.New("canonical release manifest contract differs")
	}
	var chartInput *metadataInput
	if filepath.IsAbs(options.ChartReference) {
		bound, err := bindMetadataArtifact(ctx, options.ChartReference, targetChartCompressedMaximum)
		if err != nil || "sha256:"+hex.EncodeToString(bound.hash[:]) != manifest.Astronomer.Chart.ContentDigest {
			return nil, errors.New("local chart archive differs from signed content")
		}
		chartInput = &bound
	}
	input := upgrade.HelmInputs{Tool: upgrade.HelmToolBinding{Path: tool.Path, Version: tool.Version, SHA256: tool.SHA256}, Plan: options.Plan, ChartReference: options.ChartReference, ManifestFile: options.ManifestFile, Manifest: manifest, RuntimeSources: options.RuntimeSources}
	arguments := make(map[upgrade.Stage][]string, 3)
	arguments[upgrade.DryRun], err = input.DryRunArguments()
	if err != nil {
		return nil, err
	}
	if options.Plan.Mode == upgrade.Execute {
		arguments[upgrade.Apply], err = input.ApplyArguments()
		if err != nil {
			return nil, err
		}
		arguments[upgrade.Rollback], err = input.RollbackArguments(fence.options.Revision)
		if err != nil {
			return nil, err
		}
	}
	release := &HelmRelease{fence: fence, tool: tool, plan: options.Plan, manifestFile: options.ManifestFile, manifestIdentity: identity, manifestHash: hash, chartReference: options.ChartReference, chartInput: chartInput, arguments: arguments, execute: fence.commands.Execute}
	if err := release.manifestBinding(); err != nil {
		return nil, err
	}
	if err := release.chartBinding(ctx); err != nil {
		return nil, err
	}
	return release, ctx.Err()
}

// RunStage never reports a failed or cancelled Apply as a known non-mutation.
// CLI rollback-on-failure may itself fail or commit before the acknowledgment.
// A higher Driver must reconcile uncertain results using fresh owned reads.
func (r *HelmRelease) RunStage(ctx context.Context, stage upgrade.Stage) (upgrade.Outcome, error) {
	if ctx == nil || r == nil {
		return upgrade.Outcome{Known: true}, errors.New("explicit release operation context required")
	}
	arguments, exists := r.arguments[stage]
	if !exists {
		return upgrade.Outcome{Known: true}, errors.New("stage is outside the bound Helm lifecycle")
	}
	if err := r.manifestBinding(); err != nil {
		return upgrade.Outcome{Known: true}, err
	}
	if err := r.chartBinding(ctx); err != nil {
		return upgrade.Outcome{Known: true}, err
	}
	if err := r.fence.Fence(ctx, r.plan.Identity, r.plan.Replace); err != nil {
		return upgrade.Outcome{Known: true}, err
	}
	args := append([]string{"--kubeconfig", r.fence.options.Kubeconfig}, arguments...)
	_, _, err := r.execute(ctx, r.tool, args)
	if err != nil {
		return upgrade.Outcome{Known: stage == upgrade.DryRun}, err
	}
	outcome := upgrade.Outcome{Known: true, Applied: stage == upgrade.Apply}
	if err := r.manifestBinding(); err != nil {
		return outcome, err
	}
	if err := r.chartBinding(ctx); err != nil {
		return outcome, err
	}
	// Repeat immutable owner/input checks after the joined acknowledgment. A
	// lost owner fails the higher state machine without resurrecting resources.
	return outcome, r.fence.Fence(ctx, r.plan.Identity, r.plan.Replace)
}

func (r *HelmRelease) manifestBinding() error {
	identity, hash, err := privateBoundInput(r.manifestFile)
	if err != nil {
		return err
	}
	if !os.SameFile(r.manifestIdentity, identity) || r.manifestHash != hash {
		return errors.New("initial release manifest binding changed")
	}
	return nil
}

// chartBinding never authorizes bytes merely because they are local. The
// constructor compared them with the signed manifest, and the complete Driver
// independently verified canonical OCI equality. Every use repeats the initial
// private file identity/hash under the owning context; originals are retained.
func (r *HelmRelease) chartBinding(ctx context.Context) error {
	if ctx == nil || r == nil {
		return errors.New("explicit chart input ownership required")
	}
	if r.chartInput == nil {
		return ctx.Err()
	}
	current, err := bindMetadataArtifact(ctx, r.chartReference, targetChartCompressedMaximum)
	if err != nil || !sameArtifact(r.chartInput.identity, current.identity) || r.chartInput.hash != current.hash {
		return errors.New("initial local chart content or identity changed")
	}
	return ctx.Err()
}
