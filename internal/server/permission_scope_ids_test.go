package server

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func TestPermissionScopeIDsIgnoresGenericObjectID(t *testing.T) {
	objectID := uuid.New()
	request := httptest.NewRequest("GET", "/api/v1/admin/alerting/inhibitions/"+objectID.String()+"/", nil)
	route := chi.NewRouteContext()
	route.URLParams.Add("id", objectID.String())
	request = request.WithContext(context.WithValue(request.Context(), chi.RouteCtxKey, route))

	clusterID, projectID := permissionScopeIDs(request)
	if clusterID != uuid.Nil || projectID != uuid.Nil {
		t.Fatalf("generic object id became authorization scope: cluster=%s project=%s", clusterID, projectID)
	}
}

func TestPermissionScopeIDsUsesExplicitScopeParameters(t *testing.T) {
	wantCluster, wantProject := uuid.New(), uuid.New()
	request := httptest.NewRequest("GET", "/", nil)
	route := chi.NewRouteContext()
	route.URLParams.Add("cluster_id", wantCluster.String())
	route.URLParams.Add("project_id", wantProject.String())
	request = request.WithContext(context.WithValue(request.Context(), chi.RouteCtxKey, route))

	clusterID, projectID := permissionScopeIDs(request)
	if clusterID != wantCluster || projectID != wantProject {
		t.Fatalf("scope=(%s,%s), want=(%s,%s)", clusterID, projectID, wantCluster, wantProject)
	}
}
