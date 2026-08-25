package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

type routeScopedSecurityQuerier struct {
	handler.SecurityQuerier
	scan sqlc.SecurityScanResult
}

func (q routeScopedSecurityQuerier) GetSecurityScanResultByClusterAndID(_ context.Context, arg sqlc.GetSecurityScanResultByClusterAndIDParams) (sqlc.SecurityScanResult, error) {
	if q.scan.ID == arg.ID && q.scan.ClusterID == arg.ClusterID {
		return q.scan, nil
	}
	return sqlc.SecurityScanResult{}, pgx.ErrNoRows
}

func (q routeScopedSecurityQuerier) CountSecurityScanResultsByCluster(_ context.Context, clusterID uuid.UUID) (int64, error) {
	if q.scan.ClusterID == clusterID {
		return 1, nil
	}
	return 0, nil
}

func (q routeScopedSecurityQuerier) GetActiveSecurityScanResultByID(_ context.Context, id uuid.UUID) (sqlc.SecurityScanResult, error) {
	if q.scan.ID == id {
		return q.scan, nil
	}
	return sqlc.SecurityScanResult{}, pgx.ErrNoRows
}

func (q routeScopedSecurityQuerier) GetActiveSecurityScanResultByIDForScopes(_ context.Context, arg sqlc.GetActiveSecurityScanResultByIDForScopesParams) (sqlc.SecurityScanResult, error) {
	if q.scan.ID != arg.ID {
		return sqlc.SecurityScanResult{}, pgx.ErrNoRows
	}
	for _, clusterID := range arg.ClusterIds {
		if clusterID == q.scan.ClusterID {
			return q.scan, nil
		}
	}
	return sqlc.SecurityScanResult{}, pgx.ErrNoRows
}

func (q routeScopedSecurityQuerier) ListSecurityScanResultsForScopes(context.Context, sqlc.ListSecurityScanResultsForScopesParams) ([]sqlc.SecurityScanResult, error) {
	return nil, nil
}

func (q routeScopedSecurityQuerier) CountSecurityScanResultsForScopes(context.Context, []uuid.UUID) (int64, error) {
	return 0, nil
}

func securityRouteBindings(clusterID uuid.UUID, includeSecurity, includeCluster bool) []rbac.RoleBinding {
	bindings := []rbac.RoleBinding{}
	if includeSecurity {
		bindings = append(bindings, rbac.RoleBinding{
			Scope: "global", RoleRules: []rbac.Rule{{Resource: string(rbac.ResourceSecurity), Verbs: []string{string(rbac.VerbRead)}}},
		})
	}
	if includeCluster {
		bindings = append(bindings, rbac.RoleBinding{
			Scope: "cluster", ClusterID: clusterID.String(),
			RoleRules: []rbac.Rule{{Resource: string(rbac.ResourceClusters), Verbs: []string{string(rbac.VerbRead)}}},
		})
	}
	return bindings
}

func newSecurityReadScopeRouter(t *testing.T, bindings []rbac.RoleBinding, scan sqlc.SecurityScanResult) (http.Handler, string) {
	t.Helper()
	jwtManager := auth.MustNewJWTManager("security-read-scope-route-secret", 60)
	token, err := jwtManager.GenerateAccessToken(uuid.New())
	if err != nil {
		t.Fatalf("generate access token: %v", err)
	}
	engine := rbac.NewEngine()
	rbacQueries := routeSecurityRBACQuerier{bindings: bindings}
	security := handler.NewSecurityHandler(routeScopedSecurityQuerier{scan: scan})
	security.SetAuthorization(engine, rbacQueries)
	return NewRouter(&config.Config{}, RouterDependencies{
		JWT: jwtManager, RBACEngine: engine, RBACQueries: rbacQueries, Security: security,
	}), token
}

func TestSecurityReadRouteAuthorizationPersonas(t *testing.T) {
	clusterA, clusterB := uuid.New(), uuid.New()
	scan := sqlc.SecurityScanResult{ID: uuid.New(), ClusterID: clusterA, ScanType: "cis", Status: "complete"}
	globalPath := "/api/v1/security/scans/" + scan.ID.String() + "/"
	clusterPath := "/api/v1/clusters/" + clusterA.String() + "/security/scans/" + scan.ID.String() + "/"

	tests := []struct {
		name       string
		bindings   []rbac.RoleBinding
		path       string
		authorized bool
		wantStatus int
	}{
		{name: "unauthenticated", bindings: securityRouteBindings(clusterA, true, true), path: globalPath, authorized: false, wantStatus: http.StatusUnauthorized},
		{name: "authenticated without grant", path: globalPath, authorized: true, wantStatus: http.StatusForbidden},
		{name: "global security reader still needs cluster visibility", bindings: securityRouteBindings(clusterA, true, false), path: globalPath, authorized: true, wantStatus: http.StatusNotFound},
		{name: "global security reader with visible cluster", bindings: securityRouteBindings(clusterA, true, true), path: globalPath, authorized: true, wantStatus: http.StatusOK},
		{name: "cluster reader", bindings: securityRouteBindings(clusterA, false, true), path: clusterPath, authorized: true, wantStatus: http.StatusOK},
		{name: "wrong cluster reader", bindings: securityRouteBindings(clusterB, false, true), path: clusterPath, authorized: true, wantStatus: http.StatusForbidden},
		{name: "superuser", bindings: []rbac.RoleBinding{{IsSuperuser: true}}, path: globalPath, authorized: true, wantStatus: http.StatusOK},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			router, token := newSecurityReadScopeRouter(t, tc.bindings, scan)
			request := httptest.NewRequest(http.MethodGet, tc.path, nil)
			if tc.authorized {
				request.Header.Set("Authorization", "Bearer "+token)
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, tc.wantStatus, response.Body.String())
			}
		})
	}
}
