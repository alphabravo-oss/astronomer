package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
)

type cursorClusterQuerier struct {
	ClusterQuerier
	rows        []sqlc.Cluster
	cursorCalls []sqlc.ListClustersAfterParams
	offsetCalls []sqlc.ListClustersParams
}

func (q *cursorClusterQuerier) ListClustersAfter(_ context.Context, arg sqlc.ListClustersAfterParams) ([]sqlc.Cluster, error) {
	q.cursorCalls = append(q.cursorCalls, arg)
	start := 0
	if arg.HasCursor {
		start = len(q.rows)
		for i, row := range q.rows {
			if row.CreatedAt.Before(arg.AfterCreatedAt) || (row.CreatedAt.Equal(arg.AfterCreatedAt) && row.ID.String() < arg.AfterID.String()) {
				start = i
				break
			}
		}
	}
	end := start + int(arg.QueryLimit)
	if end > len(q.rows) {
		end = len(q.rows)
	}
	return append([]sqlc.Cluster(nil), q.rows[start:end]...), nil
}

func (q *cursorClusterQuerier) ListClusters(_ context.Context, arg sqlc.ListClustersParams) ([]sqlc.Cluster, error) {
	q.offsetCalls = append(q.offsetCalls, arg)
	return []sqlc.Cluster{}, nil
}

func (q *cursorClusterQuerier) ListClustersFilteredAfter(context.Context, sqlc.ListClustersFilteredAfterParams) ([]sqlc.Cluster, error) {
	return nil, nil
}
func (q *cursorClusterQuerier) ListClustersForScopesAfter(context.Context, sqlc.ListClustersForScopesAfterParams) ([]sqlc.Cluster, error) {
	return nil, nil
}
func (q *cursorClusterQuerier) ListClustersFilteredForScopesAfter(context.Context, sqlc.ListClustersFilteredForScopesAfterParams) ([]sqlc.Cluster, error) {
	return nil, nil
}
func (q *cursorClusterQuerier) CountClusters(context.Context) (int64, error) {
	return int64(len(q.rows)), nil
}
func (q *cursorClusterQuerier) ListPendingClusterDecommissionsForClusters(context.Context, []uuid.UUID) ([]sqlc.ClusterDecommission, error) {
	return nil, nil
}
func (q *cursorClusterQuerier) GetClusterLiveness(context.Context, uuid.UUID) (sqlc.ClusterLiveness, error) {
	return sqlc.ClusterLiveness{}, nil
}
func (q *cursorClusterQuerier) ListClusterLivenessForClusters(context.Context, []uuid.UUID) ([]sqlc.ClusterLiveness, error) {
	return nil, nil
}

func TestClusterListUsesBoundCursorByDefault(t *testing.T) {
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	q := &cursorClusterQuerier{rows: []sqlc.Cluster{
		{ID: uuid.MustParse("ffffffff-ffff-ffff-ffff-ffffffffffff"), Name: "one", CreatedAt: now, UpdatedAt: now},
		{ID: uuid.MustParse("eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee"), Name: "two", CreatedAt: now.Add(-time.Second), UpdatedAt: now},
		{ID: uuid.MustParse("dddddddd-dddd-dddd-dddd-dddddddddddd"), Name: "three", CreatedAt: now.Add(-2 * time.Second), UpdatedAt: now},
	}}
	h := &ClusterHandler{queries: q}

	first := httptest.NewRecorder()
	h.List(first, httptest.NewRequest(http.MethodGet, "/api/v1/clusters/?limit=2", nil))
	if first.Code != http.StatusOK {
		t.Fatalf("first page status=%d body=%s", first.Code, first.Body.String())
	}
	var firstPage paging.Response[ClusterResponse]
	if err := json.Unmarshal(first.Body.Bytes(), &firstPage); err != nil {
		t.Fatal(err)
	}
	if len(firstPage.Data) != 2 || firstPage.Pagination.NextCursor == nil || len(q.offsetCalls) != 0 {
		t.Fatalf("first page = %+v cursor_calls=%d offset_calls=%d", firstPage.Pagination, len(q.cursorCalls), len(q.offsetCalls))
	}

	second := httptest.NewRecorder()
	h.List(second, httptest.NewRequest(http.MethodGet, "/api/v1/clusters/?limit=2&cursor="+*firstPage.Pagination.NextCursor, nil))
	if second.Code != http.StatusOK {
		t.Fatalf("second page status=%d body=%s", second.Code, second.Body.String())
	}
	var secondPage paging.Response[ClusterResponse]
	if err := json.Unmarshal(second.Body.Bytes(), &secondPage); err != nil {
		t.Fatal(err)
	}
	if len(secondPage.Data) != 1 || secondPage.Data[0].Name != "three" || secondPage.Pagination.NextCursor != nil || !q.cursorCalls[1].HasCursor {
		t.Fatalf("second page = data:%+v pagination:%+v args:%+v", secondPage.Data, secondPage.Pagination, q.cursorCalls[1])
	}

	mismatch := httptest.NewRecorder()
	h.List(mismatch, httptest.NewRequest(http.MethodGet, "/api/v1/clusters/?limit=2&status=active&cursor="+*firstPage.Pagination.NextCursor, nil))
	if mismatch.Code != http.StatusBadRequest {
		t.Fatalf("filter-substituted cursor status=%d body=%s", mismatch.Code, mismatch.Body.String())
	}
}

func TestClusterLegacyOffsetIsBoundedBeforeSQL(t *testing.T) {
	q := &cursorClusterQuerier{}
	h := &ClusterHandler{queries: q}
	recorder := httptest.NewRecorder()
	h.List(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/clusters/?limit=20&offset=2147483647", nil))
	if recorder.Code != http.StatusOK || len(q.offsetCalls) != 1 || q.offsetCalls[0].Offset != int32(maxPaginationOffset) {
		t.Fatalf("status=%d offset calls=%+v body=%s", recorder.Code, q.offsetCalls, recorder.Body.String())
	}
}
