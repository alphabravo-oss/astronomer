package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

// TestSecurityMutatingRoutesRequireSecurityRBAC proves the mutating security
// routes reject a zero-grant viewer with 403. Before the fix these routes sat
// behind ONLY the feature-flag gate, so any authenticated principal could
// create/update/delete templates and policies and launch scans.
func TestSecurityMutatingRoutesRequireSecurityRBAC(t *testing.T) {
	jwtMgr := auth.MustNewJWTManager("route-security-test-secret", 60)
	token, err := jwtMgr.GenerateAccessToken(uuid.New())
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	router := NewRouter(&config.Config{}, RouterDependencies{
		JWT:         jwtMgr,
		RBACEngine:  rbac.NewEngine(),
		RBACQueries: routeSecurityRBACQuerier{bindings: routeSecurityReadOnlyBindings()},
		Security:    handler.NewSecurityHandler(nil),
	})

	for _, tc := range []struct {
		name   string
		method string
		path   string
	}{
		{"create_template", http.MethodPost, "/api/v1/security/templates/"},
		{"update_template", http.MethodPut, "/api/v1/security/templates/" + uuid.NewString() + "/"},
		{"delete_template", http.MethodDelete, "/api/v1/security/templates/" + uuid.NewString() + "/"},
		{"create_policy", http.MethodPost, "/api/v1/security/policies/"},
		{"apply_policy", http.MethodPost, "/api/v1/security/policies/" + uuid.NewString() + "/apply/"},
		{"delete_policy", http.MethodDelete, "/api/v1/security/policies/" + uuid.NewString() + "/"},
		{"create_scan", http.MethodPost, "/api/v1/security/scans/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.Header.Set("Authorization", "Bearer "+token)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s %s status = %d, want %d; body=%s", tc.method, tc.path, rec.Code, http.StatusForbidden, rec.Body.String())
			}
		})
	}
}

// TestCatalogRepositoryRoutesRequireCatalogRBAC proves repository CRUD refuses a
// zero-grant viewer, honoring the catalog:create/update/delete requirement that
// docs/security-sensitive-routes.json already declares.
func TestCatalogRepositoryRoutesRequireCatalogRBAC(t *testing.T) {
	jwtMgr := auth.MustNewJWTManager("route-security-test-secret", 60)
	token, err := jwtMgr.GenerateAccessToken(uuid.New())
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	router := NewRouter(&config.Config{}, RouterDependencies{
		JWT:         jwtMgr,
		RBACEngine:  rbac.NewEngine(),
		RBACQueries: routeSecurityRBACQuerier{bindings: routeSecurityReadOnlyBindings()},
		Catalog:     handler.NewCatalogHandler(nil),
	})

	for _, tc := range []struct {
		name   string
		method string
		path   string
	}{
		{"create_repo", http.MethodPost, "/api/v1/catalog/repositories/"},
		{"update_repo", http.MethodPut, "/api/v1/catalog/repositories/" + uuid.NewString() + "/"},
		{"delete_repo", http.MethodDelete, "/api/v1/catalog/repositories/" + uuid.NewString() + "/"},
		{"sync_repo", http.MethodPost, "/api/v1/catalog/repositories/" + uuid.NewString() + "/sync/"},
		{"test_connection", http.MethodPost, "/api/v1/catalog/repositories/" + uuid.NewString() + "/test-connection/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.Header.Set("Authorization", "Bearer "+token)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s %s status = %d, want %d; body=%s", tc.method, tc.path, rec.Code, http.StatusForbidden, rec.Body.String())
			}
		})
	}
}

