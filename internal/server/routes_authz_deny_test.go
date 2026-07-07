package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

// TestSecurityMutatingRoutesRequireSecurityRBAC proves the mutating security
// routes reject a zero-grant viewer with 403. Before the fix these routes sat
// behind ONLY the feature-flag gate, so any authenticated principal could
// create/update/delete templates and policies and launch scans.
func TestSecurityMutatingRoutesRequireSecurityRBAC(t *testing.T) {
	jwtMgr := auth.NewJWTManager("route-security-test-secret", 60)
	token, err := jwtMgr.GenerateAccessToken(uuid.New())
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	router := NewRouter(&config.Config{}, RouterDependencies{
		JWT:         jwtMgr,
		RBACEngine:  rbac.NewEngine(),
		RBACQueries: routeSecurityRBACQuerier{bindings: routeSecurityReadOnlyBindings()},
		Security:    handler.NewSecurityHandler(nil),
	})

	for _, tc := range []struct {
		name   string
		method string
		path   string
	}{
		{"create_template", http.MethodPost, "/api/v1/security/templates/"},
		{"update_template", http.MethodPut, "/api/v1/security/templates/" + uuid.NewString() + "/"},
		{"delete_template", http.MethodDelete, "/api/v1/security/templates/" + uuid.NewString() + "/"},
		{"create_policy", http.MethodPost, "/api/v1/security/policies/"},
		{"apply_policy", http.MethodPost, "/api/v1/security/policies/" + uuid.NewString() + "/apply/"},
		{"delete_policy", http.MethodDelete, "/api/v1/security/policies/" + uuid.NewString() + "/"},
		{"create_scan", http.MethodPost, "/api/v1/security/scans/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.Header.Set("Authorization", "Bearer "+token)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s %s status = %d, want %d; body=%s", tc.method, tc.path, rec.Code, http.StatusForbidden, rec.Body.String())
			}
		})
	}
}

// TestCatalogRepositoryRoutesRequireCatalogRBAC proves repository CRUD refuses a
// zero-grant viewer, honoring the catalog:create/update/delete requirement that
// docs/security-sensitive-routes.json already declares.
func TestCatalogRepositoryRoutesRequireCatalogRBAC(t *testing.T) {
	jwtMgr := auth.NewJWTManager("route-security-test-secret", 60)
	token, err := jwtMgr.GenerateAccessToken(uuid.New())
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	router := NewRouter(&config.Config{}, RouterDependencies{
		JWT:         jwtMgr,
		RBACEngine:  rbac.NewEngine(),
		RBACQueries: routeSecurityRBACQuerier{bindings: routeSecurityReadOnlyBindings()},
		Catalog:     handler.NewCatalogHandler(nil),
	})

	for _, tc := range []struct {
		name   string
		method string
		path   string
	}{
		{"create_repo", http.MethodPost, "/api/v1/catalog/repositories/"},
		{"update_repo", http.MethodPut, "/api/v1/catalog/repositories/" + uuid.NewString() + "/"},
		{"delete_repo", http.MethodDelete, "/api/v1/catalog/repositories/" + uuid.NewString() + "/"},
		{"sync_repo", http.MethodPost, "/api/v1/catalog/repositories/" + uuid.NewString() + "/sync/"},
		{"test_connection", http.MethodPost, "/api/v1/catalog/repositories/" + uuid.NewString() + "/test-connection/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.Header.Set("Authorization", "Bearer "+token)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s %s status = %d, want %d; body=%s", tc.method, tc.path, rec.Code, http.StatusForbidden, rec.Body.String())
			}
		})
	}
}

// TestControllersMutatingRoutesRequireSuperuser proves the platform-admin
// controllers surface refuses a non-superuser principal with 403. Before the
// fix the mutating routes were reachable by any authenticated caller.
func TestControllersMutatingRoutesRequireSuperuser(t *testing.T) {
	jwtMgr := auth.NewJWTManager("route-security-test-secret", 60)
	userID := uuid.New()
	token, err := jwtMgr.GenerateAccessToken(userID)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	router := NewRouter(&config.Config{}, RouterDependencies{
		JWT: jwtMgr,
		// Non-superuser user resolved by the superuser gate.
		AuthQueries:  routeSecurityTokenAuthQuerier{user: sqlc.User{ID: userID, IsActive: true}},
		RBACEngine:   rbac.NewEngine(),
		RBACQueries:  routeSecurityRBACQuerier{bindings: routeSecurityAdminBindings()},
		ControlPlane: handler.NewControlPlaneHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil),
	})

	for _, tc := range []struct {
		name   string
		method string
		path   string
	}{
		{"update_policy", http.MethodPut, "/api/v1/controllers/policy/"},
		{"acknowledge_alert", http.MethodPost, "/api/v1/controllers/alerts/" + uuid.NewString() + "/acknowledge/"},
		{"create_silence", http.MethodPost, "/api/v1/controllers/silences/"},
		{"delete_silence", http.MethodDelete, "/api/v1/controllers/silences/" + uuid.NewString() + "/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.Header.Set("Authorization", "Bearer "+token)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s %s status = %d, want %d; body=%s", tc.method, tc.path, rec.Code, http.StatusForbidden, rec.Body.String())
			}
		})
	}
}
