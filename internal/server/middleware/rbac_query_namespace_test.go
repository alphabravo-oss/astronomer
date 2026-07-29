package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

type narrowedBindingsQuerier struct {
	bindings []rbac.RoleBinding
}

func (q narrowedBindingsQuerier) GetUserBindings(context.Context, string) ([]rbac.RoleBinding, error) {
	return q.bindings, nil
}

// TestRequirePermission_IgnoresQueryNamespace pins the fix for the
// attacker-controlled gate namespace.
//
// A namespace-narrowed CLUSTER binding — the shape expandProjectBindings gives
// every project member — matches only a request carrying that same namespace.
// While the gate read ?namespace=, the caller chose the value it was checked
// against, so any route whose handler derives its real target elsewhere (a
// request body, or nothing at all) was reachable for the whole cluster. The
// route param is not forgeable this way; the query is.
func TestRequirePermission_IgnoresQueryNamespace(t *testing.T) {
	clusterID := uuid.New()
	engine := rbac.NewEngine()
	querier := narrowedBindingsQuerier{bindings: []rbac.RoleBinding{{
		UserID:    "u",
		Scope:     "cluster",
		ClusterID: clusterID.String(),
		Namespace: "team-a",
		RoleRules: []rbac.Rule{{Resource: "services", Verbs: []string{"create", "list"}}},
	}}}

	newRouter := func(mw func(http.Handler) http.Handler, pattern string) http.Handler {
		r := chi.NewRouter()
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				next.ServeHTTP(w, req.WithContext(
					SetAuthenticatedUserForTest(req.Context(), &AuthenticatedUser{ID: "u"})))
			})
		})
		r.With(mw).Get(pattern, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})
		return r
	}

	plain := newRouter(
		RequirePermission(engine, querier, rbac.ResourceServices, rbac.VerbList),
		"/clusters/{cluster_id}/things/")
	queryScoped := newRouter(
		RequireQueryNamespacePermission(engine, querier, rbac.ResourceServices, rbac.VerbList),
		"/clusters/{cluster_id}/things/")
	routeScoped := newRouter(
		RequirePermission(engine, querier, rbac.ResourceServices, rbac.VerbList),
		"/clusters/{cluster_id}/things/{namespace}/")

	base := "/clusters/" + clusterID.String() + "/things/"
	for _, tc := range []struct {
		name   string
		router http.Handler
		path   string
		want   int
	}{
		// The bypass: the caller's own namespace in the query no longer
		// satisfies a plain gate, because the handler behind it need not use it.
		{"plain gate + owned namespace in query", plain, base + "?namespace=team-a", http.StatusForbidden},
		{"plain gate + foreign namespace in query", plain, base + "?namespace=team-b", http.StatusForbidden},
		{"plain gate + no namespace", plain, base, http.StatusForbidden},
		// The opt-in gate for handlers that DO narrow to ?namespace=.
		{"query gate + owned namespace", queryScoped, base + "?namespace=team-a", http.StatusNoContent},
		{"query gate + foreign namespace", queryScoped, base + "?namespace=team-b", http.StatusForbidden},
		{"query gate + no namespace", queryScoped, base, http.StatusForbidden},
		// A route param is the path the handler itself acts on; unchanged.
		{"route param + owned namespace", routeScoped, base + "team-a/", http.StatusNoContent},
		{"route param + foreign namespace", routeScoped, base + "team-b/", http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tc.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

// A cluster-wide grant is unaffected by any of this: it matches at namespace ""
// and therefore passes every gate above.
func TestRequirePermission_ClusterWideGrantUnaffectedByQueryNamespace(t *testing.T) {
	clusterID := uuid.New()
	engine := rbac.NewEngine()
	querier := narrowedBindingsQuerier{bindings: []rbac.RoleBinding{{
		UserID:    "u",
		Scope:     "cluster",
		ClusterID: clusterID.String(),
		RoleRules: []rbac.Rule{{Resource: "services", Verbs: []string{"list"}}},
	}}}

	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(
				SetAuthenticatedUserForTest(req.Context(), &AuthenticatedUser{ID: "u"})))
		})
	})
	r.With(RequirePermission(engine, querier, rbac.ResourceServices, rbac.VerbList)).
		Get("/clusters/{cluster_id}/things/", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})

	for _, path := range []string{
		"/clusters/" + clusterID.String() + "/things/",
		"/clusters/" + clusterID.String() + "/things/?namespace=anything",
	} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNoContent {
			t.Fatalf("%s: status = %d, want 204", path, rec.Code)
		}
	}
}
