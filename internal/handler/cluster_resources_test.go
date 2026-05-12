// Sprint 069 — unit tests for the ClusterResourcesHandler.
//
// Tests exercise the public list endpoints with a synthetic chi route
// context and a tiny in-memory querier fake. Coverage matches the
// spec's test prefix (TestCRDMirrorHandler_*):
//
//   - TestCRDMirrorHandler_ListsForCluster (one per kind, table-driven)
//   - TestCRDMirrorHandler_FilterByNamespace
//   - TestCRDMirrorHandler_RequiresClusterRead (route-table doc-stub —
//     the real RBAC gate is verified by the route-layer integration
//     test; we leave a smoke check here that the unwired handler
//     short-circuits to 503).

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// fakeMirrorQuerier is a tiny in-memory ClusterResourcesQuerier. We
// duplicate this rather than reuse internal/crd's test fake because
// handler tests need a GetClusterByID branch + only the read methods.
type fakeClusterResourcesQuerier struct {
	mu       sync.Mutex
	clusters map[uuid.UUID]sqlc.Cluster
	ic       []sqlc.MirroredIngressClass
	gwc      []sqlc.MirroredGatewayClass
	np       []sqlc.MirroredNetworkPolicy
	rq       []sqlc.MirroredResourceQuota
	lr       []sqlc.MirroredLimitRange
}

func newFakeClusterResourcesQuerier(clusterID uuid.UUID, name string) *fakeClusterResourcesQuerier {
	return &fakeClusterResourcesQuerier{
		clusters: map[uuid.UUID]sqlc.Cluster{
			clusterID: {ID: clusterID, Name: name},
		},
	}
}

func (f *fakeClusterResourcesQuerier) GetClusterByID(_ context.Context, id uuid.UUID) (sqlc.Cluster, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.clusters[id]
	if !ok {
		return sqlc.Cluster{}, pgx.ErrNoRows
	}
	return c, nil
}

func (f *fakeClusterResourcesQuerier) ListMirroredIngressClasses(_ context.Context, cid uuid.UUID) ([]sqlc.MirroredIngressClass, error) {
	out := []sqlc.MirroredIngressClass{}
	for _, r := range f.ic {
		if r.ClusterID == cid {
			out = append(out, r)
		}
	}
	return out, nil
}
func (f *fakeClusterResourcesQuerier) ListMirroredGatewayClasses(_ context.Context, cid uuid.UUID) ([]sqlc.MirroredGatewayClass, error) {
	out := []sqlc.MirroredGatewayClass{}
	for _, r := range f.gwc {
		if r.ClusterID == cid {
			out = append(out, r)
		}
	}
	return out, nil
}
func (f *fakeClusterResourcesQuerier) ListMirroredNetworkPolicies(_ context.Context, cid uuid.UUID) ([]sqlc.MirroredNetworkPolicy, error) {
	out := []sqlc.MirroredNetworkPolicy{}
	for _, r := range f.np {
		if r.ClusterID == cid {
			out = append(out, r)
		}
	}
	return out, nil
}
func (f *fakeClusterResourcesQuerier) ListMirroredNetworkPoliciesByNamespace(_ context.Context, arg sqlc.ListMirroredNetworkPoliciesByNamespaceParams) ([]sqlc.MirroredNetworkPolicy, error) {
	out := []sqlc.MirroredNetworkPolicy{}
	for _, r := range f.np {
		if r.ClusterID == arg.ClusterID && r.Namespace == arg.Namespace {
			out = append(out, r)
		}
	}
	return out, nil
}
func (f *fakeClusterResourcesQuerier) ListMirroredResourceQuotas(_ context.Context, cid uuid.UUID) ([]sqlc.MirroredResourceQuota, error) {
	out := []sqlc.MirroredResourceQuota{}
	for _, r := range f.rq {
		if r.ClusterID == cid {
			out = append(out, r)
		}
	}
	return out, nil
}
func (f *fakeClusterResourcesQuerier) ListMirroredResourceQuotasByNamespace(_ context.Context, arg sqlc.ListMirroredResourceQuotasByNamespaceParams) ([]sqlc.MirroredResourceQuota, error) {
	out := []sqlc.MirroredResourceQuota{}
	for _, r := range f.rq {
		if r.ClusterID == arg.ClusterID && r.Namespace == arg.Namespace {
			out = append(out, r)
		}
	}
	return out, nil
}
func (f *fakeClusterResourcesQuerier) ListMirroredLimitRanges(_ context.Context, cid uuid.UUID) ([]sqlc.MirroredLimitRange, error) {
	out := []sqlc.MirroredLimitRange{}
	for _, r := range f.lr {
		if r.ClusterID == cid {
			out = append(out, r)
		}
	}
	return out, nil
}
func (f *fakeClusterResourcesQuerier) ListMirroredLimitRangesByNamespace(_ context.Context, arg sqlc.ListMirroredLimitRangesByNamespaceParams) ([]sqlc.MirroredLimitRange, error) {
	out := []sqlc.MirroredLimitRange{}
	for _, r := range f.lr {
		if r.ClusterID == arg.ClusterID && r.Namespace == arg.Namespace {
			out = append(out, r)
		}
	}
	return out, nil
}

