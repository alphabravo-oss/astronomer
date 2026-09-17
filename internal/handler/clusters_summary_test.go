package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
	"github.com/google/uuid"
)

type clusterSummaryQuerier struct {
	ClusterQuerier
	all       sqlc.GetClusterEstateSummaryRow
	scoped    sqlc.GetClusterEstateSummaryForScopesRow
	allCalls  int
	scopeArgs [][]uuid.UUID
}

func (q *clusterSummaryQuerier) GetClusterEstateSummary(context.Context) (sqlc.GetClusterEstateSummaryRow, error) {
	q.allCalls++
	return q.all, nil
}

func (q *clusterSummaryQuerier) GetClusterEstateSummaryForScopes(_ context.Context, ids []uuid.UUID) (sqlc.GetClusterEstateSummaryForScopesRow, error) {
	q.scopeArgs = append(q.scopeArgs, append([]uuid.UUID(nil), ids...))
	return q.scoped, nil
}

type clusterSummaryEnvelope struct {
	Data ClusterEstateSummaryResponse `json:"data"`
}

func TestClusterSummaryUsesEstateAggregate(t *testing.T) {
	q := &clusterSummaryQuerier{all: sqlc.GetClusterEstateSummaryRow{
		ClustersTotal: 2001, ClustersActive: 1995, ClustersWarning: 2,
		ClustersDisconnected: 4, NodesTotal: 6003, PodsTotal: 48024,
	}}
	h := &ClusterHandler{queries: q}
	recorder := httptest.NewRecorder()
	h.Summary(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/clusters/summary/", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response clusterSummaryEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data.ClustersTotal != 2001 || response.Data.NodesTotal != 6003 || response.Data.PodsTotal != 48024 {
		t.Fatalf("summary = %+v", response.Data)
	}
	if response.Data.AsOf.IsZero() || q.allCalls != 1 || len(q.scopeArgs) != 0 {
		t.Fatalf("as_of=%s all_calls=%d scope_calls=%d", response.Data.AsOf, q.allCalls, len(q.scopeArgs))
	}
}

func TestClusterSummaryIsAuthorizationScoped(t *testing.T) {
	clusterA := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	clusterB := uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	q := &clusterSummaryQuerier{scoped: sqlc.GetClusterEstateSummaryForScopesRow{
		ClustersTotal: 2, ClustersActive: 1, ClustersDisconnected: 1,
	}}
	h := &ClusterHandler{queries: q}
	h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: append(
		clustersVerbBindings(clusterB, rbac.VerbList),
		clustersVerbBindings(clusterA, rbac.VerbList)...,
	)})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/summary/", nil)
	req = req.WithContext(reqctx.WithUser(req.Context(), &reqctx.User{ID: uuid.NewString()}))
	recorder := httptest.NewRecorder()
	h.Summary(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if q.allCalls != 0 || len(q.scopeArgs) != 1 {
		t.Fatalf("all_calls=%d scope_args=%v", q.allCalls, q.scopeArgs)
	}
	if got := q.scopeArgs[0]; len(got) != 2 || got[0] != clusterA || got[1] != clusterB {
		t.Fatalf("scope ids = %v, want sorted [%s %s]", got, clusterA, clusterB)
	}
}

func TestClusterSummaryFailsClosedWithoutAuthorizationWiring(t *testing.T) {
	h := &ClusterHandler{queries: &clusterSummaryQuerier{}}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/summary/", nil)
	req = req.WithContext(reqctx.WithUser(req.Context(), &reqctx.User{ID: uuid.NewString()}))
	recorder := httptest.NewRecorder()
	h.Summary(recorder, req)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestClusterSummaryFailsClosedWithoutSummaryStore(t *testing.T) {
	h := &ClusterHandler{queries: new(cursorClusterQuerier)}
	recorder := httptest.NewRecorder()
	h.Summary(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/clusters/summary/", nil))

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
