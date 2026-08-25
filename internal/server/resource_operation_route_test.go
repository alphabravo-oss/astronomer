package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

type routeResourceOperationStore struct{ operation sqlc.ResourceOperation }

func (s routeResourceOperationStore) GetResourceOperation(context.Context, uuid.UUID) (sqlc.ResourceOperation, error) {
	return s.operation, nil
}

func TestResourceOperationReceiptRechecksClusterAccessAndRejectsCrossClusterPath(t *testing.T) {
	jwt := auth.MustNewJWTManager("resource-operation-route-test", 60)
	token, err := jwt.GenerateAccessToken(uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	clusterID, operationID := uuid.New(), uuid.New()
	now := time.Now()
	resourceHandler := handler.NewResourceHandler()
	resourceHandler.SetResourceOperationStore(routeResourceOperationStore{operation: sqlc.ResourceOperation{
		ID: operationID, ClusterID: clusterID, ResourceType: "services", ResourceName: "api",
		Action: "apply", RequiredVerb: "create", Status: "pending", Generation: 1, CreatedAt: now, UpdatedAt: now,
	}})

	request := func(bindings []rbac.RoleBinding, pathCluster uuid.UUID) *httptest.ResponseRecorder {
		resourceHandler.SetAuthorization(rbac.NewEngine(), routeSecurityRBACQuerier{bindings: bindings})
		router := NewRouter(&config.Config{}, RouterDependencies{
			JWT: jwt, RBACEngine: rbac.NewEngine(), RBACQueries: routeSecurityRBACQuerier{bindings: bindings},
			Resources: resourceHandler,
		})
		req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/"+pathCluster.String()+"/resources/operations/"+operationID.String()+"/", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, req)
		return recorder
	}

	if got := request(routeSecurityBindings(rbac.ResourceClusters, rbac.VerbRead), clusterID); got.Code != http.StatusOK {
		t.Fatalf("authorized receipt status=%d body=%s", got.Code, got.Body.String())
	}
	if got := request(routeSecurityBindings(rbac.ResourceServices, rbac.VerbCreate), clusterID); got.Code != http.StatusOK {
		t.Fatalf("originating permission receipt status=%d body=%s", got.Code, got.Body.String())
	}
	if got := request(routeSecurityBindings(rbac.ResourceServices, rbac.VerbUpdate), clusterID); got.Code != http.StatusNotFound {
		t.Fatalf("wrong verb receipt status=%d body=%s", got.Code, got.Body.String())
	}
	if got := request(nil, clusterID); got.Code != http.StatusNotFound {
		t.Fatalf("revoked receipt status=%d body=%s", got.Code, got.Body.String())
	}
	if got := request(routeSecurityBindings(rbac.ResourceClusters, rbac.VerbRead), uuid.New()); got.Code != http.StatusNotFound {
		t.Fatalf("cross-cluster receipt status=%d body=%s", got.Code, got.Body.String())
	}

	resourceHandler.SetAuthorization(rbac.NewEngine(), routeSecurityRBACQuerier{bindings: routeSecurityBindings(rbac.ResourceClusters, rbac.VerbRead)})
	router := NewRouter(&config.Config{}, RouterDependencies{
		JWT: jwt, RBACEngine: rbac.NewEngine(), RBACQueries: routeSecurityRBACQuerier{}, Resources: resourceHandler,
	})
	unauthenticated := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/"+clusterID.String()+"/resources/operations/"+operationID.String()+"/", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, unauthenticated)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated receipt status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
