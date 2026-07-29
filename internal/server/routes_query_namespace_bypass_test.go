package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	appmiddleware "github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/alphabravocompany/astronomer-go/internal/tunnel"
)

// Every project member now carries namespace-narrowed CLUSTER bindings
// (expandProjectBindings), and a namespace-narrowed binding only matches a
// request whose namespace equals the binding's. That makes any namespace the
// gate accepts from the URL QUERY a forgeable key: the gate authorizes
// ?namespace=<mine> while the handler acts somewhere else entirely.
//
// These tests pin the two halves of the fix:
//   - requirePermission ignores ?namespace= outright (namespaceContext), so
//     routes whose handler does not scope by it fail closed;
//   - the typed-resource CREATE route is authorized against the namespace in
//     the request BODY, which is the one CreateNamedResource actually writes to.

// resourceCreateRouter wires the Resources handler (no requester, so an admitted
// request surfaces as 503) behind the real project-binding querier.
func resourceCreateRouter(t *testing.T, jwtMgr *auth.JWTManager, q projectBindingQuerier) http.Handler {
	t.Helper()
	return NewRouter(&config.Config{}, RouterDependencies{
		JWT:                 jwtMgr,
		RBACEngine:          rbac.NewEngine(),
		RBACQueries:         appmiddleware.NewSQLCRBACQuerierWithCache(q, nil),
		Resources:           handler.NewResourceHandler(),
		Proxy:               tunnel.NewProxyHandler(tunnel.NewHub(nil), nil),
		NamespaceScopedRBAC: true,
	})
}

func TestResourceCreate_AuthorizesBodyNamespaceNotQuery(t *testing.T) {
	const (
		admitted = http.StatusServiceUnavailable
		denied   = http.StatusForbidden
	)
	jwtMgr := auth.MustNewJWTManager("route-security-test-secret", 60)
	userID := uuid.New()
	projectID := uuid.New()
	clusterID := uuid.New()
	token := nsRBACProxyToken(t, jwtMgr, userID)

	// service-ingress-manager shape: full services CRUD, project-scoped, and the
	// project owns exactly team-a.
	router := resourceCreateRouter(t, jwtMgr, projectBindingQuerier{
		userID:    userID,
		projectID: projectID,
		roleName:  "service-ingress-manager",
		roleRules: []byte(`[{"resource":"services","verbs":["create","read","list"]}]`),
		namespaces: []sqlc.ProjectNamespace{
			{ProjectID: projectID, ClusterID: clusterID, Namespace: "team-a"},
		},
	})
	path := "/api/v1/clusters/" + clusterID.String() + "/resources/services/"

	for _, tc := range []struct {
		name  string
		query string
		body  string
		want  int
	}{
		// The caller's own namespace, named honestly in the body: allowed. This
		// is the anchor — without it the denials below could just mean the
		// fixture never granted anything.
		{"body names the owned namespace", "", `{"metadata":{"name":"api","namespace":"team-a"}}`, admitted},
		// The escalation: gate satisfied by the query, object written elsewhere.
		{"query owned, body foreign", "?namespace=team-a", `{"metadata":{"name":"api","namespace":"kube-system"}}`, denied},
		// No query at all — the shape the UI actually sends.
		{"body names a foreign namespace", "", `{"metadata":{"name":"api","namespace":"team-b"}}`, denied},
		// A body with no namespace would proxy to an implicit/cluster-wide path;
		// no namespace-narrowed grant covers that.
		{"body names no namespace", "?namespace=team-a", `{"metadata":{"name":"api"}}`, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, path+tc.query, strings.NewReader(tc.body))
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

// TestResourceList_QueryNamespaceStillNarrows is the counterpart: the LIST route
// does build its upstream path from ?namespace=, so it keeps the query-scoped
// gate — naming the owned namespace is admitted, naming a foreign one is not,
// and a bare cluster-wide list stays denied for a narrowed caller.
func TestResourceList_QueryNamespaceStillNarrows(t *testing.T) {
	const (
		admitted = http.StatusServiceUnavailable
		denied   = http.StatusForbidden
	)
	jwtMgr := auth.MustNewJWTManager("route-security-test-secret", 60)
	userID := uuid.New()
	projectID := uuid.New()
	clusterID := uuid.New()
	token := nsRBACProxyToken(t, jwtMgr, userID)

	router := resourceCreateRouter(t, jwtMgr, projectBindingQuerier{
		userID:    userID,
		projectID: projectID,
		roleName:  "service-ingress-manager",
		roleRules: []byte(`[{"resource":"services","verbs":["create","read","list"]}]`),
		namespaces: []sqlc.ProjectNamespace{
			{ProjectID: projectID, ClusterID: clusterID, Namespace: "team-a"},
		},
	})
	path := "/api/v1/clusters/" + clusterID.String() + "/resources/services/"

	for _, tc := range []struct {
		name  string
		query string
		want  int
	}{
		{"owned namespace", "?namespace=team-a", admitted},
		{"foreign namespace", "?namespace=team-b", denied},
		{"cluster-wide", "", denied},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path+tc.query, nil)
			req.Header.Set("Authorization", "Bearer "+token)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}
