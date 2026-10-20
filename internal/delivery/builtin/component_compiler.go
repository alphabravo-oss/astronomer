package builtin

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	builtinbundles "github.com/alphabravocompany/astronomer-go/deploy/bundles"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
)

// compileComponentVersion is the same pure compiler used by ordinary builtin
// persistence and optional DB expectation data. It grants no authorization and
// never creates a desired generation, assignment receipt or scan-used witness.
func compileComponentVersion(bundleID uuid.UUID, source sourceIdentity, release string, component builtinbundles.Component, trust model.TrustPolicy, distributionIdentity, verificationIdentity string) (sqlc.EnsureBuiltinComponentVersionParams, error) {
	valuesJSON, err := json.Marshal(component.Values)
	if err != nil {
		return sqlc.EnsureBuiltinComponentVersionParams{}, fmt.Errorf("encode built-in values %s: %w", component.Slug, err)
	}
	reconciliation := model.ReconciliationPolicy{
		Interval: model.Duration(5 * time.Minute), RetryInterval: model.Duration(30 * time.Second),
		Timeout: model.Duration(10 * time.Minute), Prune: true, Wait: true, Drift: model.DriftRepair,
	}
	requirements := make([]model.CapabilityRequirement, 0, len(component.Requirements.Capabilities))
	for _, capability := range component.Requirements.Capabilities {
		requirements = append(requirements, model.CapabilityRequirement{Name: capability})
	}
	draft := model.BundleVersionDraft{
		SourceID: source.id, RequestedRevision: component.Source.Version, Scope: model.ScopePlatform,
		Renderer: model.RendererSpec{Kind: model.RendererHelm, Helm: &model.HelmSpec{
			Chart: component.Source.Chart, ChartVersion: component.Source.Version,
			ReleaseName: component.ReleaseName, TargetNamespace: component.TargetNamespace,
			Values: valuesJSON, InstallRetries: 3, UpgradeRetries: 3,
		}},
		Reconciliation: reconciliation, RequiredCapabilities: requirements,
	}
	artifactDigest, err := model.ParseDigest(component.Source.ChartDigest)
	if err != nil {
		return sqlc.EnsureBuiltinComponentVersionParams{}, err
	}
	revision := model.ImmutableRevision{Kind: model.RevisionHelmChart, Value: component.Source.Version, ArtifactDigest: artifactDigest}
	resolved, err := draft.Resolve(revision)
	if err != nil {
		return sqlc.EnsureBuiltinComponentVersionParams{}, fmt.Errorf("validate built-in component %s: %w", component.Slug, err)
	}
	specDigest, err := model.CanonicalDigest(struct {
		Spec         model.BundleVersionSpec `json:"spec"`
		Dependencies []uuid.UUID             `json:"dependency_bundle_ids"`
	}{resolved, []uuid.UUID{}})
	if err != nil {
		return sqlc.EnsureBuiltinComponentVersionParams{}, err
	}
	resolvedSource := model.ResolvedSourceSpec{
		SourceID: source.id, Type: sourceType(source),
		URL: source.url, AuthMode: model.AuthNone,
		Trust: trust, Revision: revision,
	}
	sourceJSON, _ := json.Marshal(resolvedSource)
	rendererJSON, _ := json.Marshal(draft.Renderer)
	reconciliationJSON, _ := json.Marshal(reconciliation)
	requirementsJSON, _ := json.Marshal(requirements)
	versionID, versionLabel := componentVersionIdentity(bundleID, release, component, distributionIdentity)
	verificationStatus := "unsigned"
	if distributionIdentity != "" {
		verificationStatus = "verified"
		verificationIdentity += ";distribution:" + distributionIdentity + ";upstream:" + component.Source.OriginalURL + ";archive:" + component.Source.OriginalArchiveDigest
	} else {
		verificationIdentity = "source-development:" + release
	}
	return sqlc.EnsureBuiltinComponentVersionParams{
		ID: versionID, BundleID: bundleID, SourceID: source.id, Version: versionLabel,
		RequestedRevision: component.Source.Version, ArtifactDigest: component.Source.ChartDigest,
		SourceSpec: sourceJSON, RendererSpec: rendererJSON, ReconciliationPolicy: reconciliationJSON,
		Requirements: requirementsJSON, SpecDigest: specDigest.String(), VerificationStatus: verificationStatus,
		VerificationIdentity: verificationIdentity,
	}, nil
}

// CatalogComponentExpectation is compilation DATA. It is not a verified proof
// or permission to accept/update a DB. The private durable owner must obtain its
// catalog through current opaque physical authority, lock real source/target/
// renderer rows, and compare them in the owned transaction before use.
type CatalogComponentExpectation struct {
	ProjectID, SourceID, BundleID, VersionID, TargetID uuid.UUID
	Source                                             model.Source
	Version                                            sqlc.EnsureBuiltinComponentVersionParams
}

func ExpectedCatalogComponent(catalog builtinbundles.Catalog, clusterID uuid.UUID, slug string) (CatalogComponentExpectation, error) {
	if clusterID == uuid.Nil || catalog.Validate() != nil {
		return CatalogComponentExpectation{}, errors.New("builtin compilation inputs invalid")
	}
	projectID := stableID("project", clusterID.String())
	for _, component := range catalog.Components {
		if component.Slug != slug {
			continue
		}
		source, err := catalogSourceIdentityForComponent(projectID, component, catalog.DistributionIdentity)
		if err != nil {
			return CatalogComponentExpectation{}, err
		}
		trust := catalogTrustPolicy(catalog)
		definition := model.Source{ID: source.id, ProjectID: projectID, Name: source.name, Description: sourceDescription(source), Type: sourceType(source), URL: source.url, AuthMode: model.AuthNone, Trust: trust}
		if err := definition.Validate(); err != nil {
			return CatalogComponentExpectation{}, fmt.Errorf("validate built-in source %s: %w", source.name, err)
		}
		bundleID := stableID("bundle", projectID.String(), component.Slug)
		version, err := compileComponentVersion(bundleID, source, catalog.Release, component, trust, catalog.DistributionIdentity, catalog.VerificationIdentity)
		if err != nil {
			return CatalogComponentExpectation{}, err
		}
		return CatalogComponentExpectation{ProjectID: projectID, SourceID: source.id, BundleID: bundleID, VersionID: version.ID, TargetID: stableID("target", clusterID.String(), component.Slug), Source: definition, Version: version}, nil
	}
	return CatalogComponentExpectation{}, errors.New("builtin compilation component unavailable")
}
