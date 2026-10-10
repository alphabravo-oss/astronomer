//go:build unix && integration

package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	builtinbundles "github.com/alphabravocompany/astronomer-go/deploy/bundles"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/builtin"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/rollout"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// These tests require the existing real signed QA graph and exact runner-created
// PG17/18 database lease fence. All fleet rows are confined to a rolled-back real
// transaction. Discovery is DATA; no accepted DB state or scan witness is minted.
func TestDatabaseRefreshPostgresDisabledBeforeEnabledAndFirstAdoption(t *testing.T) {
	fixture := openDatabaseRestartPostgresFixture(t)
	runDatabaseRefreshDiscoveryTransaction(t, fixture, func(ctx context.Context, q *sqlc.Queries, catalog builtinbundles.Catalog, slug string) {
		first, second := createDatabaseRefreshCluster(t, ctx, q), createDatabaseRefreshCluster(t, ctx, q)
		if bytes.Compare(first[:], second[:]) > 0 {
			first, second = second, first
		}
		materializeDatabaseRefreshScanner(t, ctx, q, catalog, first, slug, true)
		materializeDatabaseRefreshScanner(t, ctx, q, catalog, second, slug, true)
		if _, err := q.UpdateCluster(ctx, sqlc.UpdateClusterParams{ID: first, DisplayName: "owned disabled fixture", Environment: "development", Labels: json.RawMessage(`{}`), Annotations: json.RawMessage(`{"astronomer.io/image-scanning":"disabled"}`), AgentOverrides: json.RawMessage(`{}`)}); err != nil {
			t.Fatal("fixture opt-out annotation failed")
		}
		// An entirely new cluster, or target without a deployment, must wait for
		// ordinary provider/rollout materialization rather than poison the sweep.
		createDatabaseRefreshCluster(t, ctx, q)
		notDeployed := createDatabaseRefreshCluster(t, ctx, q)
		materializeDatabaseRefreshScanner(t, ctx, q, catalog, notDeployed, slug, false)
		params := sqlc.ListVulnerabilityDatabaseRefreshClustersParams{AfterID: uuid.Nil, ScannerName: "astronomer-builtins-" + slug, QueryLimit: 1}
		ids, err := q.ListVulnerabilityDatabaseRefreshClusters(ctx, params)
		if err != nil || len(ids) != 1 || ids[0] != second {
			t.Fatal("disabled earlier UUID or unmaterialized cluster blocked enabled discovery")
		}
		if _, err := lockDatabaseScannerIntent(ctx, q, second, catalog); err != nil {
			t.Fatal("discovered enabled scanner did not pass actual locked canonical validation")
		}
		params.AfterID = second
		ids, err = q.ListVulnerabilityDatabaseRefreshClusters(ctx, params)
		if err != nil || len(ids) != 0 {
			t.Fatal("bounded cursor rediscovered or included opted-out/unmaterialized cluster")
		}
		// The next sweep includes a previously deferred cluster once its actual
		// deployment exists. No feed-generation or adoption authority is inferred.
		materializeDatabaseRefreshScanner(t, ctx, q, catalog, notDeployed, slug, true)
		params.AfterID, params.QueryLimit = uuid.Nil, 100
		ids, err = q.ListVulnerabilityDatabaseRefreshClusters(ctx, params)
		if err != nil || len(ids) != 2 || !containsDatabaseRefreshCluster(ids, second) || !containsDatabaseRefreshCluster(ids, notDeployed) {
			t.Fatal("ordinary materialization did not make deferred scanner discoverable")
		}
	})
}

func TestDatabaseRefreshPostgresInvalidMaterializedSourceIsNotSkipped(t *testing.T) {
	fixture := openDatabaseRestartPostgresFixture(t)
	runDatabaseRefreshDiscoveryTransaction(t, fixture, func(ctx context.Context, q *sqlc.Queries, catalog builtinbundles.Catalog, slug string) {
		cluster := createDatabaseRefreshCluster(t, ctx, q)
		expected := materializeDatabaseRefreshScanner(t, ctx, q, catalog, cluster, slug, true)
		if _, err := lockDatabaseScannerIntent(ctx, q, cluster, catalog); err != nil {
			t.Fatal("unchanged materialized fixture failed canonical locked validation")
		}
		// Mutate the real row, not a mocked query result or a signed input. The
		// discovery query must return it; only the locked authority check rejects it.
		if _, err := q.UpdateDeliverySourceStatus(ctx, sqlc.UpdateDeliverySourceStatusParams{ID: expected.SourceID, Status: "degraded", LastErrorCode: "fixture-source-invalid"}); err != nil {
			t.Fatal("actual source status mutation failed")
		}
		ids, err := q.ListVulnerabilityDatabaseRefreshClusters(ctx, sqlc.ListVulnerabilityDatabaseRefreshClustersParams{AfterID: uuid.Nil, ScannerName: "astronomer-builtins-" + slug, QueryLimit: 100})
		if err != nil || len(ids) != 1 || ids[0] != cluster {
			t.Fatal("invalid materialized source was silently hidden by discovery")
		}
		if _, err := lockDatabaseScannerIntent(ctx, q, cluster, catalog); err == nil || !strings.Contains(err.Error(), "scanner target failed exact validation") {
			t.Fatal("actual invalid source escaped canonical locked validation")
		}
	})
}

