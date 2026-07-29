package server

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	appmiddleware "github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/alphabravocompany/astronomer-go/internal/tunnel"
)

// projectBindingQuerier is the DB surface SQLCRBACQuerier needs, faked. Using
// the real querier (rather than handing the router pre-expanded bindings) is the
// point of these tests: the thing under test is that a stored `project-member`
// row, which carries neither cluster_id nor namespace, becomes usable
// authorization on a cluster resource route.
type projectBindingQuerier struct {
	userID     uuid.UUID
	projectID  uuid.UUID
	roleName   string
	namespaces []sqlc.ProjectNamespace
	// roleRules overrides the default pods read+list rule set. Raw JSON so it
	// travels the same decodeRoleRules path a real row does.
	roleRules []byte
}

func (q projectBindingQuerier) GetUserByID(_ context.Context, id uuid.UUID) (sqlc.User, error) {
	return sqlc.User{ID: id}, nil
}

func (q projectBindingQuerier) ListUserBindingsWithRoles(_ context.Context, userID pgtype.UUID) ([]sqlc.ListUserBindingsWithRolesRow, error) {
	if !userID.Valid || uuid.UUID(userID.Bytes) != q.userID {
		return nil, nil
	}
	rules := q.roleRules
	if len(rules) == 0 {
		rules = []byte(`[{"resource":"pods","verbs":["read","list"]}]`)
	}
	return []sqlc.ListUserBindingsWithRolesRow{{
		Scope:     "project",
		BindingID: uuid.New(),
		RoleID:    uuid.New(),
		RoleName:  q.roleName,
		ProjectID: pgtype.UUID{Bytes: q.projectID, Valid: true},
		RoleRules: rules,
	}}, nil
}

func (q projectBindingQuerier) ListProjectNamespaces(_ context.Context, projectID uuid.UUID) ([]sqlc.ProjectNamespace, error) {
	if projectID != q.projectID {
		return nil, nil
	}
	return q.namespaces, nil
}

// projectMemberRouter builds a router whose RBAC queries go through the real
// SQLCRBACQuerier, so the project→namespace expansion is exercised end to end.
// NamespaceScopedRBAC mirrors its config default (true, see
// TestNamespaceScopedRBACEnabledDefaultsOn).
func projectMemberRouter(t *testing.T, jwtMgr *auth.JWTManager, q projectBindingQuerier) http.Handler {
	t.Helper()
	return NewRouter(&config.Config{}, RouterDependencies{
		JWT:                 jwtMgr,
		RBACEngine:          rbac.NewEngine(),
		RBACQueries:         appmiddleware.NewSQLCRBACQuerierWithCache(q, nil),
		Proxy:               tunnel.NewProxyHandler(tunnel.NewHub(slog.Default()), slog.Default()),
		NamespaceScopedRBAC: true,
	})
}

