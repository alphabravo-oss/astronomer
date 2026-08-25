package tasks

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

func TestClusterSnapshotOperationWorkerReplaysCommittedIntents(t *testing.T) {
	q := newFakePollQuerier()
	d := newFakeDriver()
	runtime := ClusterSnapshotRuntime{Deps: ClusterSnapshotDeps{Queries: q, Driver: d}}
	clusterID := uuid.New()
	snapshot, _ := q.CreateClusterSnapshot(context.Background(), sqlc.CreateClusterSnapshotParams{
		ClusterID: clusterID, VeleroName: "backup-one", VeleroNamespace: "velero",
		Spec: []byte(`{"includedNamespaces":["payments"]}`), Phase: "New",
	})
	restoreID := uuid.New()
	q.restores[restoreID] = sqlc.ClusterRestore{
		ID: restoreID, SnapshotID: snapshot.ID, TargetClusterID: clusterID,
		VeleroName: "restore-one", VeleroNamespace: "velero", Phase: "New",
	}

	operations := []ClusterSnapshotOperationPayload{
		{Operation: ClusterSnapshotOperationCreate, SnapshotID: snapshot.ID.String()},
		{Operation: ClusterSnapshotOperationRestore, RestoreID: restoreID.String()},
		{Operation: ClusterSnapshotOperationDelete, SnapshotID: snapshot.ID.String(), ClusterID: clusterID.String(), VeleroName: snapshot.VeleroName, VeleroNamespace: snapshot.VeleroNamespace},
	}
	for _, payload := range operations {
		task, err := NewClusterSnapshotOperationTask(payload)
		if err != nil {
			t.Fatalf("new %s task: %v", payload.Operation, err)
		}
		if err := runtime.HandleClusterSnapshotApplyOperation(context.Background(), task); err != nil {
			t.Fatalf("handle %s: %v", payload.Operation, err)
		}
	}
	if len(d.posted) != 3 {
		t.Fatalf("remote operations=%d, want 3", len(d.posted))
	}
	for index, operation := range []string{"create_snapshot", "create_restore", "delete_snapshot"} {
		if d.posted[index]["operation"] != operation {
			t.Fatalf("operation[%d]=%v, want %s", index, d.posted[index]["operation"], operation)
		}
	}
}

func TestClusterSnapshotOperationWorkerRetryAndStaleIntentSemantics(t *testing.T) {
	q := newFakePollQuerier()
	d := newFakeDriver()
	runtime := ClusterSnapshotRuntime{Deps: ClusterSnapshotDeps{Queries: q, Driver: d}}
	missingID := uuid.New()
	stale, _ := NewClusterSnapshotOperationTask(ClusterSnapshotOperationPayload{
		Operation: ClusterSnapshotOperationCreate, SnapshotID: missingID.String(),
	})
	if err := runtime.HandleClusterSnapshotApplyOperation(context.Background(), stale); err != nil {
		t.Fatalf("stale desired-state row should be a no-op: %v", err)
	}

	row, _ := q.CreateClusterSnapshot(context.Background(), sqlc.CreateClusterSnapshotParams{
		ClusterID: uuid.New(), VeleroName: "retry-me", VeleroNamespace: "velero", Phase: "New",
		ExpiresAt: pgtype.Timestamptz{},
	})
	task, _ := NewClusterSnapshotOperationTask(ClusterSnapshotOperationPayload{
		Operation: ClusterSnapshotOperationCreate, SnapshotID: row.ID.String(),
	})
	d.postErr = errors.New("tunnel unavailable")
	if err := runtime.HandleClusterSnapshotApplyOperation(context.Background(), task); err == nil || !strings.Contains(err.Error(), "tunnel unavailable") {
		t.Fatalf("transient external failure was not returned for retry: %v", err)
	}
}

func TestClusterSnapshotOperationTaskRejectsMalformedPayload(t *testing.T) {
	for _, payload := range []ClusterSnapshotOperationPayload{
		{},
		{Operation: ClusterSnapshotOperationCreate, SnapshotID: "not-a-uuid"},
		{Operation: ClusterSnapshotOperationRestore, RestoreID: "not-a-uuid"},
		{Operation: ClusterSnapshotOperationDelete, SnapshotID: uuid.NewString(), ClusterID: uuid.NewString()},
	} {
		if _, err := NewClusterSnapshotOperationTask(payload); err == nil {
			t.Fatalf("accepted malformed payload: %+v", payload)
		}
	}
}