// TestControllersMutatingRoutesRequireSuperuser proves the platform-admin
// controllers surface refuses a non-superuser principal with 403. Before the
// fix the mutating routes were reachable by any authenticated caller.
func TestControllersMutatingRoutesRequireSuperuser(t *testing.T) {
	jwtMgr := auth.MustNewJWTManager("route-security-test-secret", 60)
	userID := uuid.New()
	token, err := jwtMgr.GenerateAccessToken(userID)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	router := NewRouter(&config.Config{}, RouterDependencies{
		JWT: jwtMgr,
		// Non-superuser user resolved by the superuser gate.
		AuthQueries:  routeSecurityTokenAuthQuerier{user: sqlc.User{ID: userID, IsActive: true}},
		RBACEngine:   rbac.NewEngine(),
		RBACQueries:  routeSecurityRBACQuerier{bindings: routeSecurityAdminBindings()},
		ControlPlane: handler.NewControlPlaneHandler(nil, nil, nil, nil, nil, nil, nil, nil),
	})

	for _, tc := range []struct {
		name   string
		method string
		path   string
	}{
		{"update_policy", http.MethodPut, "/api/v1/controllers/policy/"},
		{"acknowledge_alert", http.MethodPost, "/api/v1/controllers/alerts/" + uuid.NewString() + "/acknowledge/"},
		{"create_silence", http.MethodPost, "/api/v1/controllers/silences/"},
		{"delete_silence", http.MethodDelete, "/api/v1/controllers/silences/" + uuid.NewString() + "/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.Header.Set("Authorization", "Bearer "+token)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s %s status = %d, want %d; body=%s", tc.method, tc.path, rec.Code, http.StatusForbidden, rec.Body.String())
			}
		})
	}
}

// monitoringSettingsReadRoutes are the three /settings/monitoring routes that
// were registered on the bare settings group and carried no authorization call
// of their own: GET backend returned the decoded authConfig, and both previews
// rendered the shared stack's helm values, all to an anonymous caller.
var monitoringSettingsReadRoutes = []struct {
	name string
	// method + path as the frontend/CLI would call them.
	method string
	path   string
	// status once the caller holds monitoring:read — the handlers run with a
	// nil monitoring store here, so "through the gate" is 200 for the backend
	// read and 400 ("monitoring store not configured") for the previews.
	allowedStatus int
}{
	{"backend_config", http.MethodGet, "/api/v1/settings/monitoring/backend/", http.StatusOK},
	{"thanos_preview", http.MethodPost, "/api/v1/settings/monitoring/thanos/preview/", http.StatusBadRequest},
	{"alertmanager_preview", http.MethodPost, "/api/v1/settings/monitoring/alertmanager/preview/", http.StatusBadRequest},
}

func newMonitoringAuthzRouter(jwtMgr *auth.JWTManager, bindings []rbac.RoleBinding) chi.Router {
	engine := rbac.NewEngine()
	querier := routeSecurityRBACQuerier{bindings: bindings}
	monitoring := handler.NewMonitoringHandler()
	monitoring.SetAuthorization(engine, querier)
	return NewRouter(&config.Config{}, RouterDependencies{
		JWT:         jwtMgr,
		RBACEngine:  engine,
		RBACQueries: querier,
		// The /settings group only exists when Resources is wired, and the
		// monitoring routes hang off it.
		Resources:  handler.NewResourceHandler(),
		Monitoring: monitoring,
	})
}