func runDatabaseRefreshDiscoveryTransaction(t *testing.T, fixture *databaseRestartPostgresFixture, run func(context.Context, *sqlc.Queries, builtinbundles.Catalog, string)) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	proof, err := fixture.authority.Base(ctx)
	if err != nil {
		t.Fatal("actual signed base unavailable")
	}
	catalog, err := fixture.authority.Catalog(ctx, proof)
	if err != nil {
		t.Fatal("actual signed catalog unavailable")
	}
	slug := ""
	for _, component := range catalog.Components {
		if component.Source.Chart == "trivy-operator" {
			if slug != "" {
				t.Fatal("fixture has ambiguous scanner components")
			}
			slug = component.Slug
		}
	}
	if slug == "" {
		t.Fatal("fixture signed catalog needs real scanner component")
	}
	conn, err := fixture.pool.Acquire(ctx)
	if err != nil {
		t.Fatal("owned fixture backend unavailable")
	}
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		conn.Release()
		t.Fatal("owned fixture transaction unavailable")
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if err := tx.Rollback(cleanup); err != nil {
			_ = conn.Hijack().Close(cleanup)
			t.Error("owned discovery transaction rollback failed; backend closed")
		} else {
			conn.Release()
		}
		fixture.assertBackendReleased(t)
	}()
	run(ctx, sqlc.New(tx), catalog, slug)
}

func createDatabaseRefreshCluster(t *testing.T, ctx context.Context, q *sqlc.Queries) uuid.UUID {
	t.Helper()
	cluster, err := q.CreateCluster(ctx, sqlc.CreateClusterParams{Name: "plan032-refresh-" + uuid.NewString(), DisplayName: "owned refresh fixture", Environment: "development", Provider: "other", Labels: json.RawMessage(`{}`), Annotations: json.RawMessage(`{}`), AgentOverrides: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal("owned fixture cluster creation failed")
	}
	return cluster.ID
}

func materializeDatabaseRefreshScanner(t *testing.T, ctx context.Context, q *sqlc.Queries, catalog builtinbundles.Catalog, cluster uuid.UUID, slug string, deployed bool) builtin.CatalogComponentExpectation {
	t.Helper()
	expected, err := builtin.ExpectedCatalogComponent(catalog, cluster, slug)
	if err != nil {
		t.Fatal("canonical compiler fixture failed")
	}
	trust, err := json.Marshal(expected.Source.Trust)
	if err != nil {
		t.Fatal("fixture trust encoding failed")
	}
	placement, err := json.Marshal(model.Placement{ProjectIDs: []uuid.UUID{expected.ProjectID}, ClusterIDs: []uuid.UUID{cluster}})
	if err != nil {
		t.Fatal("fixture placement encoding failed")
	}
	for _, err := range []error{
		q.EnsureBuiltinSystemProject(ctx, sqlc.EnsureBuiltinSystemProjectParams{ID: expected.ProjectID, Name: "astronomer-system", ClusterID: cluster}),
		q.EnsureBuiltinCatalogSource(ctx, sqlc.EnsureBuiltinCatalogSourceParams{ID: expected.SourceID, ProjectID: expected.ProjectID, Name: expected.Source.Name, Description: expected.Source.Description, SourceType: string(expected.Source.Type), Url: expected.Source.URL, TrustPolicy: trust}),
		q.EnsureBuiltinComponentBundle(ctx, sqlc.EnsureBuiltinComponentBundleParams{ID: expected.BundleID, ProjectID: expected.ProjectID, Name: "astronomer-builtins-" + slug, Description: "owned scanner fixture"}),
		q.EnsureBuiltinComponentVersion(ctx, expected.Version),
	} {
		if err != nil {
			t.Fatal("actual canonical fixture materialization failed")
		}
	}
	generation, err := q.EnsureBuiltinComponentTarget(ctx, sqlc.EnsureBuiltinComponentTargetParams{ID: expected.TargetID, ProjectID: expected.ProjectID, Name: "astronomer-builtins-" + slug, BundleVersionID: expected.VersionID, Placement: placement, RolloutPolicy: json.RawMessage(`{"approval_required":false}`), ReconciliationPolicy: expected.Version.ReconciliationPolicy})
	if err != nil {
		t.Fatal("actual fixture target materialization failed")
	}
	if deployed {
		bundleDigest, err := model.ParseDigest(expected.Version.SpecDigest)
		if err != nil {
			t.Fatal("canonical fixture digest invalid")
		}
		effective, err := rollout.ResolveUnconfiguredDeployment(bundleDigest, expected.Version.RendererSpec)
		if err != nil {
			t.Fatal("canonical effective fixture renderer failed")
		}
		_, err = q.UpsertClusterDeploymentDesired(ctx, sqlc.UpsertClusterDeploymentDesiredParams{TargetID: expected.TargetID, ClusterID: cluster, DesiredBundleVersionID: pgtype.UUID{Bytes: expected.VersionID, Valid: true}, DesiredGeneration: generation, DesiredSpecDigest: effective.SpecDigest.String(), DesiredOverrides: json.RawMessage(`{}`), DesiredRevision: expected.Version.RequestedRevision, DesiredRendererSpec: effective.Renderer, DesiredConfigurationDigest: pgtype.Text{String: effective.ConfigurationDigest.String(), Valid: true}, Action: "apply", Phase: "pending"})
		if err != nil {
			t.Fatal("actual fixture deployment materialization failed")
		}
	}
	return expected
}

func containsDatabaseRefreshCluster(ids []uuid.UUID, want uuid.UUID) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}
