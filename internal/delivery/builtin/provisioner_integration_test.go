//go:build integration

package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	builtinbundles "github.com/alphabravocompany/astronomer-go/deploy/bundles"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"

	"github.com/alphabravocompany/astronomer-go/internal/testdb"
)

// This opt-in test owns an isolated PostgreSQL schema and exercises the actual
// serializable transaction, deterministic rows, conflict rollback, and
// concurrent retry behavior used by the built-in provisioner.
//nolint:tparallel // subtests run sequentially against one shared per-test database fixture
func TestProvisionerPostgresMultiSourceAssets(t *testing.T) {
	t.Parallel()
	dsn := testdb.DSN(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool := openBuiltinProvisionerTestPool(t, ctx, dsn)
	defer pool.Close()
	provisioner := &Provisioner{pool: pool}

	t.Run("same source retry stability and current compatibility", func(t *testing.T) {
		catalog := loadBuiltinTestCatalog(t)
		clusterID := uuid.New()
		first, err := provisioner.ensureAssets(ctx, clusterID, catalog)
		if err != nil {
			t.Fatal(err)
		}
		second, err := provisioner.ensureAssets(ctx, clusterID, catalog)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(first, second) {
			t.Fatalf("retry changed target identities: first=%+v second=%+v", first, second)
		}
		projectID := stableID("project", clusterID.String())
		assertBuiltinProjectCounts(t, ctx, pool, projectID, 1, 2, 2, 2)
		var sourceID uuid.UUID
		var sourceName, sourceURL string
		if err := pool.QueryRow(ctx, `SELECT id,name,url FROM delivery_sources WHERE project_id=$1`, projectID).
			Scan(&sourceID, &sourceName, &sourceURL); err != nil {
			t.Fatal(err)
		}
		if sourceID != stableID("source", projectID.String(), systemSourceName) || sourceName != systemSourceName || sourceURL != systemSourceURL {
			t.Fatalf("current source compatibility changed: id=%s name=%s url=%s", sourceID, sourceName, sourceURL)
		}
	})

	t.Run("multiple sources bind each component", func(t *testing.T) {
		catalog := loadBuiltinTestCatalog(t)
		secondURL := "https://charts.example.test/stable"
		catalog.Components[1].Source.URL = secondURL
		clusterID := uuid.New()
		if _, err := provisioner.ensureAssets(ctx, clusterID, catalog); err != nil {
			t.Fatal(err)
		}
		projectID := stableID("project", clusterID.String())
		assertBuiltinProjectCounts(t, ctx, pool, projectID, 2, 2, 2, 2)
		rows, err := pool.Query(ctx, `
			SELECT b.name,s.url,v.source_spec
			FROM component_bundle_versions v
			JOIN component_bundles b ON b.id=v.bundle_id
			JOIN delivery_sources s ON s.id=v.source_id
			WHERE b.project_id=$1 ORDER BY b.name`, projectID)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		got := make(map[string]string)
		for rows.Next() {
			var bundleName, sourceURL string
			var sourceSpecJSON []byte
			if err := rows.Scan(&bundleName, &sourceURL, &sourceSpecJSON); err != nil {
				t.Fatal(err)
			}
			var sourceSpec model.ResolvedSourceSpec
			if err := json.Unmarshal(sourceSpecJSON, &sourceSpec); err != nil {
				t.Fatal(err)
			}
			if sourceSpec.URL != sourceURL || sourceSpec.AuthMode != model.AuthNone {
				t.Fatalf("bundle %s source projection = %+v, row URL=%s", bundleName, sourceSpec, sourceURL)
			}
			got[bundleName] = sourceURL
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		want := map[string]string{
			"astronomer-builtins-" + catalog.Components[0].Slug: systemSourceURL,
			"astronomer-builtins-" + catalog.Components[1].Slug: secondURL,
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("component source bindings = %v, want %v", got, want)
		}
	})

	t.Run("source conflict rolls back dependent assets", func(t *testing.T) {
		catalog := loadBuiltinTestCatalog(t)
		clusterID := uuid.New()
		projectID := stableID("project", clusterID.String())
		if _, err := pool.Exec(ctx, `
			INSERT INTO projects (id,name,display_name,description,cluster_id,namespaces,resource_quota,managed_by)
			VALUES ($1,$2,'Astronomer System','Flux-managed platform components',$3,'[]','{}','system')`,
			projectID, systemProjectName, clusterID); err != nil {
			t.Fatal(err)
		}
		trustJSON, _ := json.Marshal(model.TrustPolicy{AllowUnsigned: true})
		legacyID := stableID("source", projectID.String(), systemSourceName)
		if _, err := pool.Exec(ctx, `
			INSERT INTO delivery_sources
			(id,project_id,name,description,source_type,url,auth_mode,trust_policy,status,last_resolved_at)
			VALUES ($1,$2,$3,$4,'helm_http','https://charts.conflict.test/stable','none',$5,'ready',now())`,
			legacyID, projectID, systemSourceName, systemSourceDescription, trustJSON); err != nil {
			t.Fatal(err)
		}
		if _, err := provisioner.ensureAssets(ctx, clusterID, catalog); err == nil {
			t.Fatal("conflicting deterministic source was accepted")
		}
		assertBuiltinProjectCounts(t, ctx, pool, projectID, 1, 0, 0, 0)
	})

	t.Run("verified mirror transition retains accepted history and stable ownership", func(t *testing.T) {
		catalog := loadBuiltinTestCatalog(t)
		clusterID := uuid.New()
		initial, err := provisioner.ensureAssets(ctx, clusterID, catalog)
		if err != nil {
			t.Fatal(err)
		}
		projectID := stableID("project", clusterID.String())
		catalog.DistributionIdentity = "sha256:" + strings.Repeat("a", 64) + ";mirror:sha256:" + strings.Repeat("b", 64)
		catalog.VerificationProvider = string(model.SignatureCosignKey)
		catalog.VerificationKeyRef = "local-qa"
		// Key mode has no OIDC identity/issuer; the mounted key_ref is its authority.
		catalog.VerificationIdentity = ""
		for index := range catalog.Components {
			c := &catalog.Components[index]
			c.Source.OriginalURL = c.Source.URL
			c.Source.OriginalArchiveDigest = c.Source.ChartDigest
			c.Source.URL = "oci://registry.corp.local/charts"
			c.Source.Kind = "helm_oci"
			c.Source.ChartDigest = "sha256:" + strings.Repeat(fmt.Sprint(index+1), 64)
		}
		transitioned, err := provisioner.ensureAssets(ctx, clusterID, catalog)
		if err != nil {
			t.Fatal(err)
		}
		retry, err := provisioner.ensureAssets(ctx, clusterID, catalog)
		if err != nil || !reflect.DeepEqual(retry, transitioned) {
			t.Fatalf("retry did not converge: %v", err)
		}
		for index := range initial {
			if initial[index].id != transitioned[index].id || transitioned[index].generation != initial[index].generation+1 {
				t.Fatal("mirror transition changed ownership or failed generation fence")
			}
		}
		assertBuiltinProjectCounts(t, ctx, pool, projectID, 2, 2, 4, 2)
		rows, err := pool.Query(ctx, `SELECT s.url,s.source_type,v.source_spec,v.renderer_spec,v.verification_status,v.verification_identity FROM delivery_targets t JOIN component_bundle_versions v ON v.id=t.bundle_version_id JOIN delivery_sources s ON s.id=v.source_id WHERE t.project_id=$1`, projectID)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		count := 0
		for rows.Next() {
			var location, kind, status, identity string
			var sourceJSON, rendererJSON []byte
			if err := rows.Scan(&location, &kind, &sourceJSON, &rendererJSON, &status, &identity); err != nil {
				t.Fatal(err)
			}
			var source model.ResolvedSourceSpec
			var renderer model.RendererSpec
			if json.Unmarshal(sourceJSON, &source) != nil || json.Unmarshal(rendererJSON, &renderer) != nil {
				t.Fatal("persisted contract is invalid")
			}
			if location != "oci://registry.corp.local/charts" || kind != "helm_oci" || source.Type != model.SourceHelmOCI || source.URL != location || source.Trust.AllowUnsigned || source.Trust.KeyRef != "local-qa" || source.Revision.ArtifactDigest.String() == "" || renderer.Helm == nil || status != "verified" || !strings.Contains(identity, ";upstream:https://") || !strings.Contains(identity, ";archive:sha256:") {
				t.Fatal("actual persisted source/renderer lost digest/trust/audit projection")
			}
			count++
		}
		if rows.Err() != nil || count != 2 {
			t.Fatal("missing projected assets")
		}
		// A later signed distribution at the same chart versions creates new history,
		// never silently overwrites the previous immutable source/version identities.
		catalog.DistributionIdentity = "sha256:" + strings.Repeat("c", 64) + ";mirror:sha256:" + strings.Repeat("b", 64)
		next, err := provisioner.ensureAssets(ctx, clusterID, catalog)
		if err != nil {
			t.Fatal(err)
		}
		for index := range next {
			if next[index].id != initial[index].id || next[index].generation != transitioned[index].generation+1 {
				t.Fatal("later distribution escaped durable generation fencing")
			}
		}
		assertBuiltinProjectCounts(t, ctx, pool, projectID, 3, 2, 6, 2)
	})

	t.Run("concurrent retries converge", func(t *testing.T) {
		catalog := loadBuiltinTestCatalog(t)
		clusterID := uuid.New()
		const replicas = 6
		var group sync.WaitGroup
		errorsOut := make(chan error, replicas)
		for range replicas {
			group.Add(1)
			go func() {
				defer group.Done()
				_, err := provisioner.ensureAssets(ctx, clusterID, catalog)
				errorsOut <- err
			}()
		}
		group.Wait()
		close(errorsOut)
		for err := range errorsOut {
			if err != nil {
				t.Fatalf("concurrent ensure failed: %v", err)
			}
		}
		assertBuiltinProjectCounts(t, ctx, pool, stableID("project", clusterID.String()), 1, 2, 2, 2)
	})
}

func loadBuiltinTestCatalog(t *testing.T) builtinbundles.Catalog {
	t.Helper()
	catalog, err := builtinbundles.Load()
	if err != nil {
		t.Fatal(err)
	}
	// These transaction scenarios deliberately exercise the two metrics
	// components sharing one source; current multi-source membership is
	// covered separately by TestPlanCatalogSourcesReusesCurrentSourceAndPreservesIdentity.
	catalog.Components = catalog.Components[1:]
	return catalog
}

func assertBuiltinProjectCounts(t *testing.T, ctx context.Context, pool *pgxpool.Pool, projectID uuid.UUID, sources, bundles, versions, targets int) {
	t.Helper()
	queries := []struct {
		query string
		want  int
	}{
		{`SELECT count(*) FROM delivery_sources WHERE project_id=$1`, sources},
		{`SELECT count(*) FROM component_bundles WHERE project_id=$1`, bundles},
		{`SELECT count(*) FROM component_bundle_versions v JOIN component_bundles b ON b.id=v.bundle_id WHERE b.project_id=$1`, versions},
		{`SELECT count(*) FROM delivery_targets WHERE project_id=$1`, targets},
	}
	for _, item := range queries {
		var got int
		if err := pool.QueryRow(ctx, item.query, projectID).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != item.want {
			t.Fatalf("query %q count = %d, want %d", item.query, got, item.want)
		}
	}
}

func openBuiltinProvisionerTestPool(t *testing.T, ctx context.Context, dsn string) *pgxpool.Pool {
	t.Helper()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close(context.Background()) })
	schema := "builtin_sources_" + uuid.NewString()[:8]
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+identifier+" CASCADE") })
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.MaxConns = 12
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, builtinProvisionerDDL); err != nil {
		pool.Close()
		t.Fatal(err)
	}
	return pool
}

