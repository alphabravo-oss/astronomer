package server

import (
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	appmiddleware "github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

// registerClusterRoutes declares, once for its whole subtree, that the chi param
// {id} names a CLUSTER (appmiddleware.ClusterScopeFromIDParam). That is a
// per-ROUTE fact asserted with a SUBTREE-wide, unconditional context flag: the
// next route added under r.Route("/clusters") whose {id} names something else —
// a template, a group, a nested child object — inherits a cluster scope, and
// every permission gate on it is then evaluated at the wrong scope with nothing
// failing. These tests are that fence.
//
// They deliberately do not read routes_clusters.go's registrations. The set of
// routes the declaration actually REACHES is not what that file appears to
// register:
//
//   - /clusters/{id}/gatekeeper/constraints/ (routes_gatekeeper.go) and the
//     /clusters/{cluster_id}/... families from six other files are registered on
//     the PARENT router and never enter the subtree, so they carry no
//     declaration;
//   - /clusters/{id}/metrics/ is registered inside the subtree but shadowed at
//     match time by the {cluster_id} registration routes_monitoring.go puts on
//     the parent.
//
// So the subtree is identified the only way that cannot drift: by walking the
// built router and testing each route's real middleware chain for the
// declaration itself.

// clusterScopeDeclaredRoutes walks the fully-wired router and returns the
// patterns whose middleware chain actually contains ClusterScopeFromIDParam.
// Comparison is by function pointer — r.Use stores the function value itself,
// so this matches the declaration and nothing else.
func clusterScopeDeclaredRoutes(t *testing.T, router chi.Routes) []string {
	t.Helper()
	want := reflect.ValueOf(appmiddleware.ClusterScopeFromIDParam).Pointer()
	seen := map[string]struct{}{}
	if err := chi.Walk(router, func(_ string, route string, _ http.Handler, middlewares ...func(http.Handler) http.Handler) error {
		for _, mw := range middlewares {
			if reflect.ValueOf(mw).Pointer() == want {
				seen[normalizeRoutePattern(route)] = struct{}{}
				return nil
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("walk router: %v", err)
	}
	patterns := make([]string, 0, len(seen))
	for pattern := range seen {
		patterns = append(patterns, pattern)
	}
	sort.Strings(patterns)
	return patterns
}

// TestClusterScopeDeclarationOnlyCoversRoutesWhoseIDIsTheCluster is the
// invariant: everywhere the declaration is in force, a bare {id} must be the
// segment immediately after /clusters, because that is the only segment the
// declaration's claim is true of.
//
// It fails on exactly the mistake the subtree-wide flag makes easy — adding
// /clusters/{cluster_id}/templates/{id}/ or /clusters/policies/{id}/ inside
// registerClusterRoutes, where {id} is not a cluster and the flag says it is.
func TestClusterScopeDeclarationOnlyCoversRoutesWhoseIDIsTheCluster(t *testing.T) {
	router, _ := newRouteSecurityRouter(t)
	patterns := clusterScopeDeclaredRoutes(t, router)

	if len(patterns) == 0 {
		t.Fatal("no route in the built router carries ClusterScopeFromIDParam — either the declaration was dropped from registerClusterRoutes or this test no longer finds it, and both silently return the per-cluster monitoring gates to a GLOBAL check")
	}

	for _, pattern := range patterns {
		segments := strings.Split(strings.Trim(pattern, "/"), "/")
		clusterAt := -1
		for i, segment := range segments {
			if segment == "clusters" {
				clusterAt = i
				break
			}
		}
		if clusterAt < 0 {
			t.Errorf("%s: carries the cluster-scope declaration but is not under /clusters at all", pattern)
			continue
		}
		for i, segment := range segments {
			if segment != "{id}" {
				continue
			}
			if i != clusterAt+1 {
				t.Errorf("%s: {id} is at segment %d, but the declaration claims every {id} in this subtree is the CLUSTER id at segment %d. Either name this param something else or move the route out of registerClusterRoutes.",
					pattern, i, clusterAt+1)
			}
		}
	}
}

// TestClusterScopeDeclarationCoversTheMonitoringGatedRoutes is the converse
// fence. The invariant above stays green if the declaration stops reaching the
// routes that need it — e.g. someone moves the monitoring block out to a file
// that registers on the parent router, which is where six other
// /clusters/... families already live. These are the routes under
// /clusters/{id} that gate on a resource OTHER than clusters/projects, so they
// are precisely the ones whose scope comes from the declaration and nowhere
// else.
func TestClusterScopeDeclarationCoversTheMonitoringGatedRoutes(t *testing.T) {
	router, _ := newRouteSecurityRouter(t)
	covered := map[string]struct{}{}
	for _, pattern := range clusterScopeDeclaredRoutes(t, router) {
		covered[pattern] = struct{}{}
	}

	// Every rbac.ResourceMonitoring gate under /clusters/{id} in
	// routes_clusters.go, in the trailing-slash-normalized form
	// normalizeRoutePattern produces. /{id}/metrics/ and /{id}/metrics/summary/
	// are registered here too but omitted: routes_monitoring.go shadows both
	// with a {cluster_id} registration on the parent router, which resolves its
	// scope without the declaration.
	required := []string{
		"/api/v1/clusters/{id}/health",
		"/api/v1/clusters/{id}/monitoring/config",
		"/api/v1/clusters/{id}/monitoring/stack/status",
		"/api/v1/clusters/{id}/monitoring/stack/preview",
		"/api/v1/clusters/{id}/monitoring/stack/install",
		"/api/v1/clusters/{id}/monitoring/stack/upgrade",
		"/api/v1/clusters/{id}/monitoring/stack/replace",
		"/api/v1/clusters/{id}/monitoring/stack/uninstall",
	}
	for _, pattern := range required {
		if _, ok := covered[pattern]; !ok {
			t.Errorf("%s gates on rbac.ResourceMonitoring at a bare {id} but does NOT carry ClusterScopeFromIDParam, so its permission check resolves to uuid.Nil and runs as a GLOBAL monitoring check", pattern)
		}
	}
}
