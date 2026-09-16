package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
)

func TestRemovedPublicAPIAliasesAreNotMounted(t *testing.T) {
	router, _ := newRouteSecurityRouter(t)
	clusterID := uuid.NewString()
	resourceID := uuid.NewString()
	accessToken, err := auth.MustNewJWTManager("route-security-test-secret", 60).GenerateAccessToken(uuid.New())
	if err != nil {
		t.Fatalf("generate access token: %v", err)
	}

	for _, route := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v1/alerts/rules/" + resourceID + "/enable/"},
		{http.MethodPost, "/api/v1/alerts/rules/" + resourceID + "/disable/"},
		{http.MethodPost, "/api/v1/alerts/silences/" + resourceID + "/expire/"},
		{http.MethodGet, "/api/v1/delivery/fleet/"},
		{http.MethodGet, "/api/v1/activity/"},
		{http.MethodPost, "/api/v1/clusters/" + clusterID + "/generate_kubeconfig/"},
		{http.MethodGet, "/api/v1/clusters/" + clusterID + "/kubeconfig/"},
		{http.MethodGet, "/api/v1/backups/runs/"},
		{http.MethodPost, "/api/v1/backups/storage/" + resourceID + "/test/"},
		{http.MethodGet, "/api/v1/backups/storage-configs/"},
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(route.method, route.path, nil)
		request.Header.Set("Authorization", "Bearer "+accessToken)
		router.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("removed alias %s %s status=%d, want 404; body=%s", route.method, route.path, recorder.Code, recorder.Body.String())
		}
	}
}

func TestCanonicalPublicRoutesRemainProtected(t *testing.T) {
	router, _ := newRouteSecurityRouter(t)
	clusterID := uuid.NewString()
	resourceID := uuid.NewString()

	for _, route := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v1/alerting/rules/" + resourceID + "/enable/"},
		{http.MethodGet, "/api/v1/delivery/estate/"},
		{http.MethodPost, "/api/v1/clusters/" + clusterID + "/generate-kubeconfig/"},
		{http.MethodGet, "/api/v1/clusters/" + clusterID + "/kubeconfig-preview/"},
		{http.MethodGet, "/api/v1/backups/"},
		{http.MethodPost, "/api/v1/backups/storage/" + resourceID + "/test-connection/"},
	} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(route.method, route.path, nil))
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("canonical route %s %s status=%d, want 401 auth gate; body=%s", route.method, route.path, recorder.Code, recorder.Body.String())
		}
	}
}
