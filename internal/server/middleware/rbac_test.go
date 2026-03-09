package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

// mockRBACQuerier implements RBACQuerier for testing.
type mockRBACQuerier struct {
	bindings []rbac.RoleBinding
	err      error
}

func (m *mockRBACQuerier) GetUserBindings(_ context.Context, _ string) ([]rbac.RoleBinding, error) {
	return m.bindings, m.err
}

// adminBindings returns global bindings with wildcard access to all resources.
func adminBindings() []rbac.RoleBinding {
	return []rbac.RoleBinding{
		{
			RoleRules: []rbac.Rule{
				{Resource: "*", Verbs: []string{"*"}},
			},
		},
	}
}

// readOnlyBindings returns global bindings with read+list access to all resources.
func readOnlyBindings() []rbac.RoleBinding {
	return []rbac.RoleBinding{
		{
			RoleRules: []rbac.Rule{
				{Resource: "*", Verbs: []string{"read", "list"}},
			},
		},
	}
}

// clusterBindings returns bindings scoped to a specific cluster.
func clusterBindings(clusterID string) []rbac.RoleBinding {
	return []rbac.RoleBinding{
		{
			ClusterID: clusterID,
			RoleRules: []rbac.Rule{
				{Resource: "*", Verbs: []string{"*"}},
			},
		},
	}
}

// setupChiRequest creates a request with chi URL params injected into the context.
func setupChiRequest(r *http.Request, params map[string]string) *http.Request {
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func TestRBAC_AdminAccessGranted(t *testing.T) {
	engine := rbac.NewEngine()
	querier := &mockRBACQuerier{bindings: adminBindings()}

	mw := RequirePermission(engine, querier, rbac.ResourceClusters, rbac.VerbCreate)

	handlerCalled := false
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/clusters", nil)
	ctx := SetAuthenticatedUserForTest(req.Context(), &AuthenticatedUser{
		ID: uuid.New().String(), Email: "admin@test.com",
	})
	req = req.WithContext(ctx)
	req = setupChiRequest(req, nil)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	if !handlerCalled {
		t.Error("expected handler to be called")
	}
}

func TestRBAC_ReadOnlyAccessRead(t *testing.T) {
	engine := rbac.NewEngine()
	querier := &mockRBACQuerier{bindings: readOnlyBindings()}

	mw := RequirePermission(engine, querier, rbac.ResourceClusters, rbac.VerbRead)

	handlerCalled := false
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/clusters/123", nil)
	ctx := SetAuthenticatedUserForTest(req.Context(), &AuthenticatedUser{
		ID: uuid.New().String(), Email: "viewer@test.com",
	})
	req = req.WithContext(ctx)
	req = setupChiRequest(req, nil)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	if !handlerCalled {
		t.Error("expected handler to be called")
	}
}

func TestRBAC_ReadOnlyAccessWriteDenied(t *testing.T) {
	engine := rbac.NewEngine()
	querier := &mockRBACQuerier{bindings: readOnlyBindings()}

	mw := RequirePermission(engine, querier, rbac.ResourceClusters, rbac.VerbCreate)

	handlerCalled := false
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/clusters", nil)
	ctx := SetAuthenticatedUserForTest(req.Context(), &AuthenticatedUser{
		ID: uuid.New().String(), Email: "viewer@test.com",
	})
	req = req.WithContext(ctx)
	req = setupChiRequest(req, nil)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rr.Code)
	}
	if handlerCalled {
		t.Error("expected handler NOT to be called")
	}

	var body map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	errObj, ok := body["error"].(map[string]interface{})
	if !ok {
		t.Fatal("expected error object in response")
	}
	if errObj["code"] != "permission_denied" {
		t.Errorf("expected code 'permission_denied', got %v", errObj["code"])
	}
}

func TestRBAC_Unauthenticated(t *testing.T) {
	engine := rbac.NewEngine()
	querier := &mockRBACQuerier{bindings: adminBindings()}

	mw := RequirePermission(engine, querier, rbac.ResourceClusters, rbac.VerbRead)

	handlerCalled := false
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	// No authenticated user in context
	req := httptest.NewRequest(http.MethodGet, "/clusters", nil)
	req = setupChiRequest(req, nil)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
	if handlerCalled {
		t.Error("expected handler NOT to be called")
	}
}

func TestRBAC_ClusterScopedCorrectCluster(t *testing.T) {
	clusterID := uuid.New()
	engine := rbac.NewEngine()
	querier := &mockRBACQuerier{bindings: clusterBindings(clusterID.String())}

	mw := RequirePermission(engine, querier, rbac.ResourceWorkloads, rbac.VerbCreate)

	handlerCalled := false
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/clusters/%s/workloads", clusterID), nil)
	ctx := SetAuthenticatedUserForTest(req.Context(), &AuthenticatedUser{
		ID: uuid.New().String(), Email: "cluster-admin@test.com",
	})
	req = req.WithContext(ctx)
	req = setupChiRequest(req, map[string]string{"cluster_id": clusterID.String()})

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	if !handlerCalled {
		t.Error("expected handler to be called")
	}
}

func TestRBAC_ClusterScopedWrongCluster(t *testing.T) {
	clusterID := uuid.New()
	wrongClusterID := uuid.New()
	engine := rbac.NewEngine()
	querier := &mockRBACQuerier{bindings: clusterBindings(clusterID.String())}

	mw := RequirePermission(engine, querier, rbac.ResourceWorkloads, rbac.VerbCreate)

	handlerCalled := false
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/clusters/%s/workloads", wrongClusterID), nil)
	ctx := SetAuthenticatedUserForTest(req.Context(), &AuthenticatedUser{
		ID: uuid.New().String(), Email: "cluster-admin@test.com",
	})
	req = req.WithContext(ctx)
	req = setupChiRequest(req, map[string]string{"cluster_id": wrongClusterID.String()})

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rr.Code)
	}
	if handlerCalled {
		t.Error("expected handler NOT to be called")
	}
}
