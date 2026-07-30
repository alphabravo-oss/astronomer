package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

// The per-cluster monitoring routes are mounted as /clusters/{id}/... and gate
// on rbac.ResourceMonitoring. Until ClusterScopeFromIDParam was mounted on the
// /clusters subtree the gate could not tell that {id} was a cluster — the bare
// {id} fallback in permissionScope only fired for rbac.ResourceClusters — so
// every one of them was evaluated at uuid.Nil, as a GLOBAL monitoring check.
//
// The tests below pin all four directions of the resulting scope contract. They
// are table-driven over all six per-cluster stack verbs plus the two config
// routes because those eight carry four different permissions
// (read/read/create/update/update/delete on the stack, read/update on config)
// and a scope bug can hide behind any single one of them.

// monitoringBindingOnCluster is a monitoring grant scoped to exactly one
// cluster — the shape a cluster_role_bindings row produces for Cluster Owner,
// Cluster Operator, Cluster Viewer, Cluster Member or Service Mesh Operator.
func monitoringBindingOnCluster(clusterID uuid.UUID, verbs ...rbac.Verb) []rbac.RoleBinding {
	values := make([]string, 0, len(verbs))
	for _, verb := range verbs {
		values = append(values, string(verb))
	}
	return []rbac.RoleBinding{{
		ClusterID: clusterID.String(),
		RoleRules: []rbac.Rule{{Resource: string(rbac.ResourceMonitoring), Verbs: values}},
	}}
}

func allMonitoringVerbs() []rbac.Verb {
	return []rbac.Verb{rbac.VerbRead, rbac.VerbList, rbac.VerbCreate, rbac.VerbUpdate, rbac.VerbDelete}
}

// clusterMonitoringRequest builds an authenticated request for one of the
// routes under test.
func clusterMonitoringRequest(token, method, clusterPath string) *http.Request {
	req := httptest.NewRequest(method, clusterPath, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

// TestClusterMonitoringRoutesHonourClusterScopedBinding is the ALLOW direction:
// a caller whose monitoring grant names exactly this cluster must get past the
// gate on this cluster's routes.
//
// PRE-FIX: every subtest failed with 403 — the gate resolved uuid.Nil, the
// binding carries a cluster id, and rbac.bindingApplies rejects a cluster
// binding against the nil scope. A Cluster Owner (rules "*"/"*", cluster scope)
// could not read, let alone install, their own cluster's monitoring stack.
func TestClusterMonitoringRoutesHonourClusterScopedBinding(t *testing.T) {
	jwtMgr := auth.MustNewJWTManager("route-security-test-secret", 60)
	token, err := jwtMgr.GenerateAccessToken(uuid.New())
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	clusterX := uuid.New()
	router := newClusterMonitoringAuthzRouter(jwtMgr, monitoringBindingOnCluster(clusterX, allMonitoringVerbs()...))
	base := "/api/v1/clusters/" + clusterX.String() + "/monitoring"

	for _, tc := range clusterMonitoringRoutes {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, clusterMonitoringRequest(token, tc.method, base+tc.path))
			if rec.Code == http.StatusForbidden {
				t.Fatalf("%s %s: cluster-scoped monitoring grant on THIS cluster got 403; body=%s",
					tc.method, base+tc.path, rec.Body.String())
			}
		})
	}
}

// TestClusterMonitoringRoutesRejectBindingOnAnotherCluster is the DENY
// direction: the same binding must not open another cluster's routes.
//
// A bare "403 on cluster Y" assertion would pass for the wrong reason, because
// PRE-FIX the cluster-scoped caller was denied EVERYWHERE. Each subtest
// therefore carries the cluster-X allow as its control, which is what fails
// pre-fix and is what makes the Y denial mean "the gate compared the clusters"
// instead of "the gate ignored them".
func TestClusterMonitoringRoutesRejectBindingOnAnotherCluster(t *testing.T) {
	jwtMgr := auth.MustNewJWTManager("route-security-test-secret", 60)
	token, err := jwtMgr.GenerateAccessToken(uuid.New())
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	clusterX, clusterY := uuid.New(), uuid.New()
	router := newClusterMonitoringAuthzRouter(jwtMgr, monitoringBindingOnCluster(clusterX, allMonitoringVerbs()...))
	granted := "/api/v1/clusters/" + clusterX.String() + "/monitoring"
	other := "/api/v1/clusters/" + clusterY.String() + "/monitoring"

	for _, tc := range clusterMonitoringRoutes {
		t.Run(tc.name, func(t *testing.T) {
			denied := httptest.NewRecorder()
			router.ServeHTTP(denied, clusterMonitoringRequest(token, tc.method, other+tc.path))
			if denied.Code != http.StatusForbidden {
				t.Fatalf("%s %s: monitoring grant scoped to a DIFFERENT cluster got %d, want 403; body=%s",
					tc.method, other+tc.path, denied.Code, denied.Body.String())
			}

			control := httptest.NewRecorder()
			router.ServeHTTP(control, clusterMonitoringRequest(token, tc.method, granted+tc.path))
			if control.Code == http.StatusForbidden {
				t.Fatalf("%s %s: the SAME caller is 403 on the cluster they are bound to, so the denial above is not measuring scope; body=%s",
					tc.method, granted+tc.path, control.Body.String())
			}
		})
	}
}