// TestMonitoringSettingsReadRoutesRequireMonitoringRBAC proves the three
// monitoring settings read routes reject an anonymous caller (401) and an
// authenticated caller without monitoring permission (403), while a holder of
// monitoring:read still reaches the handler. Before the fix the anonymous case
// returned 200 with the backend's decoded authConfig / rendered helm values.
func TestMonitoringSettingsReadRoutesRequireMonitoringRBAC(t *testing.T) {
	jwtMgr := auth.MustNewJWTManager("route-security-test-secret", 60)
	token, err := jwtMgr.GenerateAccessToken(uuid.New())
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}

	// Authentication alone must not be enough: this principal holds every verb
	// on clusters and nothing on monitoring.
	noMonitoringRBAC := newMonitoringAuthzRouter(jwtMgr, routeSecurityBindings(rbac.ResourceClusters, rbac.VerbRead, rbac.VerbList, rbac.VerbUpdate))
	withMonitoringRBAC := newMonitoringAuthzRouter(jwtMgr, routeSecurityBindings(rbac.ResourceMonitoring, rbac.VerbRead))

	for _, tc := range monitoringSettingsReadRoutes {
		t.Run(tc.name, func(t *testing.T) {
			anonReq := httptest.NewRequest(tc.method, tc.path, nil)
			anonRec := httptest.NewRecorder()
			noMonitoringRBAC.ServeHTTP(anonRec, anonReq)
			if anonRec.Code != http.StatusUnauthorized {
				t.Fatalf("anonymous %s %s status = %d, want %d; body=%s", tc.method, tc.path, anonRec.Code, http.StatusUnauthorized, anonRec.Body.String())
			}

			deniedReq := httptest.NewRequest(tc.method, tc.path, nil)
			deniedReq.Header.Set("Authorization", "Bearer "+token)
			deniedRec := httptest.NewRecorder()
			noMonitoringRBAC.ServeHTTP(deniedRec, deniedReq)
			if deniedRec.Code != http.StatusForbidden {
				t.Fatalf("authenticated without monitoring RBAC %s %s status = %d, want %d; body=%s", tc.method, tc.path, deniedRec.Code, http.StatusForbidden, deniedRec.Body.String())
			}

			allowedReq := httptest.NewRequest(tc.method, tc.path, nil)
			allowedReq.Header.Set("Authorization", "Bearer "+token)
			allowedRec := httptest.NewRecorder()
			withMonitoringRBAC.ServeHTTP(allowedRec, allowedReq)
			if allowedRec.Code != tc.allowedStatus {
				t.Fatalf("monitoring:read %s %s status = %d, want %d; body=%s", tc.method, tc.path, allowedRec.Code, tc.allowedStatus, allowedRec.Body.String())
			}
		})
	}
}

// TestMonitoringSettingsReadRoutesDenyUnauthenticatedWithoutHandlerAuthz proves
// the router-level requireAuth is a genuine second line of defence: even with
// the handler's own authorization support unwired (engine nil, which makes
// authorizeGlobalAction fall through to "allowed"), an anonymous request never
// reaches the handler.
func TestMonitoringSettingsReadRoutesDenyUnauthenticatedWithoutHandlerAuthz(t *testing.T) {
	router := NewRouter(&config.Config{}, RouterDependencies{
		JWT:        auth.MustNewJWTManager("route-security-test-secret", 60),
		Resources:  handler.NewResourceHandler(),
		Monitoring: handler.NewMonitoringHandler(),
	})
	for _, tc := range monitoringSettingsReadRoutes {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("anonymous %s %s status = %d, want %d; body=%s", tc.method, tc.path, rec.Code, http.StatusUnauthorized, rec.Body.String())
			}
		})
	}
}

// clusterMonitoringRoutes are the eight per-cluster monitoring routes from
// registerClusterRoutes, with the exact per-route verb each is mounted with.
// The verbs differ from the shared-stack family's (install is create, uninstall
// is delete), which is part of why the per-cluster handlers are gated by
// middleware rather than by an in-handler preamble.
var clusterMonitoringRoutes = []struct {
	name   string
	method string
	path   string
}{
	{"config_get", http.MethodGet, "/config/"},
	{"config_put", http.MethodPut, "/config/"},
	{"stack_status", http.MethodGet, "/stack/status/"},
	{"stack_preview", http.MethodPost, "/stack/preview/"},
	{"stack_install", http.MethodPost, "/stack/install/"},
	{"stack_upgrade", http.MethodPut, "/stack/upgrade/"},
	{"stack_replace", http.MethodPost, "/stack/replace/"},
	{"stack_uninstall", http.MethodDelete, "/stack/uninstall/"},
}