// TestK8sProxy_ProjectMemberListsPodsInProjectNamespaces is the parity
// acceptance: a caller whose only grant is `project-member` on a project that
// owns team-a can list pods on that project's cluster. Before project bindings
// expanded unconditionally this was a flat 403 — the binding matched nothing on
// a URL that carries no project_id.
//
// The proxy has no agent connected, so an ADMITTED request lands on the handler
// and returns 503 while a DENIED one is stopped at 403 by the middleware. That
// pair is how every k8s-proxy route test distinguishes the two.
func TestK8sProxy_ProjectMemberListsPodsInProjectNamespaces(t *testing.T) {
	const (
		admitted = http.StatusServiceUnavailable
		denied   = http.StatusForbidden
	)
	jwtMgr := auth.MustNewJWTManager("route-security-test-secret", 60)
	userID := uuid.New()
	projectID := uuid.New()
	clusterID := uuid.New()
	token := nsRBACProxyToken(t, jwtMgr, userID)

	router := projectMemberRouter(t, jwtMgr, projectBindingQuerier{
		userID:    userID,
		projectID: projectID,
		roleName:  "project-member",
		namespaces: []sqlc.ProjectNamespace{
			{ProjectID: projectID, ClusterID: clusterID, Namespace: "team-a"},
		},
	})
	base := "/api/v1/clusters/" + clusterID.String() + "/k8s"

	for _, tc := range []struct {
		name string
		path string
		want int
	}{
		// Cluster-wide list: admitted through the allow-through-and-filter gate,
		// which stashes the {team-a} allow-set for the proxy to filter against.
		{"cluster-wide pod list", base + "/api/v1/pods", admitted},
		// Explicitly inside the project's namespace: plain CheckPermission.
		{"list inside the project namespace", base + "/api/v1/namespaces/team-a/pods", admitted},
		// Watch takes the same gate (F7-b) — this is the path whose event
		// batches the reassembly fix made safe to enable.
		{"cluster-wide pod watch", base + "/api/v1/pods?watch=true", admitted},
		// The role grants read+list only; nothing widens to mutation.
		{"secrets are not in the role", base + "/api/v1/secrets", denied},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			req.Header.Set("Authorization", "Bearer "+token)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

// TestK8sProxy_ProjectMemberDeniedOutsideProjectNamespaces is the other half:
// expansion must confine the grant to exactly the (cluster, namespace) pairs the
// project owns. A namespace belonging to another tenant, and a cluster the
// project was never materialized on, both stay 403.
//
// The 403s alone are non-discriminating — a fixture where expansion never ran
// produces the identical result, because the caller has no cluster-wide grant
// either way. So the test first asserts the POSITIVE case on the SAME router:
// team-a must be admitted. Without that anchor a silently-disabled expansion
// would pass this test instead of failing it.
func TestK8sProxy_ProjectMemberDeniedOutsideProjectNamespaces(t *testing.T) {
	jwtMgr := auth.MustNewJWTManager("route-security-test-secret", 60)
	userID := uuid.New()
	projectID := uuid.New()
	clusterID := uuid.New()
	otherClusterID := uuid.New()
	token := nsRBACProxyToken(t, jwtMgr, userID)

	router := projectMemberRouter(t, jwtMgr, projectBindingQuerier{
		userID:    userID,
		projectID: projectID,
		roleName:  "project-member",
		namespaces: []sqlc.ProjectNamespace{
			{ProjectID: projectID, ClusterID: clusterID, Namespace: "team-a"},
		},
	})
	assertProjectNamespaceAdmitted(t, router, token,
		"/api/v1/clusters/"+clusterID.String()+"/k8s/api/v1/namespaces/team-a/pods")

	for _, tc := range []struct {
		name string
		path string
	}{
		{
			name: "another tenant's namespace on the same cluster",
			path: "/api/v1/clusters/" + clusterID.String() + "/k8s/api/v1/namespaces/team-b/pods",
		},
		{
			name: "a named pod in another tenant's namespace",
			path: "/api/v1/clusters/" + clusterID.String() + "/k8s/api/v1/namespaces/team-b/pods/victim",
		},
		{
			name: "a cluster the project does not own",
			path: "/api/v1/clusters/" + otherClusterID.String() + "/k8s/api/v1/pods",
		},
		{
			name: "the project's own namespace on a cluster it does not own",
			path: "/api/v1/clusters/" + otherClusterID.String() + "/k8s/api/v1/namespaces/team-a/pods",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			req.Header.Set("Authorization", "Bearer "+token)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

// assertProjectNamespaceAdmitted is the anchor the isolation tests use to prove
// their fixture actually expands project bindings. The proxy has no agent, so an
// admitted request surfaces as 503 from the handler; a 403 means the middleware
// stopped it, which for an owned namespace means expansion did not happen.
func assertProjectNamespaceAdmitted(t *testing.T, router http.Handler, token, path string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("anchor %s: status = %d, want 503 (admitted); expansion is not wired, so the 403s below prove nothing. body=%s",
			path, rec.Code, rec.Body.String())
	}
}

// TestK8sProxy_ProjectMemberWithNoNamespacesDeniedEverywhere pins the fail-closed
// case that made the old flag look like a safety property: a project with no
// namespace mappings expands to nothing, so its members reach nothing. Making
// expansion unconditional must not change this.
//
// As above, the denials only mean something if the same fixture DOES admit when
// a namespace exists — so a sibling router with one mapped namespace is built
// and anchored first.
func TestK8sProxy_ProjectMemberWithNoNamespacesDeniedEverywhere(t *testing.T) {
	jwtMgr := auth.MustNewJWTManager("route-security-test-secret", 60)
	userID := uuid.New()
	projectID := uuid.New()
	clusterID := uuid.New()
	token := nsRBACProxyToken(t, jwtMgr, userID)

	assertProjectNamespaceAdmitted(t, projectMemberRouter(t, jwtMgr, projectBindingQuerier{
		userID:    userID,
		projectID: projectID,
		roleName:  "project-owner",
		namespaces: []sqlc.ProjectNamespace{
			{ProjectID: projectID, ClusterID: clusterID, Namespace: "team-a"},
		},
	}), token, "/api/v1/clusters/"+clusterID.String()+"/k8s/api/v1/namespaces/team-a/pods")

	router := projectMemberRouter(t, jwtMgr, projectBindingQuerier{
		userID:    userID,
		projectID: projectID,
		roleName:  "project-owner",
	})

	for _, path := range []string{
		"/api/v1/clusters/" + clusterID.String() + "/k8s/api/v1/pods",
		"/api/v1/clusters/" + clusterID.String() + "/k8s/api/v1/namespaces/team-a/pods",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s: status = %d, want 403; body=%s", path, rec.Code, rec.Body.String())
		}
	}
}