// TestClusterMonitoringRoutesStillAdmitGlobalBinding is the no-regression
// direction. A global monitoring grant (Monitoring Admin, Platform Operator,
// Support Engineer, the wildcard admin roles) reaches every cluster's routes,
// as it did before, because rbac.bindingApplies short-circuits a binding with
// no cluster and no project to true at any scope.
//
// PRE-FIX: passed. It is here so a future narrowing of the resolver cannot
// silently lock the fleet-wide operators out.
func TestClusterMonitoringRoutesStillAdmitGlobalBinding(t *testing.T) {
	jwtMgr := auth.MustNewJWTManager("route-security-test-secret", 60)
	token, err := jwtMgr.GenerateAccessToken(uuid.New())
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	router := newClusterMonitoringAuthzRouter(jwtMgr, routeSecurityBindings(rbac.ResourceMonitoring, allMonitoringVerbs()...))

	for i, clusterID := range []uuid.UUID{uuid.New(), uuid.New()} {
		base := "/api/v1/clusters/" + clusterID.String() + "/monitoring"
		for _, tc := range clusterMonitoringRoutes {
			t.Run(fmt.Sprintf("cluster%d/%s", i, tc.name), func(t *testing.T) {
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, clusterMonitoringRequest(token, tc.method, base+tc.path))
				if rec.Code == http.StatusForbidden {
					t.Fatalf("%s %s: GLOBAL monitoring grant got 403; body=%s",
						tc.method, base+tc.path, rec.Body.String())
				}
			})
		}
	}
}

// TestClusterMonitoringRoutesDenyCallerWithNoBinding is the floor: no
// monitoring grant at any scope means 403 on every cluster. TestCluster-
// MonitoringRoutesRequireMonitoringRBAC covers the same ground for a caller who
// holds every CLUSTERS verb; this one holds nothing at all, which is what
// catches a resolver that accidentally treats "no binding" as unrestricted.
//
// PRE-FIX: passed.
func TestClusterMonitoringRoutesDenyCallerWithNoBinding(t *testing.T) {
	jwtMgr := auth.MustNewJWTManager("route-security-test-secret", 60)
	token, err := jwtMgr.GenerateAccessToken(uuid.New())
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	clusterX := uuid.New()
	router := newClusterMonitoringAuthzRouter(jwtMgr, nil)
	base := "/api/v1/clusters/" + clusterX.String() + "/monitoring"

	for _, tc := range clusterMonitoringRoutes {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, clusterMonitoringRequest(token, tc.method, base+tc.path))
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s %s: caller with NO bindings got %d, want 403; body=%s",
					tc.method, base+tc.path, rec.Code, rec.Body.String())
			}
		})
	}
}

// sharedMonitoringRoutes are the fleet-wide Thanos/Alertmanager lifecycle
// routes under /api/v1/settings/monitoring/. They are a different mount with
// different semantics: they run helm against the management cluster, so they
// are authorized IN the handler against uuid.Nil (authorizeGlobalAction) and
// must stay global.
var sharedMonitoringRoutes = []struct {
	name   string
	method string
	path   string
}{
	{"thanos_status", http.MethodGet, "/thanos/status/"},
	{"thanos_preview", http.MethodPost, "/thanos/preview/"},
	{"thanos_install", http.MethodPost, "/thanos/install/"},
	{"thanos_upgrade", http.MethodPut, "/thanos/upgrade/"},
	{"thanos_replace", http.MethodPost, "/thanos/replace/"},
	{"thanos_uninstall", http.MethodDelete, "/thanos/uninstall/"},
	{"alertmanager_status", http.MethodGet, "/alertmanager/status/"},
	{"alertmanager_preview", http.MethodPost, "/alertmanager/preview/"},
	{"alertmanager_install", http.MethodPost, "/alertmanager/install/"},
	{"alertmanager_upgrade", http.MethodPut, "/alertmanager/upgrade/"},
	{"alertmanager_replace", http.MethodPost, "/alertmanager/replace/"},
	{"alertmanager_uninstall", http.MethodDelete, "/alertmanager/uninstall/"},
	{"backend_get", http.MethodGet, "/backend/"},
	{"backend_put", http.MethodPut, "/backend/"},
}

