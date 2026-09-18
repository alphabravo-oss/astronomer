package reqctx

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func TestRouteScopes(t *testing.T) {
	clusterID, projectID, objectID := uuid.New(), uuid.New(), uuid.New()
	for _, tc := range []struct {
		name                     string
		params                   map[string]string
		wantCluster, wantProject uuid.UUID
	}{
		{"explicit scopes", map[string]string{"cluster_id": clusterID.String(), "project_id": projectID.String(), "id": objectID.String()}, clusterID, projectID},
		{"generic id is not scope", map[string]string{"id": objectID.String()}, uuid.Nil, uuid.Nil},
		{"malformed scope does not fall back", map[string]string{"cluster_id": "bad", "project_id": "bad", "id": objectID.String()}, uuid.Nil, uuid.Nil},
		{"nil scope is invalid", map[string]string{"cluster_id": uuid.Nil.String(), "project_id": uuid.Nil.String()}, uuid.Nil, uuid.Nil},
		{"query is not scope", nil, uuid.Nil, uuid.Nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/?cluster_id="+clusterID.String()+"&project_id="+projectID.String(), nil)
			route := chi.NewRouteContext()
			for key, value := range tc.params {
				route.URLParams.Add(key, value)
			}
			r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, route))
			cluster, clusterErr := ClusterID(r)
			project, projectErr := ProjectID(r)
			if cluster != tc.wantCluster || (clusterErr != nil) != (tc.wantCluster == uuid.Nil) {
				t.Fatalf("cluster = %s, %v", cluster, clusterErr)
			}
			if project != tc.wantProject || (projectErr != nil) != (tc.wantProject == uuid.Nil) {
				t.Fatalf("project = %s, %v", project, projectErr)
			}
		})
	}
	if _, err := ClusterID(nil); err == nil {
		t.Fatal("nil request accepted")
	}
}
