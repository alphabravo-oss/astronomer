package tasks

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// clusterTombstonePurgeQuerier embeds RuntimeQuerier so only the sweep's
// methods need implementations; any other call would nil-deref, flagging an
// unexpected path.
type clusterTombstonePurgeQuerier struct {
	RuntimeQuerier
	listCalls int
	cutoff    time.Time
	rows      []sqlc.ListExpiredClusterTombstonesRow
	deleted   []uuid.UUID
	audits    []sqlc.CreateAuditLogV1Params
}

func (q *clusterTombstonePurgeQuerier) ListExpiredClusterTombstones(_ context.Context, cutoff pgtype.Timestamptz) ([]sqlc.ListExpiredClusterTombstonesRow, error) {
	q.listCalls++
	q.cutoff = cutoff.Time
	return q.rows, nil
}

func (q *clusterTombstonePurgeQuerier) DeleteCluster(_ context.Context, id uuid.UUID) error {
	q.deleted = append(q.deleted, id)
	return nil
}

func (q *clusterTombstonePurgeQuerier) CreateAuditLogV1(_ context.Context, arg sqlc.CreateAuditLogV1Params) error {
	q.audits = append(q.audits, arg)
	return nil
}

func TestClusterTombstoneRetention_PurgesWithDefaultWindow(t *testing.T) {
	saved := runtimeDeps
	t.Cleanup(func() { runtimeDeps = saved })

	id := uuid.New()
	q := &clusterTombstonePurgeQuerier{rows: []sqlc.ListExpiredClusterTombstonesRow{{
		ID:               id,
		Name:             "old-cluster",
		DisplayName:      "Old Cluster",
		DecommissionedAt: pgtype.Timestamptz{Time: time.Now().UTC().Add(-100 * 24 * time.Hour), Valid: true},
	}}}
	runtimeDeps = RuntimeDependencies{Queries: q}

	retention := time.Duration(defaultClusterTombstoneRetentionDays) * 24 * time.Hour
	before := time.Now().UTC().Add(-retention)
	if err := HandleClusterTombstoneRetention(context.Background(), &asynq.Task{}); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if q.listCalls != 1 {
		t.Fatalf("expected 1 list call, got %d", q.listCalls)
	}
	after := time.Now().UTC().Add(-retention)
	if q.cutoff.Before(before.Add(-time.Minute)) || q.cutoff.After(after.Add(time.Minute)) {
		t.Errorf("cutoff %s not within the retention window [%s, %s]", q.cutoff, before, after)
	}
	if len(q.deleted) != 1 || q.deleted[0] != id {
		t.Fatalf("expected DeleteCluster(%s), got %v", id, q.deleted)
	}
	if len(q.audits) != 1 {
		t.Fatalf("expected 1 audit row, got %d", len(q.audits))
	}
	a := q.audits[0]
	if a.Action != "cluster.tombstone.purged" || a.ResourceType != "cluster" || a.ResourceID != id.String() || a.ResourceName != "Old Cluster" || a.Source != "worker" {
		t.Errorf("unexpected audit params: %+v", a)
	}
}

// The purge must be leader-gated so only the lease holder runs the DELETEs.
func TestClusterTombstoneRetention_SkippedOnNonLeader(t *testing.T) {
	saved := runtimeDeps
	t.Cleanup(func() { runtimeDeps = saved })

	q := &clusterTombstonePurgeQuerier{}
	runtimeDeps = RuntimeDependencies{Queries: q, Leader: &fakeLeader{held: false}}

	if err := HandleClusterTombstoneRetention(context.Background(), &asynq.Task{}); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if q.listCalls != 0 || len(q.deleted) != 0 {
		t.Fatalf("non-leader replica must not purge, got %d list calls / %d deletes", q.listCalls, len(q.deleted))
	}
}
