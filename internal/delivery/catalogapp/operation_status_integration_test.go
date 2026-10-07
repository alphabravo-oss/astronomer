package catalogapp

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/deployment"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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
	cluster, project, source, bundle, version, target, oldRollout, newRollout, pendingRollout := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
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
	}{{oldRollout, "failed"}, {newRollout, "succeeded"}, {pendingRollout, "queued"}} {
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
		status, err = readRolloutStatus(ctx, tx, target, pendingRollout)
		wantRollout, wantCode := "pending", "original-error"
		if state != "active" {
			wantRollout = "failed"
		}
		if err != nil || status.Phase != wantRollout || status.LastErrorCode != wantCode {
			t.Fatalf("pending rollout with target %s: %+v %v", state, status, err)
		}
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
	for _, phase := range []string{"", "removed", "ready"} {
		targetID := uuid.New()
		exec(`INSERT INTO delivery_targets(id,project_id,name,bundle_version_id) VALUES($1,$2,$3,$4)`, targetID, project, targetID.String(), version)
		if phase != "" {
			exec(`INSERT INTO cluster_deployments(target_id,cluster_id,phase) VALUES($1,$2,$3)`, targetID, cluster, phase)
		}
		row, err := deployment.RequestTargetDeletion(ctx, sqlc.New(tx), sqlc.RequestDeliveryTargetDeletionCASParams{
			ID: targetID, ProjectID: project, ExpectedResourceVersion: 1,
		})
		wantState, wantPhase, wantCount := "deleted", "removed", int64(0)
		if phase == "ready" {
			wantState, wantPhase, wantCount = "deleting", "pending", 1
		}
		if err != nil || row.DeletionState != wantState || row.DeploymentCount != wantCount {
			t.Fatalf("delete target with deployment phase %q: state=%s count=%d error=%v", phase, row.DeletionState, row.DeploymentCount, err)
		}
		status, err := readDeletionStatus(ctx, tx, targetID)
		if err != nil || status.Phase != wantPhase {
			t.Fatalf("delete status with deployment phase %q: %+v %v", phase, status, err)
		}
		if phase == "ready" {
			var action, storedPhase string
			if err := tx.QueryRow(ctx, `SELECT action,phase FROM cluster_deployments WHERE target_id=$1`, targetID).Scan(&action, &storedPhase); err != nil || action != "delete" || storedPhase != "pending" {
				t.Fatalf("active deployment must still await cluster deletion: action=%s phase=%s error=%v", action, storedPhase, err)
			}
		}
	}
	verifyDeletionWaitsForConcurrentPlanner(t, pool)
}

func verifyDeletionWaitsForConcurrentPlanner(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	schema := pgx.Identifier{"target_delete_" + strings.ReplaceAll(uuid.NewString(), "-", "")}.Sanitize()
	if _, err := pool.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := pool.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
	}()
	for _, table := range []string{"delivery_targets", "delivery_rollouts", "cluster_deployments"} {
		if _, err := pool.Exec(ctx, "CREATE TABLE "+schema+"."+table+" (LIKE public."+table+" INCLUDING ALL)"); err != nil {
			t.Fatal(err)
		}
	}
	target, project, version := uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, "INSERT INTO "+schema+".delivery_targets(id,project_id,name,bundle_version_id) VALUES($1,$2,'concurrent',$3)", target, project, version); err != nil {
		t.Fatal(err)
	}
	planner, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = planner.Rollback(context.Background()) }()
	if _, err := planner.Exec(ctx, "SET LOCAL search_path TO "+schema+",public"); err != nil {
		t.Fatal(err)
	}
	if _, err := planner.Exec(ctx, "SELECT id FROM delivery_targets WHERE id=$1 FOR UPDATE", target); err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		row sqlc.RequestDeliveryTargetDeletionCASRow
		err error
	}
	started := make(chan uint32, 1)
	finished := make(chan outcome, 1)
	go func() {
		tx, err := pool.Begin(ctx)
		if err != nil {
			finished <- outcome{err: err}
			return
		}
		defer func() { _ = tx.Rollback(context.Background()) }()
		if _, err := tx.Exec(ctx, "SET LOCAL search_path TO "+schema+",public"); err != nil {
			finished <- outcome{err: err}
			return
		}
		started <- tx.Conn().PgConn().PID()
		row, err := deployment.RequestTargetDeletion(ctx, sqlc.New(tx), sqlc.RequestDeliveryTargetDeletionCASParams{ID: target, ProjectID: project, ExpectedResourceVersion: 1})
		if err == nil {
			err = tx.Commit(ctx)
		}
		finished <- outcome{row, err}
	}()
	var pid uint32
	select {
	case pid = <-started:
	case result := <-finished:
		t.Fatal(result.err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	for {
		var blocked bool
		if err := pool.QueryRow(ctx, "SELECT COALESCE(wait_event_type='Lock',false) FROM pg_stat_activity WHERE pid=$1", pid).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	if _, err := planner.Exec(ctx, `INSERT INTO delivery_rollouts(target_id,target_generation,to_bundle_version_id,placement_digest,strategy_digest,request_digest,plan_digest,frozen_plan,state,idempotency_key) VALUES($1,1,$2,$3,$3,$3,$3,'{}','queued','concurrent')`, target, version, digest); err != nil {
		t.Fatal(err)
	}
	if err := planner.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-finished:
		if result.err != nil || result.row.DeletionState != "deleting" {
			t.Fatalf("concurrent rollout must retain acknowledgment-based deletion: state=%s error=%v", result.row.DeletionState, result.err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}
