package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/tunnel"
)

// proxyPermissionRouter wires the k8s passthrough proxy with a JWT identity
// and a single RBAC binding so a request's resolved (resource, verb) mapping
// can be observed via the RBAC gate's allow (proxy 503, tunnel not connected)
// vs deny (403) outcome. JWT auth deliberately bypasses scope enforcement so
// the test isolates the RBAC *mapping* under examination.
func newProxyPermissionRouter(t *testing.T, bindings []rbac.RoleBinding) (http.Handler, string) {
	t.Helper()
	jwtMgr := auth.NewJWTManager("route-security-test-secret", 60)
	userID := uuid.New()
	token, err := jwtMgr.GenerateAccessToken(userID)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	router := NewRouter(&config.Config{}, RouterDependencies{
		JWT:         jwtMgr,
		RBACEngine:  rbac.NewEngine(),
		RBACQueries: routeSecurityRBACQuerier{bindings: bindings},
		Proxy:       tunnel.NewProxyHandler(tunnel.NewHub(slog.Default()), slog.Default()),
	})
	return router, token
}

func proxyRequest(h http.Handler, method, path, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestProxyExecAlwaysRequiresPodsExec is the F1 (M2) negative test: pod
// exec/attach/portforward via the generic k8s proxy must ALWAYS map to
// pods:exec across multiple well-formed path shapes. A token holding only a
// generic pods write verb (update/create/delete) — but NOT exec — is denied,
// while a pods:exec token reaches the proxy handler. This proves the
// subresource can no longer degrade to a generic pod write verb.
func TestProxyExecAlwaysRequiresPodsExec(t *testing.T) {
	clusterID := uuid.New().String()
	base := "/api/v1/clusters/" + clusterID + "/k8s"

	// Each shape is a distinct, well-formed exec/attach/portforward URL the
	// old hardcoded api/v1-trailing-segment matcher could miss.
	shapes := []struct {
		name   string
		method string
		path   string
	}{
		{"exec", http.MethodPost, base + "/api/v1/namespaces/default/pods/web-0/exec"},
		{"exec trailing slash", http.MethodPost, base + "/api/v1/namespaces/default/pods/web-0/exec/"},
		{"exec GET upgrade", http.MethodGet, base + "/api/v1/namespaces/default/pods/web-0/exec"},
		{"attach", http.MethodPost, base + "/api/v1/namespaces/default/pods/web-0/attach"},
		{"portforward", http.MethodPost, base + "/api/v1/namespaces/default/pods/web-0/portforward"},
	}

	// A binding with every generic pod write verb EXCEPT exec. If exec
	// degraded to one of these, the request would wrongly be allowed.
	podWriteNoExec := []rbac.RoleBinding{{RoleRules: []rbac.Rule{{
		Resource: string(rbac.ResourcePods),
		Verbs:    []string{"create", "update", "delete", "read", "list"},
	}}}}
	podExec := routeSecurityPodExecBindings()

	for _, s := range shapes {
		t.Run(s.name+" denied without pods:exec", func(t *testing.T) {
			router, token := newProxyPermissionRouter(t, podWriteNoExec)
			rec := proxyRequest(router, s.method, s.path, token)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s without pods:exec status = %d, want %d; body=%s", s.name, rec.Code, http.StatusForbidden, rec.Body.String())
			}
		})

		t.Run(s.name+" allowed with pods:exec", func(t *testing.T) {
			router, token := newProxyPermissionRouter(t, podExec)
			rec := proxyRequest(router, s.method, s.path, token)
			if rec.Code == http.StatusForbidden {
				t.Fatalf("%s with pods:exec status = %d (forbidden); exec mapping should permit; body=%s", s.name, rec.Code, rec.Body.String())
			}
		})
	}
}

