package catalogapp

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCatalogOperationStatusReadsExactRolloutAndDeletion(t *testing.T) {
	dsn := os.Getenv("AUDIT_OUTBOX_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("AUDIT_OUTBOX_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := tx.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	cluster, project, source, bundle, version, target, oldRollout, newRollout := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	digest := "sha256:" + strings.Repeat("a", 64)
	exec(`INSERT INTO clusters(id,name,display_name) VALUES($1,$2,$2)`, cluster, cluster.String())
	exec(`INSERT INTO projects(id,name,display_name,cluster_id) VALUES($1,$2,$2,$3)`, project, project.String(), cluster)
	exec(`INSERT INTO delivery_sources(id,project_id,name,source_type,url) VALUES($1,$2,'source','helm_http','https://example.invalid/charts')`, source, project)
	exec(`INSERT INTO component_bundles(id,project_id,name) VALUES($1,$2,'bundle')`, bundle, project)
	exec(`INSERT INTO component_bundle_versions(id,bundle_id,source_id,version,renderer,requested_revision,spec_digest) VALUES($1,$2,$3,'1','helm','1',$4)`, version, bundle, source, digest)
	exec(`INSERT INTO delivery_targets(id,project_id,name,bundle_version_id) VALUES($1,$2,'target',$3)`, target, project, version)
	for _, fixture := range []struct {
		id    uuid.UUID
		state string
	}{{oldRollout, "failed"}, {newRollout, "succeeded"}} {
		exec(`INSERT INTO delivery_rollouts(id,target_id,target_generation,to_bundle_version_id,placement_digest,strategy_digest,request_digest,plan_digest,frozen_plan,state,idempotency_key,last_error_code) VALUES($1,$2,1,$3,$4,$4,$4,$4,'{}',$5,$6,'original-error')`, fixture.id, target, version, digest, fixture.state, fixture.id.String())
	}
	status, err := readRolloutStatus(ctx, tx, target, oldRollout)
	if err != nil || status.Phase != "failed" || status.LastErrorCode != "original-error" {
		t.Fatalf("old rollout borrowed newer success: %+v %v", status, err)
	}
	status, err = readRolloutStatus(ctx, tx, target, newRollout)
	if err != nil || status.Phase != "ready" {
		t.Fatalf("new rollout: %+v %v", status, err)
	}
	if _, err := readRolloutStatus(ctx, tx, uuid.New(), oldRollout); err == nil {
		t.Fatal("foreign target read succeeded")
	}
	for _, state := range []string{"active", "deleting", "deleted"} {
		exec(`UPDATE delivery_targets SET deletion_state=$2 WHERE id=$1`, target, state)
		status, err = readDeletionStatus(ctx, tx, target)
		want := "pending"
		if state == "deleted" {
			want = "removed"
		}
		if err != nil || status.Phase != want {
			t.Fatalf("deletion %s: %+v %v", state, status, err)
		}
	}
	if _, err := readDeletionStatus(ctx, tx, uuid.New()); err == nil {
		t.Fatal("missing target claimed removed")
	}
}