const builtinProvisionerDDL = `
CREATE TABLE projects (
 id uuid PRIMARY KEY, name text NOT NULL, display_name text NOT NULL, description text NOT NULL,
 cluster_id uuid NOT NULL, namespaces jsonb NOT NULL, resource_quota jsonb NOT NULL,
 managed_by text NOT NULL, UNIQUE(name,cluster_id)
);
CREATE TABLE delivery_sources (
 id uuid PRIMARY KEY, project_id uuid NOT NULL, name text NOT NULL, description text NOT NULL,
 source_type text NOT NULL, url text NOT NULL, auth_mode text NOT NULL DEFAULT 'none',
 credential_encrypted text NOT NULL DEFAULT '', credential_key_version integer NOT NULL DEFAULT 0,
 credential_epoch bigint NOT NULL DEFAULT 0, ca_bundle_encrypted text NOT NULL DEFAULT '',
 proxy_ref text NOT NULL DEFAULT '', trust_policy jsonb NOT NULL DEFAULT '{}', status text NOT NULL,
 last_resolved_at timestamptz, last_error_code text NOT NULL DEFAULT '', UNIQUE(project_id,name)
);
CREATE TABLE component_bundles (
 id uuid PRIMARY KEY, project_id uuid NOT NULL, name text NOT NULL, description text NOT NULL,
 UNIQUE(project_id,name)
);
CREATE TABLE component_bundle_versions (
 id uuid PRIMARY KEY, bundle_id uuid NOT NULL, source_id uuid NOT NULL, version text NOT NULL,
 renderer text NOT NULL, scope text NOT NULL, requested_revision text NOT NULL, resolved_revision text NOT NULL,
 artifact_digest text NOT NULL, source_spec jsonb NOT NULL, renderer_spec jsonb NOT NULL,
 reconciliation_policy jsonb NOT NULL, health_policy jsonb NOT NULL, requirements jsonb NOT NULL,
 dependency_bundle_ids jsonb NOT NULL, spec_digest text NOT NULL, verification_status text NOT NULL,
 verification_identity text NOT NULL, state text NOT NULL, UNIQUE(bundle_id,version), UNIQUE(bundle_id,spec_digest)
);
CREATE TABLE delivery_targets (
 id uuid PRIMARY KEY, project_id uuid NOT NULL, name text NOT NULL, description text NOT NULL,
 bundle_version_id uuid NOT NULL, placement jsonb NOT NULL, rollout_policy jsonb NOT NULL,
 reconciliation_policy jsonb NOT NULL, maintenance_window_policy jsonb NOT NULL,
 suspended boolean NOT NULL, generation bigint NOT NULL DEFAULT 1, resource_version bigint NOT NULL DEFAULT 1,
 updated_at timestamptz NOT NULL DEFAULT now(), UNIQUE(project_id,name)
);
`

func Example_catalogSourceIdentity() {
	identity, _ := catalogSourceIdentity(uuid.MustParse("ad27b142-eab5-44b9-8ec5-4042b331e916"), "https://charts.example.test/stable")
	fmt.Println(identity.name)
	// Output: astronomer-builtins-source-93da67650d293b97
}