// TestProxyCustomResourceMapsToCustomResourcesPermission is the F2 (M3)
// negative test: a CRD GET/POST under apis/<group>/<version>/... maps to the
// dedicated custom_resources permission, NOT the generic clusters verb. A
// clusters:read/update binding is therefore denied, while a custom_resources
// binding is allowed — proving per-resource RBAC now governs CRDs.
func TestProxyCustomResourceMapsToCustomResourcesPermission(t *testing.T) {
	clusterID := uuid.New().String()
	base := "/api/v1/clusters/" + clusterID + "/k8s"
	crGetPath := base + "/apis/cert-manager.io/v1/namespaces/default/certificates/my-cert"
	crListPath := base + "/apis/cert-manager.io/v1/namespaces/default/certificates"
	crCreatePath := base + "/apis/cert-manager.io/v1/namespaces/default/certificates"

	t.Run("GET denied with clusters:read only", func(t *testing.T) {
		router, token := newProxyPermissionRouter(t, routeSecurityBindings(rbac.ResourceClusters, rbac.VerbRead, rbac.VerbList))
		rec := proxyRequest(router, http.MethodGet, crGetPath, token)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("CRD GET with clusters:read status = %d, want %d; body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
		}
	})

	t.Run("GET allowed with custom_resources:read", func(t *testing.T) {
		router, token := newProxyPermissionRouter(t, routeSecurityBindings(rbac.ResourceCustomResources, rbac.VerbRead, rbac.VerbList))
		rec := proxyRequest(router, http.MethodGet, crGetPath, token)
		if rec.Code == http.StatusForbidden {
			t.Fatalf("CRD GET with custom_resources:read status = %d (forbidden); body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("LIST allowed with custom_resources:list", func(t *testing.T) {
		router, token := newProxyPermissionRouter(t, routeSecurityBindings(rbac.ResourceCustomResources, rbac.VerbList))
		rec := proxyRequest(router, http.MethodGet, crListPath, token)
		if rec.Code == http.StatusForbidden {
			t.Fatalf("CRD LIST with custom_resources:list status = %d (forbidden); body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("POST denied with clusters:update only", func(t *testing.T) {
		router, token := newProxyPermissionRouter(t, routeSecurityBindings(rbac.ResourceClusters, rbac.VerbUpdate, rbac.VerbCreate))
		rec := proxyRequest(router, http.MethodPost, crCreatePath, token)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("CRD POST with clusters:update status = %d, want %d; body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
		}
	})

	t.Run("POST allowed with custom_resources:create", func(t *testing.T) {
		router, token := newProxyPermissionRouter(t, routeSecurityBindings(rbac.ResourceCustomResources, rbac.VerbCreate))
		rec := proxyRequest(router, http.MethodPost, crCreatePath, token)
		if rec.Code == http.StatusForbidden {
			t.Fatalf("CRD POST with custom_resources:create status = %d (forbidden); body=%s", rec.Code, rec.Body.String())
		}
	})

	// Non-resource discovery URLs stay read-only on the generic clusters
	// verb: a clusters:read token can reach /version, /api, /apis.
	for _, disc := range []string{base + "/version", base + "/api", base + "/apis", base + "/healthz"} {
		t.Run("discovery read allowed "+disc, func(t *testing.T) {
			router, token := newProxyPermissionRouter(t, routeSecurityBindings(rbac.ResourceClusters, rbac.VerbRead, rbac.VerbList))
			rec := proxyRequest(router, http.MethodGet, disc, token)
			if rec.Code == http.StatusForbidden {
				t.Fatalf("discovery GET %s with clusters:read status = %d (forbidden); body=%s", disc, rec.Code, rec.Body.String())
			}
		})
	}
}

// TestProxyEvictionRequiresPodsDelete is the F3 (L1) negative test: POST to
// pods/{name}/eviction deletes the pod and must require pods:delete, not a
// generic pod update. A pods:update (no delete) binding is denied; pods:delete
// is allowed.
func TestProxyEvictionRequiresPodsDelete(t *testing.T) {
	clusterID := uuid.New().String()
	evictPath := "/api/v1/clusters/" + clusterID + "/k8s/api/v1/namespaces/default/pods/web-0/eviction"

	t.Run("denied with pods:update only", func(t *testing.T) {
		router, token := newProxyPermissionRouter(t, routeSecurityBindings(rbac.ResourcePods, rbac.VerbUpdate, rbac.VerbCreate, rbac.VerbRead, rbac.VerbList))
		rec := proxyRequest(router, http.MethodPost, evictPath, token)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("eviction with pods:update status = %d, want %d; body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
		}
	})

	t.Run("allowed with pods:delete", func(t *testing.T) {
		router, token := newProxyPermissionRouter(t, routeSecurityBindings(rbac.ResourcePods, rbac.VerbDelete))
		rec := proxyRequest(router, http.MethodPost, evictPath, token)
		if rec.Code == http.StatusForbidden {
			t.Fatalf("eviction with pods:delete status = %d (forbidden); body=%s", rec.Code, rec.Body.String())
		}
	})
}

// newHelmMutationRouter wires the Catalog + Monitoring handlers with API-token
// auth so the NEW-1 write-scope backstop on the helm lifecycle routes can be
// exercised by a read- vs write-scoped token.
func newHelmMutationRouter(rawToken string, userID uuid.UUID, scopes json.RawMessage, bindings []rbac.RoleBinding) http.Handler {
	jwtMgr := auth.NewJWTManager("route-security-test-secret", 60)
	return NewRouter(&config.Config{}, RouterDependencies{
		JWT:         jwtMgr,
		AuthQueries: routeSecurityAPITokenQuerier(rawToken, userID, scopes),
		RBACEngine:  rbac.NewEngine(),
		RBACQueries: routeSecurityRBACQuerier{bindings: bindings},
		Catalog:     handler.NewCatalogHandler(nil),
		Monitoring:  handler.NewMonitoringHandler(),
		// Resources must be wired so the early /api/v1/settings route group
		// (which hosts the shared monitoring-stack routes) is registered.
		Resources: handler.NewResourceHandler(),
	})
}

// TestHelmMutationRoutesRejectReadScopedTokens is the NEW-1 negative test
// (same class as H1): the catalog helm install/upgrade/uninstall and the
// shared monitoring-stack install/upgrade/uninstall routes — cluster-mutating
// helm operations that previously lacked any scope backstop — must reject a
// read-scoped API token with 403 scope_denied, while a clusters:write token
// passes the scope gate and reaches the handler.
func TestHelmMutationRoutesRejectReadScopedTokens(t *testing.T) {
	userID := uuid.New()
	installID := uuid.New().String()

	cases := []struct {
		name   string
		method string
		path   string
		// body is sent so handlers that decode-then-touch-DB return an
		// early client error (proving the scope gate let the request through)
		// rather than panicking on the nil querier.
		body string
	}{
		{"catalog install", http.MethodPost, "/api/v1/catalog/installed/", "not-json"},
		{"catalog upgrade", http.MethodPut, "/api/v1/catalog/installed/" + installID + "/upgrade/", "not-json"},
		{"catalog uninstall", http.MethodDelete, "/api/v1/catalog/installed/not-a-uuid/", ""},
		{"monitoring thanos install", http.MethodPost, "/api/v1/settings/monitoring/thanos/install/", "not-json"},
		{"monitoring thanos uninstall", http.MethodDelete, "/api/v1/settings/monitoring/thanos/uninstall/", ""},
		{"monitoring alertmanager install", http.MethodPost, "/api/v1/settings/monitoring/alertmanager/install/", "not-json"},
		{"monitoring alertmanager uninstall", http.MethodDelete, "/api/v1/settings/monitoring/alertmanager/uninstall/", ""},
	}

	// A binding broad enough that RBAC never blocks; the scope gate is the
	// control under test.
	fullRBAC := routeSecurityAdminBindings()

	for _, tc := range cases {
		t.Run(tc.name+" read token denied", func(t *testing.T) {
			rawToken := "astro_helm_read_" + strings.ReplaceAll(tc.name, " ", "_")
			router := newHelmMutationRouter(rawToken, userID, json.RawMessage(`["read"]`), fullRBAC)
			rec := doRequest(router, tc.method, tc.path, rawToken, tc.body)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s read-scoped status = %d, want %d; body=%s", tc.name, rec.Code, http.StatusForbidden, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), "scope_denied") {
				t.Fatalf("%s read-scoped body = %s, want scope_denied", tc.name, rec.Body.String())
			}
		})

		t.Run(tc.name+" write token passes scope gate", func(t *testing.T) {
			rawToken := "astro_helm_write_" + strings.ReplaceAll(tc.name, " ", "_")
			router := newHelmMutationRouter(rawToken, userID, json.RawMessage(`["clusters:write"]`), fullRBAC)
			rec := doRequest(router, tc.method, tc.path, rawToken, tc.body)
			if rec.Code == http.StatusForbidden && strings.Contains(rec.Body.String(), "scope_denied") {
				t.Fatalf("%s write-scoped token was scope_denied (status %d); the backstop must let it through; body=%s", tc.name, rec.Code, rec.Body.String())
			}
		})
	}
}
