package server

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/tunnel"
)

// namespaceScopedListBindings returns a single namespace-scoped binding granting
// (resource, list) inside `namespace` on `clusterID` — the exact shape a
// project/namespace-scoped user carries. It fails a cluster-wide CheckPermission
// (namespace=="") but is eligible for the allow-through-and-filter gate.
func namespaceScopedListBindings(clusterID uuid.UUID, namespace string, resource rbac.Resource) []rbac.RoleBinding {
	return []rbac.RoleBinding{{
		ClusterID: clusterID.String(),
		Namespace: namespace,
		RoleRules: []rbac.Rule{{Resource: string(resource), Verbs: []string{string(rbac.VerbList), string(rbac.VerbRead)}}},
	}}
}

// clusterWideListBindings grants (resource, list) cluster-wide (no namespace) —
// a normal reader that passes the coarse CheckPermission and is NEVER filtered.
func clusterWideListBindings(clusterID uuid.UUID, resource rbac.Resource) []rbac.RoleBinding {
	return []rbac.RoleBinding{{
		ClusterID: clusterID.String(),
		RoleRules: []rbac.Rule{{Resource: string(resource), Verbs: []string{string(rbac.VerbList), string(rbac.VerbRead)}}},
	}}
}

func nsRBACProxyToken(t *testing.T, jwtMgr *auth.JWTManager, userID uuid.UUID) string {
	t.Helper()
	token, err := jwtMgr.GenerateAccessToken(userID)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	return token
}

// TestK8sProxyNamespaceScopedListGate exercises Part A: the allow-through gate in
// requireK8sProxyPermission. An admitted request reaches the proxy handler which,
// with no agent connected, returns 503 (ServiceUnavailable). A denied request is
// stopped at 403 (Forbidden). We distinguish admission from denial by that pair.
func TestK8sProxyNamespaceScopedListGate(t *testing.T) {
	jwtMgr := auth.NewJWTManager("route-security-test-secret", 60)
	userID := uuid.New()
	token := nsRBACProxyToken(t, jwtMgr, userID)
	clusterID := uuid.New()
	base := "/api/v1/clusters/" + clusterID.String() + "/k8s"

	const (
		admitted = http.StatusServiceUnavailable
		denied   = http.StatusForbidden
	)

	tests := []struct {
		name     string
		flagOn   bool
		method   string
		path     string
		bindings []rbac.RoleBinding
		want     int
	}{
		{
			name:     "scoped cluster-wide list admitted when flag on",
			flagOn:   true,
			method:   http.MethodGet,
			path:     base + "/api/v1/pods",
			bindings: namespaceScopedListBindings(clusterID, "team-a", rbac.ResourcePods),
			want:     admitted,
		},
		{
			name:     "scoped cluster-wide list denied when flag off",
			flagOn:   false,
			method:   http.MethodGet,
			path:     base + "/api/v1/pods",
			bindings: namespaceScopedListBindings(clusterID, "team-a", rbac.ResourcePods),
			want:     denied,
		},
		{
			// F7-b: cluster-wide watches are admitted when the user holds list
			// in ≥1 namespace; the tunnel filters frames to that allow-set.
			name:     "scoped cluster-wide watch admitted when flag on (F7-b)",
			flagOn:   true,
			method:   http.MethodGet,
			path:     base + "/api/v1/pods?watch=true",
			bindings: namespaceScopedListBindings(clusterID, "team-a", rbac.ResourcePods),
			want:     admitted,
		},
		{
			name:     "scoped cluster-wide /watch/ path admitted when flag on (F7-b)",
			flagOn:   true,
			method:   http.MethodGet,
			path:     base + "/api/v1/watch/pods",
			bindings: namespaceScopedListBindings(clusterID, "team-a", rbac.ResourcePods),
			want:     admitted,
		},
		{
			// Bypass guard: the apiserver parses ?watch with strconv.ParseBool,
			// so ?watch=TRUE is a watch and must use the F7-b gate (not be
			// misclassified as a denied unary list).
			name:     "scoped cluster-wide watch=TRUE (uppercase) admitted when flag on (F7-b)",
			flagOn:   true,
			method:   http.MethodGet,
			path:     base + "/api/v1/pods?watch=TRUE",
			bindings: namespaceScopedListBindings(clusterID, "team-a", rbac.ResourcePods),
			want:     admitted,
		},
		{
			name:     "scoped cluster-wide watch=t (short) admitted when flag on (F7-b)",
			flagOn:   true,
			method:   http.MethodGet,
			path:     base + "/api/v1/pods?watch=t",
			bindings: namespaceScopedListBindings(clusterID, "team-a", rbac.ResourcePods),
			want:     admitted,
		},
		{
			name:     "scoped cluster-wide mutation still denied (flag on)",
			flagOn:   true,
			method:   http.MethodPost,
			path:     base + "/api/v1/pods",
			bindings: namespaceScopedListBindings(clusterID, "team-a", rbac.ResourcePods),
			want:     denied,
		},
		{
			name:     "scoped named GET (not a list) still denied (flag on)",
			flagOn:   true,
			method:   http.MethodGet,
			path:     base + "/api/v1/namespaces/team-b/pods/x",
			bindings: namespaceScopedListBindings(clusterID, "team-a", rbac.ResourcePods),
			want:     denied,
		},
		{
			name:     "scoped user without matching resource still denied (flag on)",
			flagOn:   true,
			method:   http.MethodGet,
			path:     base + "/api/v1/secrets",
			bindings: namespaceScopedListBindings(clusterID, "team-a", rbac.ResourcePods),
			want:     denied,
		},
		{
			name:     "namespaced list within allowed namespace admitted (flag on)",
			flagOn:   true,
			method:   http.MethodGet,
			path:     base + "/api/v1/namespaces/team-a/pods",
			bindings: namespaceScopedListBindings(clusterID, "team-a", rbac.ResourcePods),
			want:     admitted,
		},
		{
			name:     "cluster-wide reader admitted unfiltered (flag on)",
			flagOn:   true,
			method:   http.MethodGet,
			path:     base + "/api/v1/pods",
			bindings: clusterWideListBindings(clusterID, rbac.ResourcePods),
			want:     admitted,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := NewRouter(&config.Config{}, RouterDependencies{
				JWT:                 jwtMgr,
				RBACEngine:          rbac.NewEngine(),
				RBACQueries:         routeSecurityRBACQuerier{bindings: tt.bindings},
				Proxy:               tunnel.NewProxyHandler(tunnel.NewHub(slog.Default()), slog.Default()),
				NamespaceScopedRBAC: tt.flagOn,
			})
			req := httptest.NewRequest(tt.method, tt.path, nil)
			req.Header.Set("Authorization", "Bearer "+token)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tt.want, rec.Body.String())
			}
		})
	}
}