// Compile-time assertion the fake satisfies the interface.
var _ ClusterResourcesQuerier = (*fakeClusterResourcesQuerier)(nil)

// withClusterParam wraps a chi request with a {cluster_id} route param
// so the handler can resolve it via chi.URLParam.
func withClusterParam(req *http.Request, cid uuid.UUID) *http.Request {
	rc := chi.NewRouteContext()
	rc.URLParams.Add("cluster_id", cid.String())
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rc))
}

func TestCRDMirrorHandler_ListsForCluster(t *testing.T) {
	cid := uuid.New()
	other := uuid.New()
	q := newFakeClusterResourcesQuerier(cid, "prod-1")
	q.clusters[other] = sqlc.Cluster{ID: other, Name: "stage-1"}
	now := time.Now()

	// Seed one row per kind across both clusters; the listed cluster
	// must only see its own rows.
	q.ic = []sqlc.MirroredIngressClass{
		{ID: uuid.New(), ClusterID: cid, Name: "nginx", Controller: "k8s.io/ingress-nginx", IsDefault: true, Labels: []byte("{}"), Annotations: []byte("{}"), Parameters: []byte("{}"), LastSeenAt: now, CreatedAt: now, UpdatedAt: now},
		{ID: uuid.New(), ClusterID: other, Name: "leaked", Labels: []byte("{}"), Annotations: []byte("{}"), Parameters: []byte("{}")},
	}
	q.gwc = []sqlc.MirroredGatewayClass{
		{ID: uuid.New(), ClusterID: cid, Name: "cilium", ControllerName: "io.cilium/gateway-controller", AcceptedStatus: "True", Labels: []byte("{}"), Annotations: []byte("{}"), Parameters: []byte("{}"), LastSeenAt: now, CreatedAt: now, UpdatedAt: now},
	}
	q.np = []sqlc.MirroredNetworkPolicy{
		{ID: uuid.New(), ClusterID: cid, Namespace: "prod-api", Name: "deny-egress", IsManaged: true, Labels: []byte(`{"app.kubernetes.io/managed-by":"astronomer"}`), Annotations: []byte("{}"), PodSelector: []byte("{}"), PolicyTypes: []byte(`["Egress"]`), IngressRules: []byte("[]"), EgressRules: []byte("[]"), LastSeenAt: now, CreatedAt: now, UpdatedAt: now},
		{ID: uuid.New(), ClusterID: cid, Namespace: "kube-system", Name: "deny-all", IsManaged: false, Labels: []byte("{}"), Annotations: []byte("{}"), PodSelector: []byte("{}"), PolicyTypes: []byte(`["Ingress"]`), IngressRules: []byte("[]"), EgressRules: []byte("[]"), LastSeenAt: now, CreatedAt: now, UpdatedAt: now},
	}
	q.rq = []sqlc.MirroredResourceQuota{
		{ID: uuid.New(), ClusterID: cid, Namespace: "team-a", Name: "quota", Hard: []byte(`{"cpu":"32"}`), Used: []byte(`{"cpu":"4"}`), Scopes: []byte("[]"), Labels: []byte("{}"), Annotations: []byte("{}"), LastSeenAt: now, CreatedAt: now, UpdatedAt: now},
	}
	q.lr = []sqlc.MirroredLimitRange{
		{ID: uuid.New(), ClusterID: cid, Namespace: "team-a", Name: "defaults", Limits: []byte(`[{"type":"Container"}]`), Labels: []byte("{}"), Annotations: []byte("{}"), LastSeenAt: now, CreatedAt: now, UpdatedAt: now},
	}

	h := NewClusterResourcesHandler(q)

	cases := []struct {
		name        string
		invoke      http.HandlerFunc
		path        string
		minRowCount int
	}{
		{"ingress-classes", h.ListIngressClasses, "/clusters/" + cid.String() + "/ingress-classes/", 1},
		{"gateway-classes", h.ListGatewayClasses, "/clusters/" + cid.String() + "/gateway-classes/", 1},
		{"network-policies", h.ListNetworkPolicies, "/clusters/" + cid.String() + "/network-policies/", 2},
		{"resource-quotas", h.ListResourceQuotas, "/clusters/" + cid.String() + "/resource-quotas/", 1},
		{"limit-ranges", h.ListLimitRanges, "/clusters/" + cid.String() + "/limit-ranges/", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := withClusterParam(httptest.NewRequest(http.MethodGet, c.path, nil), cid)
			rec := httptest.NewRecorder()
			c.invoke(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d body=%s", rec.Code, rec.Body.String())
			}
			var body struct {
				Data  []map[string]any `json:"data"`
				Count int64            `json:"count"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode: %v body=%s", err, rec.Body.String())
			}
			if body.Count != int64(c.minRowCount) {
				t.Fatalf("count: got %d want %d body=%s", body.Count, c.minRowCount, rec.Body.String())
			}
			if len(body.Data) != c.minRowCount {
				t.Fatalf("data len: got %d want %d", len(body.Data), c.minRowCount)
			}
		})
	}
}

func TestCRDMirrorHandler_FilterByNamespace(t *testing.T) {
	cid := uuid.New()
	q := newFakeClusterResourcesQuerier(cid, "prod-1")
	now := time.Now()
	q.np = []sqlc.MirroredNetworkPolicy{
		{ID: uuid.New(), ClusterID: cid, Namespace: "prod-api", Name: "p1", Labels: []byte("{}"), Annotations: []byte("{}"), PodSelector: []byte("{}"), PolicyTypes: []byte("[]"), IngressRules: []byte("[]"), EgressRules: []byte("[]"), LastSeenAt: now, CreatedAt: now, UpdatedAt: now},
		{ID: uuid.New(), ClusterID: cid, Namespace: "kube-system", Name: "p2", Labels: []byte("{}"), Annotations: []byte("{}"), PodSelector: []byte("{}"), PolicyTypes: []byte("[]"), IngressRules: []byte("[]"), EgressRules: []byte("[]"), LastSeenAt: now, CreatedAt: now, UpdatedAt: now},
	}
	h := NewClusterResourcesHandler(q)

	req := withClusterParam(httptest.NewRequest(http.MethodGet, "/clusters/"+cid.String()+"/network-policies/?namespace=prod-api", nil), cid)
	rec := httptest.NewRecorder()
	h.ListNetworkPolicies(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Data  []map[string]any `json:"data"`
		Count int64            `json:"count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Count != 1 {
		t.Fatalf("expected 1 row in prod-api, got %d", body.Count)
	}
	if ns, _ := body.Data[0]["namespace"].(string); ns != "prod-api" {
		t.Fatalf("wrong namespace returned: %v", body.Data[0])
	}
}

func TestCRDMirrorHandler_RequiresClusterRead(t *testing.T) {
	// The route-layer RBAC test (in internal/server) verifies the actual
	// requirePermission(...rbac.ResourceClusters, rbac.VerbRead) gate.
	// Here we cover the handler's nil-queries degradation path and the
	// unknown-cluster 404 — the RBAC layer wouldn't get a chance to
	// run if the handler returned 503/404 first.

	// Unwired handler → 503.
	unwired := NewClusterResourcesHandler(nil)
	cid := uuid.New()
	req := withClusterParam(httptest.NewRequest(http.MethodGet, "/x", nil), cid)
	rec := httptest.NewRecorder()
	unwired.ListIngressClasses(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unwired: status %d body=%s", rec.Code, rec.Body.String())
	}

	// Unknown cluster → 404.
	q := newFakeClusterResourcesQuerier(uuid.New(), "real-cluster")
	h := NewClusterResourcesHandler(q)
	missing := uuid.New()
	req = withClusterParam(httptest.NewRequest(http.MethodGet, "/x", nil), missing)
	rec = httptest.NewRecorder()
	h.ListIngressClasses(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown cluster: status %d body=%s", rec.Code, rec.Body.String())
	}

	// Invalid UUID → 400.
	bad := httptest.NewRequest(http.MethodGet, "/x", nil)
	rc := chi.NewRouteContext()
	rc.URLParams.Add("cluster_id", "not-a-uuid")
	bad = bad.WithContext(context.WithValue(bad.Context(), chi.RouteCtxKey, rc))
	rec = httptest.NewRecorder()
	h.ListIngressClasses(rec, bad)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad uuid: status %d", rec.Code)
	}
}
