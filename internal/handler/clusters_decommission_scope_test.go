package handler

import (
	"context"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type scopedDecommissionQuerier struct {
	ClusterQuerier
	wantRows []sqlc.ClusterDecommission
	gotIDs   []uuid.UUID
}

func (q *scopedDecommissionQuerier) ListPendingClusterDecommissionsForClusters(_ context.Context, ids []uuid.UUID) ([]sqlc.ClusterDecommission, error) {
	q.gotIDs = append([]uuid.UUID(nil), ids...)
	return q.wantRows, nil
}

func TestInFlightDecommissionSetQueriesOnlyAuthorizedPage(t *testing.T) {
	pageIDs := []uuid.UUID{uuid.New(), uuid.New()}
	q := &scopedDecommissionQuerier{wantRows: []sqlc.ClusterDecommission{{ClusterID: pageIDs[1]}}}
	h := NewClusterHandler(q)

	got := h.inFlightDecommissionSet(context.Background(), pageIDs)
	if !slices.Equal(q.gotIDs, pageIDs) {
		t.Fatalf("query cluster IDs = %v, want authorized page %v", q.gotIDs, pageIDs)
	}
	if got[pageIDs[0]] || !got[pageIDs[1]] {
		t.Fatalf("decommission set = %v, want only %s", got, pageIDs[1])
	}
}

func TestInFlightDecommissionSetSkipsEmptyPage(t *testing.T) {
	q := &scopedDecommissionQuerier{}
	h := NewClusterHandler(q)
	if got := h.inFlightDecommissionSet(context.Background(), nil); len(got) != 0 {
		t.Fatalf("decommission set = %v, want empty", got)
	}
	if q.gotIDs != nil {
		t.Fatalf("empty page queried database with IDs %v", q.gotIDs)
	}
}