func newClusterMonitoringAuthzRouter(jwtMgr *auth.JWTManager, bindings []rbac.RoleBinding) chi.Router {
	engine := rbac.NewEngine()
	querier := routeSecurityRBACQuerier{bindings: bindings}
	monitoring := handler.NewMonitoringHandler()
	monitoring.SetAuthorization(engine, querier)
	clusters := handler.NewClusterHandler(&routeSecurityClusterQuerier{})
	clusters.SetAuthorization(engine, querier)
	return NewRouter(&config.Config{}, RouterDependencies{
		JWT:         jwtMgr,
		RBACEngine:  engine,
		RBACQueries: querier,
		// The /clusters group only exists when Clusters is wired, and the
		// per-cluster monitoring routes hang off it.
		Clusters:   clusters,
		Monitoring: monitoring,
	})
}

// TestClusterMonitoringRoutesRequireMonitoringRBAC is the fence for the eight
// handlers in internal/handler/monitoring_stack_cluster.go, which deliberately
// carry no in-handler authorization: their entire gate is the requirePermission
// wrapper mounted per route in routes_clusters.go.
//
// Nothing else covers that wrapper. These routes are mounted under the
// `authenticated` group, so removing requirePermission still leaves an
// anonymous caller with 401 and TestEveryRouteDeniesUnauthenticatedRequests
// green; both chi.Walk callbacks in routes_security_test.go discard the
// middleware chain, and the route-risk registries are static (method, pattern)
// maps. So this test drives an AUTHENTICATED principal holding every verb on
// clusters and nothing on monitoring, and requires 403 from all eight — the
// only assertion in the tree that fails if a wrapper is dropped.
func TestClusterMonitoringRoutesRequireMonitoringRBAC(t *testing.T) {
	jwtMgr := auth.MustNewJWTManager("route-security-test-secret", 60)
	token, err := jwtMgr.GenerateAccessToken(uuid.New())
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	base := "/api/v1/clusters/" + uuid.NewString() + "/monitoring"

	noMonitoringRBAC := newClusterMonitoringAuthzRouter(jwtMgr, routeSecurityBindings(
		rbac.ResourceClusters, rbac.VerbRead, rbac.VerbList, rbac.VerbCreate, rbac.VerbUpdate, rbac.VerbDelete))
	withMonitoringRBAC := newClusterMonitoringAuthzRouter(jwtMgr, routeSecurityBindings(
		rbac.ResourceMonitoring, rbac.VerbRead, rbac.VerbCreate, rbac.VerbUpdate, rbac.VerbDelete))

	for _, tc := range clusterMonitoringRoutes {
		t.Run(tc.name, func(t *testing.T) {
			path := base + tc.path

			anonReq := httptest.NewRequest(tc.method, path, nil)
			anonRec := httptest.NewRecorder()
			noMonitoringRBAC.ServeHTTP(anonRec, anonReq)
			if anonRec.Code != http.StatusUnauthorized {
				t.Fatalf("anonymous %s %s status = %d, want %d; body=%s", tc.method, path, anonRec.Code, http.StatusUnauthorized, anonRec.Body.String())
			}

			deniedReq := httptest.NewRequest(tc.method, path, nil)
			deniedReq.Header.Set("Authorization", "Bearer "+token)
			deniedRec := httptest.NewRecorder()
			noMonitoringRBAC.ServeHTTP(deniedRec, deniedReq)
			if deniedRec.Code != http.StatusForbidden {
				t.Fatalf("authenticated without monitoring RBAC %s %s status = %d, want %d; body=%s", tc.method, path, deniedRec.Code, http.StatusForbidden, deniedRec.Body.String())
			}

			// Positive control: the identical request must get PAST the gate for
			// a monitoring grant holder, so the 403 above can only have come
			// from authorization. The handler runs with no store or helm
			// requester wired, so it answers 2xx/4xx/5xx by endpoint — anything
			// but 403.
			allowedReq := httptest.NewRequest(tc.method, path, nil)
			allowedReq.Header.Set("Authorization", "Bearer "+token)
			allowedRec := httptest.NewRecorder()
			withMonitoringRBAC.ServeHTTP(allowedRec, allowedReq)
			if allowedRec.Code == http.StatusForbidden {
				t.Fatalf("monitoring grant holder ALSO got 403 on %s %s — this test is not measuring authorization; body=%s", tc.method, path, allowedRec.Body.String())
			}
		})
	}
}
