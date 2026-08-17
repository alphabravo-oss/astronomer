package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	deliveryhandler "github.com/alphabravocompany/astronomer-go/internal/handler/delivery"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

func TestDeliveryControlRoutesRequireAuthentication(t *testing.T) {
	projectID := uuid.New()
	resourceID := uuid.New()
	router := NewRouter(&config.Config{}, RouterDependencies{
		JWT:                 auth.MustNewJWTManager("delivery-control-route-test-secret", 60),
		DeliveryTargets:     deliveryhandler.NewTargetHandler(nil, nil, nil),
		DeliveryRollouts:    deliveryhandler.NewRolloutHandler(nil, nil, nil, nil),
		DeliveryDeployments: deliveryhandler.NewDeploymentHandler(nil, nil, nil),
		DeliveryInventory:   deliveryhandler.NewInventoryHandler(nil),
	})
	paths := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/delivery/targets/?project_id=" + projectID.String()},
		{http.MethodPost, "/api/v1/delivery/targets/" + resourceID.String() + "/preview/?project_id=" + projectID.String()},
		{http.MethodPost, "/api/v1/delivery/targets/" + resourceID.String() + "/rollouts/?project_id=" + projectID.String()},
		{http.MethodGet, "/api/v1/delivery/rollouts/" + resourceID.String() + "/?project_id=" + projectID.String()},
		{http.MethodPost, "/api/v1/delivery/rollouts/" + resourceID.String() + "/approve/?project_id=" + projectID.String()},
		{http.MethodPost, "/api/v1/delivery/rollouts/" + resourceID.String() + "/rollback/?project_id=" + projectID.String()},
		{http.MethodGet, "/api/v1/delivery/deployments/" + resourceID.String() + "/events/?project_id=" + projectID.String()},
		{http.MethodPost, "/api/v1/delivery/deployments/" + resourceID.String() + "/suspend/?project_id=" + projectID.String()},
		{http.MethodPost, "/api/v1/delivery/deployments/" + resourceID.String() + "/resume/?project_id=" + projectID.String()},
		{http.MethodGet, "/api/v1/delivery/clusters/" + resourceID.String() + "/inventory/?project_id=" + projectID.String()},
		{http.MethodGet, "/api/v1/delivery/fleet/"},
		{http.MethodGet, "/api/v1/delivery/system/compatibility/"},
	}
	for _, test := range paths {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(test.method, test.path, nil))
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s status=%d body=%s", test.method, test.path, recorder.Code, recorder.Body.String())
		}
	}
}

func TestDeliveryControlRoutesSeparateOrdinaryApprovalRollbackAndPlatformAuthority(t *testing.T) {
	projectID := uuid.New()
	resourceID := uuid.New()
	jwt := auth.MustNewJWTManager("delivery-control-rbac-test-secret", 60)
	token, err := jwt.GenerateAccessToken(uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	bindings := []rbac.RoleBinding{{
		Scope: "project", ProjectID: projectID.String(), RoleRules: []rbac.Rule{
			{Resource: string(rbac.ResourceDeliveryTargets), Verbs: []string{string(rbac.VerbRead)}},
			{Resource: string(rbac.ResourceDeliveryRollouts), Verbs: []string{string(rbac.VerbUpdate)}},
		},
	}}
	router := NewRouter(&config.Config{}, RouterDependencies{
		JWT: jwt, RBACEngine: rbac.NewEngine(), RBACQueries: deliveryRouteRBACQuerier{bindings: bindings},
		DeliveryTargets:     deliveryhandler.NewTargetHandler(nil, nil, nil),
		DeliveryRollouts:    deliveryhandler.NewRolloutHandler(nil, nil, nil, nil),
		DeliveryDeployments: deliveryhandler.NewDeploymentHandler(nil, nil, nil),
		DeliveryInventory:   deliveryhandler.NewInventoryHandler(nil),
	})

	request := func(method, path string) int {
		req := httptest.NewRequest(method, path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, req)
		return recorder.Code
	}
	preview := "/api/v1/delivery/targets/" + resourceID.String() + "/preview/?project_id=" + projectID.String()
	if status := request(http.MethodPost, preview); status != http.StatusServiceUnavailable {
		t.Fatalf("target read permission did not reach preview handler: %d", status)
	}
	for _, path := range []string{
		"/api/v1/delivery/rollouts/" + resourceID.String() + "/approve/?project_id=" + projectID.String(),
		"/api/v1/delivery/rollouts/" + resourceID.String() + "/rollback/?project_id=" + projectID.String(),
		"/api/v1/delivery/system/compatibility/",
	} {
		if status := request(http.MethodPost, path); status != http.StatusForbidden {
			// The platform path is GET; retry it with the documented method.
			if path == "/api/v1/delivery/system/compatibility/" && request(http.MethodGet, path) == http.StatusForbidden {
				continue
			}
			t.Fatalf("ordinary rollout authority crossed a dedicated boundary for %s: %d", path, status)
		}
	}
}
