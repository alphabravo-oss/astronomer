package catalogapp

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func catalogAssetsFixture(t *testing.T) (*Service, InstallRequest) {
	t.Helper()
	dsn := os.Getenv("ASTRONOMER_CATALOG_APPLICATION_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("ASTRONOMER_CATALOG_APPLICATION_TEST_DATABASE_URL is not set")
	}
	admin, err := pgx.Connect(t.Context(), dsn)
	if err != nil {
		t.Fatal("cannot connect to owned catalog test database")
	}
	t.Cleanup(func() { _ = admin.Close(context.Background()) })
	schema := "w08_catalog_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(t.Context(), "CREATE SCHEMA "+identifier); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(context.Background(), "DROP SCHEMA "+identifier+" CASCADE") })
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid owned catalog database configuration")
	}
	config.MaxConns = 6
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal("cannot create catalog test pool")
	}
	t.Cleanup(pool.Close)
	for _, table := range []string{"delivery_sources", "component_bundles", "component_bundle_versions", "delivery_application_versions", "delivery_targets", "helm_chart_versions", "helm_charts", "helm_repositories", "catalog_blessed_charts", "installed_charts", "cluster_deployments"} {
		if _, err := pool.Exec(t.Context(), "CREATE TABLE "+pgx.Identifier{table}.Sanitize()+" (LIKE public."+pgx.Identifier{table}.Sanitize()+" INCLUDING ALL)"); err != nil {
			t.Fatal(err)
		}
	}
	request := InstallRequest{InstallationID: uuid.New(), ProjectID: uuid.New(), ClusterID: uuid.New(), ChartVersionID: uuid.New(), ChartName: "synthetic-chart", ChartVersion: "1.0.0", ChartDigest: strings.Repeat("a", 64), RepositoryURL: "https://example.test/charts", ReleaseName: "synthetic", Namespace: "default", ValuesYAML: "replicas: 1", IdempotencyKey: uuid.NewString()}
	return &Service{pool: pool}, request
}

func TestPostgresCatalogAssetsIdempotencyAndExplicitGeneration(t *testing.T) {
	s, request := catalogAssetsFixture(t)
	seedCuratedCatalogChart(t, s, request)
	first, generation, err := s.ensureAssets(t.Context(), request, false)
	if err != nil || generation != 1 {
		t.Fatalf("first generation=%d error=%v", generation, err)
	}
	second, generation, err := s.ensureAssets(t.Context(), request, false)
	if err != nil || generation != 1 || first != second {
		t.Fatalf("retry identities changed: first=%+v second=%+v generation=%d error=%v", first, second, generation, err)
	}
	third, generation, err := s.ensureAssets(t.Context(), request, true)
	if err != nil || generation != 2 || third != first {
		t.Fatalf("explicit upgrade generation=%d error=%v", generation, err)
	}
	for _, table := range []string{"delivery_sources", "component_bundles", "component_bundle_versions", "delivery_targets", "delivery_application_versions"} {
		var count int
		if err := s.pool.QueryRow(t.Context(), "SELECT count(*) FROM "+pgx.Identifier{table}.Sanitize()).Scan(&count); err != nil || count != 1 {
			t.Fatalf("%s count=%d error=%v", table, count, err)
		}
	}
	var slug, version, digest, verification string
	var bundleVersion uuid.UUID
	if err := s.pool.QueryRow(t.Context(), `SELECT catalog_slug,version,artifact_digest,verification_status,bundle_version_id
		FROM delivery_application_versions WHERE installation_id=$1 AND chart_version_id=$2`, request.InstallationID, request.ChartVersionID).
		Scan(&slug, &version, &digest, &verification, &bundleVersion); err != nil || slug != "synthetic-chart" || version != request.ChartVersion ||
		digest != "sha256:"+request.ChartDigest || verification != "digest-verified" || bundleVersion != first.BundleVersionID {
		t.Fatalf("curated immutable version identity changed: slug=%s version=%s verification=%s bundle=%s error=%v", slug, version, verification, bundleVersion, err)
	}
}

func seedCuratedCatalogChart(t *testing.T, s *Service, request InstallRequest) {
	t.Helper()
	repository, chart := uuid.New(), uuid.New()
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO helm_repositories(id,name,url) VALUES($1,'synthetic',$2)`, []any{repository, request.RepositoryURL}},
		{`INSERT INTO helm_charts(id,repository_id,name) VALUES($1,$2,$3)`, []any{chart, repository, request.ChartName}},
		{`INSERT INTO helm_chart_versions(id,chart_id,version) VALUES($1,$2,$3)`, []any{request.ChartVersionID, chart, request.ChartVersion}},
		{`INSERT INTO catalog_blessed_charts(repo_url,chart_name,source,slug,verification_status)
		  VALUES($1,$2,'catalog-v1','synthetic-chart','digest-verified')`, []any{request.RepositoryURL, request.ChartName}},
	} {
		if _, err := s.pool.Exec(t.Context(), statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPostgresCatalogAssetsUncuratedChartDoesNotInventVersionLedger(t *testing.T) {
	s, request := catalogAssetsFixture(t)
	if _, _, err := s.ensureAssets(t.Context(), request, false); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.pool.QueryRow(t.Context(), `SELECT count(*) FROM delivery_application_versions`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("uncurated chart created a curated version: count=%d error=%v", count, err)
	}
}

func TestPostgresCatalogAssetsLateValidationRollsBackPriorWrites(t *testing.T) {
	s, request := catalogAssetsFixture(t)
	request.ValuesYAML = "[invalid: yaml"
	if _, _, err := s.ensureAssets(t.Context(), request, false); err == nil {
		t.Fatal("invalid values accepted")
	}
	for _, table := range []string{"delivery_sources", "component_bundles", "component_bundle_versions", "delivery_targets"} {
		var count int
		if err := s.pool.QueryRow(t.Context(), "SELECT count(*) FROM "+pgx.Identifier{table}.Sanitize()).Scan(&count); err != nil || count != 0 {
			t.Fatalf("rolled back %s count=%d error=%v", table, count, err)
		}
	}
}
