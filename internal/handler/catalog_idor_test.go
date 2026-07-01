package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

// idorCatalogQuerier is a minimal CatalogQuerier that returns fixed
// installed-chart rows so the RBAC/IDOR gates can be exercised without a DB.
type idorCatalogQuerier struct {
	*minimalCatalogQuerier
	installs map[uuid.UUID]sqlc.InstalledChart
	list     []sqlc.InstalledChart
}

func newIDORCatalogQuerier() *idorCatalogQuerier {
	return &idorCatalogQuerier{
		minimalCatalogQuerier: &minimalCatalogQuerier{fakeProjectCatalogQuerier: newFakeProjectCatalogQuerier()},
		installs:              map[uuid.UUID]sqlc.InstalledChart{},
	}
}

func (q *idorCatalogQuerier) GetInstalledChartByID(_ context.Context, id uuid.UUID) (sqlc.InstalledChart, error) {
	row, ok := q.installs[id]
	if !ok {
		return sqlc.InstalledChart{}, pgx.ErrNoRows
	}
	return row, nil
}

func (q *idorCatalogQuerier) ListInstalledCharts(_ context.Context, _ sqlc.ListInstalledChartsParams) ([]sqlc.InstalledChart, error) {
	return q.list, nil
}

func (q *idorCatalogQuerier) CountInstalledCharts(_ context.Context) (int64, error) {
	return int64(len(q.list)), nil
}

func catalogReadBindings(clusterID uuid.UUID) []rbac.RoleBinding {
	return []rbac.RoleBinding{{
		ClusterID: clusterID.String(),
		RoleRules: []rbac.Rule{{
			Resource: string(rbac.ResourceCatalog),
			Verbs:    []string{string(rbac.VerbRead)},
		}},
	}}
}

func authedCatalogReq(method, target string, params map[string]string) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	rc := chi.NewRouteContext()
	for k, v := range params {
		rc.URLParams.Add(k, v)
	}
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rc)
	ctx = middleware.SetAuthenticatedUserForTest(ctx, &middleware.AuthenticatedUser{ID: uuid.NewString()})
	return req.WithContext(ctx)
}

// TestCatalogHandler_GetInstalledChartValuesDeniesCrossCluster proves the
// values endpoint (which returns raw Helm values — routinely secrets) is gated
// on the release's own cluster: a caller without catalog:read there gets 403,
// and a caller who holds it still gets 200 with the values.
func TestCatalogHandler_GetInstalledChartValuesDeniesCrossCluster(t *testing.T) {
	clusterA := uuid.New()
	clusterB := uuid.New()
	q := newIDORCatalogQuerier()
	chartOnB := sqlc.InstalledChart{
		ID:             uuid.New(),
		ClusterID:      clusterB,
		ReleaseName:    "postgres",
		Namespace:      "data",
		ValuesOverride: "postgresql:\n  auth:\n    password: s3cr3t\n",
		Status:         "deployed",
	}
	q.installs[chartOnB.ID] = chartOnB

	h := NewCatalogHandler(q)
	h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: catalogReadBindings(clusterA)})

	// Caller scoped to cluster A must not read cluster B's values.
	rec := httptest.NewRecorder()
	h.GetInstalledChartValues(rec, authedCatalogReq(http.MethodGet, "/api/v1/catalog/installed/"+chartOnB.ID.String()+"/values/", map[string]string{"id": chartOnB.ID.String()}))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-cluster values read: want 403, got %d body=%s", rec.Code, rec.Body.String())
	}
	if bodyContains(rec, "s3cr3t") {
		t.Fatalf("values leaked in forbidden response: %s", rec.Body.String())
	}

	// Caller who holds catalog:read on cluster B still succeeds.
	h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: catalogReadBindings(clusterB)})
	rec = httptest.NewRecorder()
	h.GetInstalledChartValues(rec, authedCatalogReq(http.MethodGet, "/api/v1/catalog/installed/"+chartOnB.ID.String()+"/values/", map[string]string{"id": chartOnB.ID.String()}))
	if rec.Code != http.StatusOK {
		t.Fatalf("authorized values read: want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !bodyContains(rec, "s3cr3t") {
		t.Fatalf("authorized caller should receive the values: %s", rec.Body.String())
	}
}

// TestCatalogHandler_ListInstalledChartsScopesAndStripsSecrets proves the
// fleet-wide listing (a) returns only clusters the caller may read and (b)
// never emits values_override in the list projection.
func TestCatalogHandler_ListInstalledChartsScopesAndStripsSecrets(t *testing.T) {
	clusterA := uuid.New()
	clusterB := uuid.New()
	q := newIDORCatalogQuerier()
	q.list = []sqlc.InstalledChart{
		{ID: uuid.New(), ClusterID: clusterA, ReleaseName: "app-a", Namespace: "a", ValuesOverride: "apiKey: AAAA", Status: "deployed"},
		{ID: uuid.New(), ClusterID: clusterB, ReleaseName: "app-b", Namespace: "b", ValuesOverride: "apiKey: BBBB", Status: "deployed"},
	}

	h := NewCatalogHandler(q)
	h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: catalogReadBindings(clusterA)})

	rec := httptest.NewRecorder()
	h.ListInstalledCharts(rec, authedCatalogReq(http.MethodGet, "/api/v1/catalog/installed/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list installed charts: want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if bodyContains(rec, "BBBB") || bodyContains(rec, "AAAA") || bodyContains(rec, "values_override") {
		t.Fatalf("list must not expose values_override: %s", rec.Body.String())
	}

	var env struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode list body: %v raw=%s", err, rec.Body.String())
	}
	if len(env.Data) != 1 {
		t.Fatalf("cluster-scoped caller should see 1 row, got %d (%+v)", len(env.Data), env.Data)
	}
	if env.Data[0]["cluster_id"] != clusterA.String() {
		t.Fatalf("visible row cluster_id: want %s, got %v", clusterA, env.Data[0]["cluster_id"])
	}
}

func bodyContains(rec *httptest.ResponseRecorder, sub string) bool {
	return strings.Contains(rec.Body.String(), sub)
}
