//go:build linux

package upgradecli

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/alphabravocompany/astronomer-go/pkg/safeconv"

	"github.com/alphabravocompany/astronomer-go/internal/jsoncontract"
	"github.com/alphabravocompany/astronomer-go/internal/ops/upgrade"
	"github.com/alphabravocompany/astronomer-go/internal/releasecontract"
)

// HelmStateObservation reads the latest actual release through the bound Helm
// CLI. It does not grant signature authority or claim that matching metadata
// proves every workload image, /readyz, database replacement or upgrade parity.
// An interrupted operation is assessed without replaying its mutating command.
type HelmStateObservation struct {
	release   *HelmRelease
	readiness *Readiness
	source    *SourceDatabaseBackup
}

// helmMetadata follows the exact upstream Helm 4.3 GetMetadata JSON contract.
// Unused chart annotations, dependencies and release labels remain private and
// are never copied to public diagnostics. The decoder rejects duplicate fields,
// extra documents and unknown top-level fields before interpreting identity.
type helmMetadata struct {
	Name         string          `json:"name"`
	Chart        string          `json:"chart"`
	Version      string          `json:"version"`
	AppVersion   string          `json:"appVersion"`
	Namespace    string          `json:"namespace"`
	Revision     *uint64         `json:"revision"`
	Status       string          `json:"status"`
	DeployedAt   string          `json:"deployedAt"`
	ApplyMethod  string          `json:"applyMethod,omitempty"`
	Annotations  json.RawMessage `json:"annotations,omitempty"`
	Labels       json.RawMessage `json:"labels,omitempty"`
	Dependencies json.RawMessage `json:"dependencies,omitempty"`
}

type ReleaseState struct {
	Revision           uint64
	ChartVersion       string
	ApplicationVersion string
	Status             string
}

func NewHelmStateObservation(release *HelmRelease, readiness *Readiness, source *SourceDatabaseBackup) (*HelmStateObservation, error) {
	if release == nil || release.fence == nil || release.plan.Validate() != nil || readiness == nil || readiness.fence != release.fence || (source != nil && source.fence != release.fence) {
		return nil, errors.New("one initially bound release and readiness/database composition required")
	}
	if err := release.manifestBinding(); err != nil {
		return nil, err
	}
	manifest, _, err := releasecontract.Load(release.manifestFile, release.plan.Target)
	if err != nil {
		return nil, errors.New("initial canonical target manifest differs")
	}
	if source != nil {
		pg := manifest.Compatibility.PostgreSQL
		if uint64(source.options.MinimumSchema) != safeconv.Uint64(pg.MinimumUpgradeSchema) || uint64(source.options.TargetSchema) != safeconv.Uint64(pg.TargetSchema) {
			return nil, errors.New("database schema bounds differ from the exact target manifest")
		}
		supported := false
		for _, major := range pg.SupportedMajors {
			if safeconv.Uint64(major) == uint64(source.options.ExpectedMajor) {
				supported = true
			}
		}
		if !supported {
			return nil, errors.New("database major is outside the exact target manifest")
		}
	}
	// A nil database adapter means metadata/readiness only, never an implicit
	// external backup or external schema approval. Full Driver must bind that
	// separate operator contract before it can complete an external-DB Observe.
	return &HelmStateObservation{release: release, readiness: readiness, source: source}, nil
}

