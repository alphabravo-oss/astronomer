//go:build integration

package db

import (
	"context"
	"io/fs"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alphabravocompany/astronomer-go/internal/db/migrations"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	deliverymetrics "github.com/alphabravocompany/astronomer-go/internal/delivery/metrics"
	"github.com/alphabravocompany/astronomer-go/internal/testdb"
)

type decommissionFixture struct {
	pool          *pgxpool.Pool
	live, retired uuid.UUID
}

// newDecommissionFixture builds two clusters, each with an applying
// deployment that has never been observed (stale) and an unready Flux
// inventory row, plus a ready deployment on the cluster to be retired.
func newDecommissionFixture(ctx context.Context, t *testing.T) decommissionFixture {
	t.Helper()
	pool := testdb.Pool(t)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	f := decommissionFixture{pool: pool, live: uuid.New(), retired: uuid.New()}
	project, source, bundle, version, target := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	digest := "sha256:" + strings.Repeat("a", 64)
	for _, cluster := range []uuid.UUID{f.live, f.retired} {
		exec(`INSERT INTO clusters(id,name,display_name) VALUES($1,$2,$2)`, cluster, cluster.String())
	}
	exec(`INSERT INTO projects(id,name,display_name,cluster_id) VALUES($1,$2,$2,$3)`, project, project.String(), f.live)
	exec(`INSERT INTO delivery_sources(id,project_id,name,source_type,url) VALUES($1,$2,'source','helm_http','https://example.invalid/charts')`, source, project)
	exec(`INSERT INTO component_bundles(id,project_id,name) VALUES($1,$2,'bundle')`, bundle, project)
	exec(`INSERT INTO component_bundle_versions(id,bundle_id,source_id,version,renderer,requested_revision,spec_digest) VALUES($1,$2,$3,'1','helm','1',$4)`, version, bundle, source, digest)
	exec(`INSERT INTO delivery_targets(id,project_id,name,bundle_version_id) VALUES($1,$2,'target',$3)`, target, project, version)
	exec(`INSERT INTO cluster_deployments(target_id,cluster_id,phase) VALUES($1,$2,'applying'),($1,$3,'applying')`, target, f.live, f.retired)
	second := uuid.New()
	exec(`INSERT INTO delivery_targets(id,project_id,name,bundle_version_id) VALUES($1,$2,'target-2',$3)`, second, project, version)
	exec(`INSERT INTO cluster_deployments(target_id,cluster_id,phase,last_observed_at) VALUES($1,$2,'ready',now())`, second, f.retired)
	exec(`INSERT INTO delivery_controller_inventory(cluster_id,ready,compatibility_status,status_digest,agent_session_id,agent_sequence,semantic_sequence) VALUES($1,false,'compatible',$3,'s',1,1),($2,false,'compatible',$3,'s',1,1)`, f.live, f.retired, digest)
	return f
}

func (f decommissionFixture) phases(ctx context.Context, t *testing.T, cluster uuid.UUID) []string {
	t.Helper()
	rows, err := f.pool.Query(ctx, `SELECT phase || ':' || last_error_code FROM cluster_deployments WHERE cluster_id=$1 ORDER BY phase`, cluster)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	return out
}

func deploymentCount(snapshot deliverymetrics.DatabaseSnapshot, phase string) int64 {
	var n int64
	for _, row := range snapshot.Deployments {
		if row.Phase == phase {
			n += row.Count
		}
	}
	return n
}

func unreadyFlux(snapshot deliverymetrics.DatabaseSnapshot) int64 {
	var n int64
	for _, row := range snapshot.Flux {
		if !row.Ready {
			n += row.Count
		}
	}
	return n
}

func snapshotOrFail(ctx context.Context, t *testing.T, pool *pgxpool.Pool) deliverymetrics.DatabaseSnapshot {
	t.Helper()
	snapshot, err := snapshotDeliveryMetrics(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

// Decommissioning a cluster marks its deployments removed, and the delivery
// alert gauges stop counting the cluster, so backlog/stale/Flux alerts clear.
func TestPostgresTombstoneCascadesDeploymentsAndMetricsExcludeCluster(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newDecommissionFixture(ctx, t)

	before := snapshotOrFail(ctx, t, f.pool)
	if before.StaleDeployments != 2 || deploymentCount(before, "applying") != 2 || unreadyFlux(before) != 2 {
		t.Fatalf("baseline stale=%d applying=%d unreadyFlux=%d, want 2/2/2", before.StaleDeployments, deploymentCount(before, "applying"), unreadyFlux(before))
	}

	if err := sqlc.New(f.pool).TombstoneCluster(ctx, f.retired); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(f.phases(ctx, t, f.retired), ","); got != "removed:cluster_decommissioned,removed:cluster_decommissioned" {
		t.Fatalf("retired cluster deployments = %s, want all removed", got)
	}
	if got := strings.Join(f.phases(ctx, t, f.live), ","); got != "applying:" {
		t.Fatalf("live cluster deployments touched: %s", got)
	}
	after := snapshotOrFail(ctx, t, f.pool)
	if after.StaleDeployments != 1 || deploymentCount(after, "applying") != 1 || deploymentCount(after, "removed") != 0 || unreadyFlux(after) != 1 {
		t.Fatalf("after tombstone stale=%d applying=%d removed=%d unreadyFlux=%d, want 1/1/0/1",
			after.StaleDeployments, deploymentCount(after, "applying"), deploymentCount(after, "removed"), unreadyFlux(after))
	}
}

// Clusters tombstoned before the cascade existed: their still-active
// deployments are excluded from the gauges, and migration 00083 marks them
// removed.
func TestPostgresLegacyDecommissionedDeploymentsExcludedAndBackfilled(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newDecommissionFixture(ctx, t)
	// Legacy tombstone: cluster row only.
	if _, err := f.pool.Exec(ctx, `UPDATE clusters SET decommissioned_at=now(), status='decommissioned' WHERE id=$1`, f.retired); err != nil {
		t.Fatal(err)
	}
	snapshot := snapshotOrFail(ctx, t, f.pool)
	if snapshot.StaleDeployments != 1 || deploymentCount(snapshot, "applying") != 1 || deploymentCount(snapshot, "ready") != 0 || unreadyFlux(snapshot) != 1 {
		t.Fatalf("decommissioned cluster still counted: stale=%d applying=%d ready=%d unreadyFlux=%d",
			snapshot.StaleDeployments, deploymentCount(snapshot, "applying"), deploymentCount(snapshot, "ready"), unreadyFlux(snapshot))
	}

	body, err := fs.ReadFile(migrations.FS(), "00083_remove_deployments_of_decommissioned_clusters.sql")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, string(body)); err != nil {
		t.Fatalf("re-run migration 00083: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(f.phases(ctx, t, f.retired), ","); got != "removed:cluster_decommissioned,removed:cluster_decommissioned" {
		t.Fatalf("backfill left %s", got)
	}
	if got := strings.Join(f.phases(ctx, t, f.live), ","); got != "applying:" {
		t.Fatalf("backfill touched live cluster: %s", got)
	}
}
