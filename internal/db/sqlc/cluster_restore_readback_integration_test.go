package sqlc

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"testing"
)

func TestClusterRestoreHistoryFiltersSourceBeforePagination(t *testing.T) {
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
	target, allowed, denied := uuid.New(), uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{target, allowed, denied} {
		if _, err := tx.Exec(ctx, `INSERT INTO clusters(id,name,display_name) VALUES($1,$2,$2)`, id, id.String()); err != nil {
			t.Fatal(err)
		}
	}
	ids := map[uuid.UUID][]uuid.UUID{}
	for _, source := range []uuid.UUID{allowed, denied} {
		snapshot := uuid.New()
		if _, err := tx.Exec(ctx, `INSERT INTO cluster_snapshots(id,cluster_id,velero_name,velero_namespace,spec,phase) VALUES($1,$2,$3,'velero','{}','Completed')`, snapshot, source, snapshot.String()); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 3; i++ {
			id := uuid.New()
			ids[source] = append(ids[source], id)
			if _, err := tx.Exec(ctx, `INSERT INTO cluster_restores(id,snapshot_id,target_cluster_id,velero_name,velero_namespace,spec,phase,created_at) VALUES($1,$2,$3,$4,'velero','{}','New','2026-09-24T00:00:00Z')`, id, snapshot, target, id.String()); err != nil {
				t.Fatal(err)
			}
		}
	}
	q := New(tx)
	for _, all := range []bool{false, true} {
		expected := int64(3)
		if all {
			expected = 6
		}
		count, err := q.CountClusterRestores(ctx, CountClusterRestoresParams{TargetClusterID: target, AllSources: all, SourceClusterIds: []uuid.UUID{allowed}})
		if err != nil || count != expected {
			t.Fatalf("count %d err %v", count, err)
		}
		seen := map[uuid.UUID]bool{}
		for offset := int32(0); offset < int32(expected); offset++ {
			rows, err := q.ListClusterRestoresPage(ctx, ListClusterRestoresPageParams{TargetClusterID: target, AllSources: all, SourceClusterIds: []uuid.UUID{allowed}, QueryLimit: 1, QueryOffset: offset})
			if err != nil || len(rows) != 1 {
				t.Fatalf("page offset %d rows %d err %v", offset, len(rows), err)
			}
			row := rows[0]
			if (!all && row.SourceClusterID != allowed) || seen[row.ID] {
				t.Fatalf("unauthorized or duplicate row %+v", row)
			}
			seen[row.ID] = true
		}
	}
	count, err := q.CountClusterRestores(ctx, CountClusterRestoresParams{TargetClusterID: target, SourceClusterIds: []uuid.UUID{}})
	if err != nil || count != 0 {
		t.Fatalf("empty source permission count %d err %v", count, err)
	}
}
