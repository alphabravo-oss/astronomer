package sqlc

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestLoggingPipelineOutputsAreClusterScopedTransactionalAndDeleteRestricted(t *testing.T) {
	dsn := os.Getenv("AUDIT_OUTBOX_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("AUDIT_OUTBOX_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	clusterID := uuid.New()
	foreignClusterID := uuid.New()
	outputID := uuid.New()
	foreignOutputID := uuid.New()
	pipelineID := uuid.New()

	// This transaction models the handler's create + association boundary. A
	// foreign-cluster UUID is ignored by SQL; the count mismatch makes the
	// service roll the whole transaction back, including the pipeline row.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	insertLoggingAssociationFixture(t, ctx, tx, clusterID, foreignClusterID, outputID, foreignOutputID, pipelineID)
	q := New(tx)
	count, err := q.ReplaceLoggingPipelineOutputs(ctx, ReplaceLoggingPipelineOutputsParams{
		LoggingPipelineID: pipelineID,
		OutputIds:         []uuid.UUID{outputID, foreignOutputID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("associated count=%d, want only the same-cluster output", count)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	var persisted int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM logging_pipelines WHERE id=$1`, pipelineID).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if persisted != 0 {
		t.Fatal("pipeline survived the simulated association validation rollback")
	}

	// A valid replacement is visible through the batch read contract, and the
	// database—not a race-prone preflight alone—prevents destination deletion.
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	insertLoggingAssociationFixture(t, ctx, tx, clusterID, foreignClusterID, outputID, foreignOutputID, pipelineID)
	q = New(tx)
	count, err = q.ReplaceLoggingPipelineOutputs(ctx, ReplaceLoggingPipelineOutputsParams{
		LoggingPipelineID: pipelineID,
		OutputIds:         []uuid.UUID{outputID},
	})
	if err != nil || count != 1 {
		t.Fatalf("valid replacement count=%d err=%v", count, err)
	}
	details, err := q.ListLoggingPipelineOutputDetails(ctx, []uuid.UUID{pipelineID})
	if err != nil {
		t.Fatal(err)
	}
	if len(details) != 1 || details[0].LoggingOutputID != outputID {
		t.Fatalf("details=%+v", details)
	}
	_, err = tx.Exec(ctx, `DELETE FROM logging_outputs WHERE id=$1`, outputID)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
		t.Fatalf("delete error=%v, want PostgreSQL 23503", err)
	}
}

func insertLoggingAssociationFixture(t *testing.T, ctx context.Context, tx pgx.Tx, clusterID, foreignClusterID, outputID, foreignOutputID, pipelineID uuid.UUID) {
	t.Helper()
	for _, cluster := range []uuid.UUID{clusterID, foreignClusterID} {
		_, err := tx.Exec(ctx, `INSERT INTO clusters (id,name,display_name) VALUES ($1,$2,$2)`, cluster, "logging-"+cluster.String())
		if err != nil {
			t.Fatalf("insert cluster: %v", err)
		}
	}
	for _, output := range []struct {
		id, clusterID uuid.UUID
		name          string
	}{{outputID, clusterID, "primary"}, {foreignOutputID, foreignClusterID, "foreign"}} {
		_, err := tx.Exec(ctx, `INSERT INTO logging_outputs (id,name,output_type,configuration,cluster_id) VALUES ($1,$2,'stdout','{}',$3)`, output.id, output.name, output.clusterID)
		if err != nil {
			t.Fatalf("insert output: %v", err)
		}
	}
	_, err := tx.Exec(ctx, `INSERT INTO logging_pipelines (id,name,cluster_id) VALUES ($1,'payments',$2)`, pipelineID, clusterID)
	if err != nil {
		t.Fatalf("insert pipeline: %v", err)
	}
}
