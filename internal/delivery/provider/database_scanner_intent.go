package provider

import (
	"context"
	"errors"

	builtinbundles "github.com/alphabravocompany/astronomer-go/deploy/bundles"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/builtin"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/rollout"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// This private DATA is compiled and locked by the same canonical component and
// rollout paths used by same-feed refresh and new-update acceptance. The caller
// must already hold the owned cluster session/global authority locks; no public
// request can supply a desired generation, effective digest or assignment.
type databaseScannerIntent struct {
	expected   builtin.CatalogComponentExpectation
	effective  rollout.UnconfiguredDeploymentIntent
	generation int64
	deployment sqlc.LockVulnerabilityDatabaseScannerDeploymentRow
}

func lockDatabaseScannerIntent(ctx context.Context, queries *sqlc.Queries, cluster uuid.UUID, catalog builtinbundles.Catalog) (databaseScannerIntent, error) {
	slug := ""
	for _, component := range catalog.Components {
		if component.Source.Chart == "trivy-operator" {
			slug = component.Slug
		}
	}
	expected, err := builtin.ExpectedCatalogComponent(catalog, cluster, slug)
	if err != nil {
		return databaseScannerIntent{}, err
	}
	bundleDigest, err := model.ParseDigest(expected.Version.SpecDigest)
	if err != nil {
		return databaseScannerIntent{}, err
	}
	effective, err := rollout.ResolveUnconfiguredDeployment(bundleDigest, expected.Version.RendererSpec)
	if err != nil {
		return databaseScannerIntent{}, err
	}
	generation, err := queries.LockVulnerabilityDatabaseScannerTarget(ctx, sqlc.LockVulnerabilityDatabaseScannerTargetParams{
		ClusterID: cluster, ProjectID: expected.ProjectID, BundleID: expected.BundleID, SourceID: expected.SourceID,
		SourceName: expected.Source.Name, SourceDescription: expected.Source.Description, SourceType: string(expected.Source.Type), SourceUrl: expected.Source.URL,
		SourceSpec: expected.Version.SourceSpec, VersionID: expected.VersionID, VersionLabel: expected.Version.Version,
		ChartVersion: expected.Version.RequestedRevision, ChartDigest: expected.Version.ArtifactDigest,
		BundleSpecDigest: expected.Version.SpecDigest, RendererSpec: expected.Version.RendererSpec,
		ReconciliationPolicy: expected.Version.ReconciliationPolicy, Requirements: expected.Version.Requirements,
		VerificationIdentity: expected.Version.VerificationIdentity, TargetID: expected.TargetID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return databaseScannerIntent{}, errors.New("database snapshot current scanner target failed exact validation")
	}
	if err != nil {
		return databaseScannerIntent{}, err
	}
	deployment, err := queries.LockVulnerabilityDatabaseScannerDeployment(ctx, sqlc.LockVulnerabilityDatabaseScannerDeploymentParams{
		ClusterID: cluster, TargetID: expected.TargetID, VersionID: pgtype.UUID{Bytes: expected.VersionID, Valid: true},
		EffectiveSpecDigest: effective.SpecDigest.String(), EffectiveRenderer: effective.Renderer,
		ConfigurationDigest: pgtype.Text{String: effective.ConfigurationDigest.String(), Valid: true}, ChartVersion: expected.Version.RequestedRevision,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return databaseScannerIntent{}, errors.New("database snapshot current scanner deployment failed exact validation")
	}
	if err != nil {
		return databaseScannerIntent{}, err
	}
	return databaseScannerIntent{expected: expected, effective: effective, generation: generation, deployment: deployment}, nil
}
