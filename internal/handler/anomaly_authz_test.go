package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

func monitoringReadBindings(clusterID uuid.UUID) []rbac.RoleBinding {
	return []rbac.RoleBinding{{
		ClusterID: clusterID.String(),
		RoleRules: []rbac.Rule{{
			Resource: string(rbac.ResourceMonitoring),
			Verbs:    []string{string(rbac.VerbRead)},
		}},
	}}
}

func authedAnomalyReq(target string, params map[string]string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rc := chi.NewRouteContext()
	for k, v := range params {
		rc.URLParams.Add(k, v)
	}
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rc)
	ctx = middleware.SetAuthenticatedUserForTest(ctx, &middleware.AuthenticatedUser{ID: uuid.NewString()})
	return req.WithContext(ctx)
}

// TestAnomalyHandler_ListFleetFiltersToAuthorizedClusters proves the unscoped
// fleet listing only returns baselines for clusters the caller may monitor.
func TestAnomalyHandler_ListFleetFiltersToAuthorizedClusters(t *testing.T) {
	clusterA := uuid.New()
	clusterB := uuid.New()
	q := &fakeAnomalyQuerierForHandler{rows: []sqlc.AnomalyBaseline{
		sampleBaseline(uuid.New(), clusterA, "cluster_cpu_percent"),
		sampleBaseline(uuid.New(), clusterB, "cluster_memory_percent"),
	}}
	h := NewAnomalyHandler(q)
	h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: monitoringReadBindings(clusterA)})

	rec := httptest.NewRecorder()
	h.List(rec, authedAnomalyReq("/api/v1/anomaly-baselines/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	got := decodeAnomalyListBody(t, rec.Body.Bytes())
	if len(got) != 1 {
		t.Fatalf("cluster-scoped caller should see 1 baseline, got %d", len(got))
	}
	if got[0]["clusterId"] != clusterA.String() {
		t.Fatalf("visible baseline clusterId: want %s, got %v", clusterA, got[0]["clusterId"])
	}
}

// TestAnomalyHandler_ListScopedDeniesOtherCluster proves ?clusterId= for a
// cluster the caller cannot monitor is a 403, not a silent read.
func TestAnomalyHandler_ListScopedDeniesOtherCluster(t *testing.T) {
	clusterA := uuid.New()
	clusterB := uuid.New()
	q := &fakeAnomalyQuerierForHandler{rows: []sqlc.AnomalyBaseline{
		sampleBaseline(uuid.New(), clusterB, "cluster_cpu_percent"),
	}}
	h := NewAnomalyHandler(q)
	h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: monitoringReadBindings(clusterA)})

	rec := httptest.NewRecorder()
	h.List(rec, authedAnomalyReq("/api/v1/anomaly-baselines/?clusterId="+clusterB.String(), nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("scoped cross-cluster list: want 403, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestAnomalyHandler_GetDeniesCrossCluster proves fetching a baseline by UUID
// is gated on that baseline's own cluster, closing UUID-iteration disclosure.
func TestAnomalyHandler_GetDeniesCrossCluster(t *testing.T) {
	clusterA := uuid.New()
	clusterB := uuid.New()
	row := sampleBaseline(uuid.New(), clusterB, "cluster_cpu_percent")
	q := &fakeAnomalyQuerierForHandler{rows: []sqlc.AnomalyBaseline{row}}
	h := NewAnomalyHandler(q)

	// Caller scoped to cluster A cannot read cluster B's baseline.
	h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: monitoringReadBindings(clusterA)})
	rec := httptest.NewRecorder()
	h.Get(rec, authedAnomalyReq("/api/v1/anomaly-baselines/"+row.ID.String()+"/", map[string]string{"id": row.ID.String()}))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-cluster baseline get: want 403, got %d body=%s", rec.Code, rec.Body.String())
	}

	// Caller who holds monitoring:read on cluster B still succeeds.
	h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: monitoringReadBindings(clusterB)})
	rec = httptest.NewRecorder()
	h.Get(rec, authedAnomalyReq("/api/v1/anomaly-baselines/"+row.ID.String()+"/", map[string]string{"id": row.ID.String()}))
	if rec.Code != http.StatusOK {
		t.Fatalf("authorized baseline get: want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}