func (o *HelmStateObservation) Read(ctx context.Context) (ReleaseState, error) {
	var empty ReleaseState
	if ctx == nil || o == nil {
		return empty, errors.New("explicit release observation context and composition required")
	}
	r := o.release
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	if err := r.manifestBinding(); err != nil {
		return empty, err
	}
	if err := r.fence.Fence(ctx, r.plan.Identity, r.plan.Replace); err != nil {
		return empty, err
	}
	args := []string{"--kubeconfig", r.fence.options.Kubeconfig, "--kube-context", r.plan.Identity.Context, "get", "metadata", r.plan.Identity.Release, "--namespace", r.plan.Identity.Namespace, "--output", "json"}
	raw, _, err := r.execute(ctx, r.tool, args)
	if err != nil {
		return empty, err
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	state, err := parseHelmMetadata(raw, r.plan.Identity)
	if err != nil {
		return empty, err
	}
	if err := r.manifestBinding(); err != nil {
		return empty, err
	}
	if err := r.fence.Fence(ctx, r.plan.Identity, r.plan.Replace); err != nil {
		return empty, err
	}
	return state, ctx.Err()
}

func parseHelmMetadata(raw []byte, id upgrade.Identity) (ReleaseState, error) {
	var empty ReleaseState
	if len(raw) == 0 || len(raw) > 1<<20 {
		return empty, errors.New("release metadata byte bound exceeded")
	}
	var metadata helmMetadata
	if jsoncontract.Decode(raw, &metadata) != nil {
		return empty, errors.New("release metadata JSON contract differs")
	}
	if metadata.Name != id.Release || metadata.Namespace != id.Namespace || metadata.Chart != "astronomer" || metadata.Revision == nil || *metadata.Revision == 0 {
		return empty, errors.New("actual Helm release identity or revision differs")
	}
	for _, value := range []string{metadata.Version, metadata.AppVersion, metadata.Status} {
		if value == "" || len(value) > 128 || strings.ContainsAny(value, "\t\r\n\x00") {
			return empty, errors.New("actual Helm metadata field differs")
		}
	}
	switch metadata.Status {
	case "deployed", "superseded", "uninstalled", "failed", "uninstalling", "pending-install", "pending-upgrade", "pending-rollback":
	default:
		return empty, errors.New("actual Helm lifecycle status is unsupported")
	}
	return ReleaseState{Revision: *metadata.Revision, ChartVersion: metadata.Version, ApplicationVersion: metadata.AppVersion, Status: metadata.Status}, nil
}

func releaseVersion(state ReleaseState, version string) bool {
	return state.ChartVersion == strings.TrimPrefix(version, "v") && strings.TrimPrefix(state.ApplicationVersion, "v") == strings.TrimPrefix(version, "v")
}

// ReconcileHelm deliberately leaves replacement and pending/failed/new-foreign
// release state unknown. A declared revision or failed child is insufficient to
// infer absence of mutation. The higher Driver must retain recovery material.
// These are repeated point-in-time observations, not an atomic cluster lock.
func (o *HelmStateObservation) ReconcileHelm(ctx context.Context) (upgrade.Outcome, error) {
	return o.reconcileHelm(ctx, false)
}

// Only the complete Driver calls this after independent retained replacement OID assessment.
func (o *HelmStateObservation) reconcileHelm(ctx context.Context, replacementAssessed bool) (upgrade.Outcome, error) {
	first, err := o.Read(ctx)
	if err != nil {
		return upgrade.Outcome{}, err
	}
	second, err := o.Read(ctx)
	if err != nil {
		return upgrade.Outcome{}, err
	}
	if first != second {
		return upgrade.Outcome{}, errors.New("Helm state changed during mutation assessment")
	}
	r := o.release
	if r.plan.Replace != nil && !replacementAssessed {
		return upgrade.Outcome{}, errors.New("database replacement requires its own mutation assessment")
	}
	if first.Status != "deployed" {
		return upgrade.Outcome{}, errors.New("Helm mutation outcome remains unresolved")
	}
	if first.Revision == r.fence.options.Revision && releaseVersion(first, r.plan.Previous) {
		return upgrade.Outcome{Known: true}, nil
	}
	if first.Revision > r.fence.options.Revision && releaseVersion(first, r.plan.Target) {
		return upgrade.Outcome{Known: true, Applied: true}, nil
	}
	// Even a newer deployed previous-version revision may be CLI automatic
	// rollback after a partially applied mutation. Do not misreport no mutation.
	return upgrade.Outcome{}, errors.New("Helm revision/version requires retained recovery assessment")
}

// ConfirmTarget supplies only metadata, existing readiness/PDB and the bound
// bundled database's exact clean target-schema observations. Full Observe must
// additionally verify workload images, agent configuration and actual /readyz.
func (o *HelmStateObservation) ConfirmTarget(ctx context.Context) error {
	return o.confirmTarget(ctx, nil)
}

// The optional target observer is independently captured before mutation. The
// public original-source observation retains its initial Pod UID semantics.
func (o *HelmStateObservation) confirmTarget(ctx context.Context, target *TargetDatabaseObservation) error {
	if target != nil && (o == nil || target.source != o.source) {
		return errors.New("target database observation owner differs")
	}
	first, err := o.Read(ctx)
	if err != nil {
		return err
	}
	r := o.release
	if first.Status != "deployed" || first.Revision <= r.fence.options.Revision || !releaseVersion(first, r.plan.Target) {
		return errors.New("target release is not the observed deployed revision")
	}
	if err := o.readiness.Check(ctx); err != nil {
		return err
	}
	if o.source != nil {
		var state DatabaseSchemaState
		var err error
		if target == nil {
			state, err = o.source.observe(ctx)
		} else {
			state, err = target.Observe(ctx)
		}
		if err != nil {
			return err
		}
		if state.Version != o.source.options.TargetSchema {
			return errors.New("post-upgrade database is not at the exact clean target schema")
		}
	}
	final, err := o.Read(ctx)
	if err != nil {
		return err
	}
	if final != first {
		return errors.New("deployed target changed during post-upgrade observation")
	}
	return ctx.Err()
}
