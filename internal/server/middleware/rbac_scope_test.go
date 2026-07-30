package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

// scopeRequest builds a request carrying the given chi URL params, optionally
// wrapped in the ClusterScopeFromIDParam declaration that registerClusterRoutes
// stamps on the whole /clusters subtree.
func scopeRequest(declared bool, params map[string]string) *http.Request {
	req := setupChiRequest(httptest.NewRequest(http.MethodGet, "/", nil), params)
	if !declared {
		return req
	}
	var seen *http.Request
	ClusterScopeFromIDParam(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = r
	})).ServeHTTP(httptest.NewRecorder(), req)
	return seen
}

// TestPermissionScopeResolvesBareIDParam pins the rule permissionScope
// implements — one case per branch, with no router in the way.
//
// The third case is the defect this file's ClusterScopeFromIDParam was added
// for: an undeclared bare {id} on a gate that checks something other than
// clusters/projects resolves to uuid.Nil, i.e. a GLOBAL check. That is right
// for /monitoring/endpoints/{id} and /audit/{id}, and was wrong for the
// /clusters/{id} routes that gate on rbac.ResourceMonitoring — which is what
// case two now covers.
func TestPermissionScopeResolvesBareIDParam(t *testing.T) {
	clusterID := uuid.New()
	projectID := uuid.New()
	otherID := uuid.New()

	for _, tc := range []struct {
		name        string
		declared    bool
		params      map[string]string
		resource    rbac.Resource
		wantCluster uuid.UUID
		wantProject uuid.UUID
	}{
		{
			name:        "explicit cluster_id wins over a bare id",
			params:      map[string]string{"cluster_id": clusterID.String(), "id": otherID.String()},
			resource:    rbac.ResourceMonitoring,
			wantCluster: clusterID,
		},
		{
			name:        "declared subtree binds a bare id as the cluster",
			declared:    true,
			params:      map[string]string{"id": clusterID.String()},
			resource:    rbac.ResourceMonitoring,
			wantCluster: clusterID,
		},
		{
			name:     "undeclared bare id stays global for a non-cluster resource",
			params:   map[string]string{"id": otherID.String()},
			resource: rbac.ResourceMonitoring,
		},
		{
			name:        "clusters resource keeps its own inference when undeclared",
			params:      map[string]string{"id": clusterID.String()},
			resource:    rbac.ResourceClusters,
			wantCluster: clusterID,
		},
		{
			name:        "projects resource still binds a bare id as the project",
			params:      map[string]string{"id": projectID.String()},
			resource:    rbac.ResourceProjects,
			wantProject: projectID,
		},
		{
			// The declaration speaks only for the cluster scope, so it must not
			// also start populating a project scope — that would let a project
			// binding satisfy a cluster route.
			name:        "declaration never populates the project scope",
			declared:    true,
			params:      map[string]string{"id": projectID.String()},
			resource:    rbac.ResourceMonitoring,
			wantCluster: projectID,
		},
		{
			name:     "a non-uuid id resolves to nothing rather than guessing",
			declared: true,
			params:   map[string]string{"id": "not-a-uuid"},
			resource: rbac.ResourceMonitoring,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gotCluster, gotProject := permissionScope(scopeRequest(tc.declared, tc.params), tc.resource)
			if gotCluster != tc.wantCluster {
				t.Fatalf("cluster scope = %v, want %v", gotCluster, tc.wantCluster)
			}
			if gotProject != tc.wantProject {
				t.Fatalf("project scope = %v, want %v", gotProject, tc.wantProject)
			}
		})
	}
}