// newSharedMonitoringAuthzRouter wires the /settings group as well, since the
// shared-stack routes hang off it and only exist when Resources is non-nil.
func newSharedMonitoringAuthzRouter(jwtMgr *auth.JWTManager, bindings []rbac.RoleBinding) chi.Router {
	engine := rbac.NewEngine()
	querier := routeSecurityRBACQuerier{bindings: bindings}
	monitoring := handler.NewMonitoringHandler()
	monitoring.SetAuthorization(engine, querier)
	return NewRouter(&config.Config{}, RouterDependencies{
		JWT:         jwtMgr,
		RBACEngine:  engine,
		RBACQueries: querier,
		Resources:   handler.NewResourceHandler(),
		Monitoring:  monitoring,
	})
}

// TestSharedMonitoringRoutesStayGlobalForClusterScopedBinding is the trap.
// Teaching the gate that {id} names a cluster must not make a cluster-scoped
// monitoring grant satisfy a route that is genuinely fleet-wide. These routes
// live outside the /clusters subtree, so the declaration never reaches them,
// and their in-handler check is hard-coded to uuid.Nil.
//
// PRE-FIX: passed. It is the regression fence for a careless widening — e.g.
// dropping the resource condition and resolving a cluster scope from any {id},
// or hoisting the declaration above the /clusters mount.
func TestSharedMonitoringRoutesStayGlobalForClusterScopedBinding(t *testing.T) {
	jwtMgr := auth.MustNewJWTManager("route-security-test-secret", 60)
	token, err := jwtMgr.GenerateAccessToken(uuid.New())
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	clusterX := uuid.New()
	scoped := newSharedMonitoringAuthzRouter(jwtMgr, monitoringBindingOnCluster(clusterX, allMonitoringVerbs()...))
	global := newSharedMonitoringAuthzRouter(jwtMgr, routeSecurityBindings(rbac.ResourceMonitoring, allMonitoringVerbs()...))

	for _, tc := range sharedMonitoringRoutes {
		t.Run(tc.name, func(t *testing.T) {
			path := "/api/v1/settings/monitoring" + tc.path

			rec := httptest.NewRecorder()
			scoped.ServeHTTP(rec, clusterMonitoringRequest(token, tc.method, path))
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s %s: monitoring grant scoped to one CLUSTER got %d on a fleet-wide route, want 403; body=%s",
					tc.method, path, rec.Code, rec.Body.String())
			}

			// Positive control: the same request must clear the gate for a
			// global grant, so the 403 above measures scope and not some
			// unrelated precondition.
			allowed := httptest.NewRecorder()
			global.ServeHTTP(allowed, clusterMonitoringRequest(token, tc.method, path))
			if allowed.Code == http.StatusForbidden {
				t.Fatalf("%s %s: GLOBAL monitoring grant ALSO got 403 — this test is not measuring scope; body=%s",
					tc.method, path, allowed.Body.String())
			}
		})
	}
}

// TestClusterHealthRouteHonoursClusterScopedMonitoringBinding covers the ninth
// live victim of the same defect. GET /clusters/{id}/health/ is the only
// per-cluster route outside the /monitoring subtree that gates on
// rbac.ResourceMonitoring, and it is what the cluster-detail page calls first,
// so it failed for a cluster-scoped operator before anything else did.
//
// PRE-FIX: the cluster-scoped subtest failed with 403.
func TestClusterHealthRouteHonoursClusterScopedMonitoringBinding(t *testing.T) {
	jwtMgr := auth.MustNewJWTManager("route-security-test-secret", 60)
	token, err := jwtMgr.GenerateAccessToken(uuid.New())
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	clusterX, clusterY := uuid.New(), uuid.New()
	router := newClusterMonitoringAuthzRouter(jwtMgr, monitoringBindingOnCluster(clusterX, rbac.VerbRead, rbac.VerbList))

	own := httptest.NewRecorder()
	router.ServeHTTP(own, clusterMonitoringRequest(token, http.MethodGet, "/api/v1/clusters/"+clusterX.String()+"/health/"))
	if own.Code == http.StatusForbidden {
		t.Fatalf("cluster-scoped monitoring:read got 403 on its OWN cluster's health; body=%s", own.Body.String())
	}

	other := httptest.NewRecorder()
	router.ServeHTTP(other, clusterMonitoringRequest(token, http.MethodGet, "/api/v1/clusters/"+clusterY.String()+"/health/"))
	if other.Code != http.StatusForbidden {
		t.Fatalf("cluster-scoped monitoring:read got %d on ANOTHER cluster's health, want 403; body=%s", other.Code, other.Body.String())
	}
}
