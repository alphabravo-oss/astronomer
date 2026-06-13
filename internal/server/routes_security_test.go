package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/events"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/tunnel"
	"github.com/alphabravocompany/astronomer-go/internal/tunnel2"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

type routeSecurityRBACQuerier struct {
	bindings []rbac.RoleBinding
}

func (q routeSecurityRBACQuerier) GetUserBindings(_ context.Context, _ string) ([]rbac.RoleBinding, error) {
	return q.bindings, nil
}

type routeSecurityArgoTokenQuerier struct {
	tokenHash string
	clusterID uuid.UUID
	touched   int
}

func (q *routeSecurityArgoTokenQuerier) GetArgoCDClusterProxyTokenByHash(_ context.Context, tokenHash string) (sqlc.ArgocdClusterProxyToken, error) {
	if tokenHash != q.tokenHash {
		return sqlc.ArgocdClusterProxyToken{}, errRouteSecurityNotFound{}
	}
	return sqlc.ArgocdClusterProxyToken{ID: uuid.New(), ClusterID: q.clusterID}, nil
}

func (q *routeSecurityArgoTokenQuerier) TouchArgoCDClusterProxyToken(_ context.Context, _ uuid.UUID) error {
	q.touched++
	return nil
}

type errRouteSecurityNotFound struct{}

func (errRouteSecurityNotFound) Error() string { return "not found" }

type routeSecurityTokenAuthQuerier struct {
	tokenHash string
	token     sqlc.ApiToken
	user      sqlc.User
}

func (q routeSecurityTokenAuthQuerier) GetTokenByHash(_ context.Context, tokenHash string) (sqlc.ApiToken, error) {
	if tokenHash != q.tokenHash {
		return sqlc.ApiToken{}, errRouteSecurityNotFound{}
	}
	return q.token, nil
}

func (q routeSecurityTokenAuthQuerier) GetUserByID(_ context.Context, id uuid.UUID) (sqlc.User, error) {
	if id != q.user.ID {
		return sqlc.User{}, errRouteSecurityNotFound{}
	}
	return q.user, nil
}

func (q routeSecurityTokenAuthQuerier) UpdateAPITokenLastUsed(context.Context, uuid.UUID) error {
	return nil
}

type routeSecurityAuditWriter struct {
	rows []sqlc.CreateAuditLogV1Params
}

func (w *routeSecurityAuditWriter) CreateAuditLogV1(_ context.Context, arg sqlc.CreateAuditLogV1Params) error {
	w.rows = append(w.rows, arg)
	return nil
}

type routeSecurityServiceProxyRequester struct{}

func (routeSecurityServiceProxyRequester) Do(context.Context, string, string, string, []byte, map[string]string) (*protocol.K8sResponsePayload, error) {
	return &protocol.K8sResponsePayload{StatusCode: http.StatusNoContent}, nil
}

type routeSecurityServiceProxyTools struct {
	tools []sqlc.ClusterTool
}

func (q routeSecurityServiceProxyTools) ListEnabledTools(context.Context) ([]sqlc.ClusterTool, error) {
	return q.tools, nil
}

func TestLongLivedClusterRoutesRequireAuth(t *testing.T) {
	jwtMgr := auth.NewJWTManager("route-security-test-secret", 60)
	clusterID := uuid.New()
	router := NewRouter(&config.Config{}, RouterDependencies{
		JWT:                 jwtMgr,
		RBACEngine:          rbac.NewEngine(),
		RBACQueries:         routeSecurityRBACQuerier{bindings: routeSecurityAdminBindings()},
		ClusterRegistration: handler.NewClusterRegistrationHandler(nil, events.NewBus()),
		PlatformSettings:    handler.NewPlatformSettingsHandler(nil),
		RemoteServer:        tunnel2.NewRemoteServer(slog.Default(), nil),
	})

	cases := []struct {
		name string
		path string
	}{
		{
			name: "registration event stream",
			path: "/api/v1/clusters/" + clusterID.String() + "/registration/events/",
		},
		{
			name: "remotedialer pod demo",
			path: "/api/v1/clusters/" + clusterID.String() + "/v2/pods/",
		},
		{
			name: "feature flags",
			path: "/api/v1/settings/features/",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusUnauthorized, rec.Body.String())
			}
		})
	}
}

func TestHighRiskRoutesDenyUnauthenticatedRequests(t *testing.T) {
	jwtMgr := auth.NewJWTManager("route-security-test-secret", 60)
	clusterID := uuid.New()
	router := NewRouter(&config.Config{}, RouterDependencies{
		JWT:                 jwtMgr,
		RBACEngine:          rbac.NewEngine(),
		RBACQueries:         routeSecurityRBACQuerier{bindings: routeSecurityAdminBindings()},
		ClusterRegistration: handler.NewClusterRegistrationHandler(nil, events.NewBus()),
		Proxy:               tunnel.NewProxyHandler(tunnel.NewHub(slog.Default()), slog.Default()),
		ArgoCDProxyTokens:   &routeSecurityArgoTokenQuerier{clusterID: clusterID},
		ServiceProxy:        routeSecurityServiceProxy(),
		RemoteServer:        tunnel2.NewRemoteServer(slog.Default(), nil),
	})

	routeSamples := map[string]string{
		"registration-events": "/api/v1/clusters/" + clusterID.String() + "/registration/events/",
		"remotedialer-pods":   "/api/v1/clusters/" + clusterID.String() + "/v2/pods/",
		"generic-k8s-proxy":   "/api/v1/clusters/" + clusterID.String() + "/k8s/api/v1/pods",
		"argocd-k8s-proxy":    "/api/v1/internal/argocd/clusters/" + clusterID.String() + "/k8s/api/v1/pods",
		"service-proxy":       "/api/v1/clusters/" + clusterID.String() + "/proxy/service/observability/grafana:3000/",
	}
	seen := make(map[string]bool, len(routeSamples))

	err := chi.Walk(router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		name := highRiskRouteName(route)
		if name == "" {
			return nil
		}
		sample := routeSamples[name]
		seen[name] = true
		req := httptest.NewRequest(methodForRoute(method), sample, nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s (%s) unauthenticated status = %d, want 401; body=%s", method, route, name, rec.Code, rec.Body.String())
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk routes: %v", err)
	}
	for name := range routeSamples {
		if !seen[name] {
			t.Fatalf("high-risk route category %s was not registered", name)
		}
	}
}

func TestAdminRouteRegistrationsAreAuthProtected(t *testing.T) {
	src, err := os.ReadFile("routes.go")
	if err != nil {
		t.Fatalf("read routes.go: %v", err)
	}
	lines := strings.Split(string(src), "\n")
	inProtectedRoutes := false
	protectedDepth := 0
	checked := 0

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "func registerProtectedRoutes(") {
			inProtectedRoutes = true
			protectedDepth = strings.Count(line, "{") - strings.Count(line, "}")
			continue
		}
		if inProtectedRoutes {
			protectedDepth += strings.Count(line, "{") - strings.Count(line, "}")
			if protectedDepth <= 0 {
				inProtectedRoutes = false
			}
		}

		if !strings.Contains(line, `"/admin/`) {
			continue
		}
		if !looksLikeRouteRegistration(line) {
			continue
		}
		checked++
		if inProtectedRoutes {
			continue
		}
		if strings.Contains(line, "requireAuth(") {
			continue
		}
		t.Fatalf("routes.go:%d registers an /admin/ route without requireAuth and outside registerProtectedRoutes: %s", i+1, strings.TrimSpace(line))
	}

	if checked == 0 {
		t.Fatal("no /admin/ route registrations found")
	}
}

func TestRegistrationEventsRejectQueryJWT(t *testing.T) {
	jwtMgr := auth.NewJWTManager("route-security-test-secret", 60)
	userID := uuid.New()
	token, err := jwtMgr.GenerateAccessToken(userID)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	clusterID := uuid.New()
	router := NewRouter(&config.Config{}, RouterDependencies{
		JWT:                 jwtMgr,
		AuthQueries:         routeSecurityTokenAuthQuerier{user: sqlc.User{ID: userID, IsActive: true}},
		RBACEngine:          rbac.NewEngine(),
		RBACQueries:         routeSecurityRBACQuerier{bindings: routeSecurityAdminBindings()},
		ClusterRegistration: handler.NewClusterRegistrationHandler(nil, events.NewBus()),
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/"+clusterID.String()+"/registration/events/?token="+token, nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
}

func TestRegistrationEventsAuthAndRBAC(t *testing.T) {
	jwtMgr := auth.NewJWTManager("route-security-test-secret", 60)
	userID := uuid.New()
	token, err := jwtMgr.GenerateAccessToken(userID)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	clusterID := uuid.New()

	tests := []struct {
		name       string
		authHeader string
		bindings   []rbac.RoleBinding
		want       int
	}{
		{
			name:       "invalid token",
			authHeader: "Bearer not-a-valid-jwt",
			bindings:   routeSecurityAdminBindings(),
			want:       http.StatusUnauthorized,
		},
		{
			name:       "no cluster access",
			authHeader: "Bearer " + token,
			bindings:   nil,
			want:       http.StatusForbidden,
		},
		{
			name:       "authorized cluster read",
			authHeader: "Bearer " + token,
			bindings:   routeSecurityReadOnlyBindings(),
			want:       http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := NewRouter(&config.Config{}, RouterDependencies{
				JWT:                 jwtMgr,
				AuthQueries:         routeSecurityTokenAuthQuerier{user: sqlc.User{ID: userID, IsActive: true}},
				RBACEngine:          rbac.NewEngine(),
				RBACQueries:         routeSecurityRBACQuerier{bindings: tt.bindings},
				ClusterRegistration: handler.NewClusterRegistrationHandler(nil, events.NewBus()),
			})
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/"+clusterID.String()+"/registration/events/", nil).WithContext(ctx)
			req.Header.Set("Authorization", tt.authHeader)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tt.want, rec.Body.String())
			}
			if tt.want == http.StatusOK && rec.Header().Get("Content-Type") != "text/event-stream" {
				t.Fatalf("Content-Type = %q, want text/event-stream", rec.Header().Get("Content-Type"))
			}
		})
	}
}

func TestRemotedialerPodDemoRouteDisabledInProduction(t *testing.T) {
	jwtMgr := auth.NewJWTManager("route-security-test-secret", 60)
	userID := uuid.New()
	token, err := jwtMgr.GenerateAccessToken(userID)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	clusterID := uuid.New()
	router := NewRouter(&config.Config{Env: "production"}, RouterDependencies{
		JWT:          jwtMgr,
		RBACEngine:   rbac.NewEngine(),
		RBACQueries:  routeSecurityRBACQuerier{bindings: routeSecurityAdminBindings()},
		RemoteServer: tunnel2.NewRemoteServer(slog.Default(), nil),
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/"+clusterID.String()+"/v2/pods/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}

func TestK8sProxyRequiresAuth(t *testing.T) {
	jwtMgr := auth.NewJWTManager("route-security-test-secret", 60)
	clusterID := uuid.New()
	router := NewRouter(&config.Config{}, RouterDependencies{
		JWT:         jwtMgr,
		RBACEngine:  rbac.NewEngine(),
		RBACQueries: routeSecurityRBACQuerier{bindings: routeSecurityAdminBindings()},
		Proxy:       tunnel.NewProxyHandler(tunnel.NewHub(slog.Default()), slog.Default()),
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/"+clusterID.String()+"/k8s/api/v1/pods", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
}

func TestK8sProxyMutationsRequireClusterUpdate(t *testing.T) {
	jwtMgr := auth.NewJWTManager("route-security-test-secret", 60)
	userID := uuid.New()
	token, err := jwtMgr.GenerateAccessToken(userID)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	clusterID := uuid.New()
	router := NewRouter(&config.Config{}, RouterDependencies{
		JWT:         jwtMgr,
		RBACEngine:  rbac.NewEngine(),
		RBACQueries: routeSecurityRBACQuerier{bindings: routeSecurityReadOnlyBindings()},
		Proxy:       tunnel.NewProxyHandler(tunnel.NewHub(slog.Default()), slog.Default()),
	})

	readReq := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/"+clusterID.String()+"/k8s/api/v1/pods", nil)
	readReq.Header.Set("Authorization", "Bearer "+token)
	readRec := httptest.NewRecorder()
	router.ServeHTTP(readRec, readReq)
	if readRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("read status = %d, want proxy handler %d; body=%s", readRec.Code, http.StatusServiceUnavailable, readRec.Body.String())
	}

	writeReq := httptest.NewRequest(http.MethodDelete, "/api/v1/clusters/"+clusterID.String()+"/k8s/api/v1/namespaces/default/pods/example", nil)
	writeReq.Header.Set("Authorization", "Bearer "+token)
	writeRec := httptest.NewRecorder()
	router.ServeHTTP(writeRec, writeReq)
	if writeRec.Code != http.StatusForbidden {
		t.Fatalf("write status = %d, want %d; body=%s", writeRec.Code, http.StatusForbidden, writeRec.Body.String())
	}
}

func TestK8sProxyMutationsAreAudited(t *testing.T) {
	jwtMgr := auth.NewJWTManager("route-security-test-secret", 60)
	userID := uuid.New()
	token, err := jwtMgr.GenerateAccessToken(userID)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	clusterID := uuid.New()
	audit := &routeSecurityAuditWriter{}
	router := NewRouter(&config.Config{}, RouterDependencies{
		JWT:         jwtMgr,
		RBACEngine:  rbac.NewEngine(),
		RBACQueries: routeSecurityRBACQuerier{bindings: routeSecurityAdminBindings()},
		AuditWriter: audit,
		Proxy:       tunnel.NewProxyHandler(tunnel.NewHub(slog.Default()), slog.Default()),
	})

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/clusters/"+clusterID.String()+"/k8s/api/v1/namespaces/default/pods/example", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want proxy handler %d; body=%s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
	if len(audit.rows) != 1 {
		t.Fatalf("audit rows = %d, want 1", len(audit.rows))
	}
	if audit.rows[0].Action != "cluster.k8s_proxy.forwarded" {
		t.Fatalf("audit action = %q", audit.rows[0].Action)
	}
	if audit.rows[0].ResourceID != clusterID.String() {
		t.Fatalf("audit resource id = %q, want %q", audit.rows[0].ResourceID, clusterID.String())
	}
	var detail map[string]any
	if err := json.Unmarshal(audit.rows[0].Detail, &detail); err != nil {
		t.Fatalf("unmarshal audit detail: %v", err)
	}
	if detail["namespace"] != "default" || detail["resource"] != "pods" || detail["name"] != "example" {
		t.Fatalf("audit detail object ref = %#v", detail)
	}
}

func TestK8sProxyPodExecRequiresPodExecPermission(t *testing.T) {
	jwtMgr := auth.NewJWTManager("route-security-test-secret", 60)
	userID := uuid.New()
	token, err := jwtMgr.GenerateAccessToken(userID)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	clusterID := uuid.New()

	readOnlyRouter := NewRouter(&config.Config{}, RouterDependencies{
		JWT:         jwtMgr,
		RBACEngine:  rbac.NewEngine(),
		RBACQueries: routeSecurityRBACQuerier{bindings: routeSecurityReadOnlyBindings()},
		Proxy:       tunnel.NewProxyHandler(tunnel.NewHub(slog.Default()), slog.Default()),
	})
	execPath := "/api/v1/clusters/" + clusterID.String() + "/k8s/api/v1/namespaces/default/pods/example/exec"
	readOnlyReq := httptest.NewRequest(http.MethodPost, execPath, nil)
	readOnlyReq.Header.Set("Authorization", "Bearer "+token)
	readOnlyRec := httptest.NewRecorder()
	readOnlyRouter.ServeHTTP(readOnlyRec, readOnlyReq)
	if readOnlyRec.Code != http.StatusForbidden {
		t.Fatalf("read-only exec status = %d, want %d; body=%s", readOnlyRec.Code, http.StatusForbidden, readOnlyRec.Body.String())
	}

	execRouter := NewRouter(&config.Config{}, RouterDependencies{
		JWT:         jwtMgr,
		RBACEngine:  rbac.NewEngine(),
		RBACQueries: routeSecurityRBACQuerier{bindings: routeSecurityPodExecBindings()},
		Proxy:       tunnel.NewProxyHandler(tunnel.NewHub(slog.Default()), slog.Default()),
	})
	execReq := httptest.NewRequest(http.MethodPost, execPath, nil)
	execReq.Header.Set("Authorization", "Bearer "+token)
	execRec := httptest.NewRecorder()
	execRouter.ServeHTTP(execRec, execReq)
	if execRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("pod-exec status = %d, want proxy handler %d; body=%s", execRec.Code, http.StatusServiceUnavailable, execRec.Body.String())
	}
}

func TestServiceProxyRequiresAuth(t *testing.T) {
	jwtMgr := auth.NewJWTManager("route-security-test-secret", 60)
	clusterID := uuid.New()
	router := NewRouter(&config.Config{}, RouterDependencies{
		JWT:          jwtMgr,
		RBACEngine:   rbac.NewEngine(),
		RBACQueries:  routeSecurityRBACQuerier{bindings: routeSecurityAdminBindings()},
		ServiceProxy: routeSecurityServiceProxy(),
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/"+clusterID.String()+"/proxy/service/observability/grafana:3000/", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
}

func TestServiceProxyMutationsRequireClusterUpdate(t *testing.T) {
	jwtMgr := auth.NewJWTManager("route-security-test-secret", 60)
	userID := uuid.New()
	token, err := jwtMgr.GenerateAccessToken(userID)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	clusterID := uuid.New()
	router := NewRouter(&config.Config{}, RouterDependencies{
		JWT:          jwtMgr,
		RBACEngine:   rbac.NewEngine(),
		RBACQueries:  routeSecurityRBACQuerier{bindings: routeSecurityReadOnlyBindings()},
		ServiceProxy: routeSecurityServiceProxy(),
	})

	readReq := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/"+clusterID.String()+"/proxy/service/observability/grafana:3000/", nil)
	readReq.Header.Set("Authorization", "Bearer "+token)
	readRec := httptest.NewRecorder()
	router.ServeHTTP(readRec, readReq)
	if readRec.Code != http.StatusNoContent {
		t.Fatalf("read status = %d, want %d; body=%s", readRec.Code, http.StatusNoContent, readRec.Body.String())
	}

	writeReq := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/"+clusterID.String()+"/proxy/service/observability/grafana:3000/", nil)
	writeReq.Header.Set("Authorization", "Bearer "+token)
	writeRec := httptest.NewRecorder()
	router.ServeHTTP(writeRec, writeReq)
	if writeRec.Code != http.StatusForbidden {
		t.Fatalf("write status = %d, want %d; body=%s", writeRec.Code, http.StatusForbidden, writeRec.Body.String())
	}
}

func TestServiceProxyAllowsClusterUpdateMutation(t *testing.T) {
	jwtMgr := auth.NewJWTManager("route-security-test-secret", 60)
	userID := uuid.New()
	token, err := jwtMgr.GenerateAccessToken(userID)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	clusterID := uuid.New()
	router := NewRouter(&config.Config{}, RouterDependencies{
		JWT:          jwtMgr,
		RBACEngine:   rbac.NewEngine(),
		RBACQueries:  routeSecurityRBACQuerier{bindings: routeSecurityAdminBindings()},
		ServiceProxy: routeSecurityServiceProxy(),
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/"+clusterID.String()+"/proxy/service/observability/grafana:3000/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusNoContent, rec.Body.String())
	}
}

func TestServiceProxyAPITokenMutationsRequireWriteScope(t *testing.T) {
	jwtMgr := auth.NewJWTManager("route-security-test-secret", 60)
	clusterID := uuid.New()
	userID := uuid.New()
	rawToken := "astro_route_security_service_proxy_scope"

	readOnlyRouter := NewRouter(&config.Config{}, RouterDependencies{
		JWT:          jwtMgr,
		AuthQueries:  routeSecurityAPITokenQuerier(rawToken, userID, json.RawMessage(`["read"]`)),
		RBACEngine:   rbac.NewEngine(),
		RBACQueries:  routeSecurityRBACQuerier{bindings: routeSecurityAdminBindings()},
		ServiceProxy: routeSecurityServiceProxy(),
	})
	readOnlyReq := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/"+clusterID.String()+"/proxy/service/observability/grafana:3000/", nil)
	readOnlyReq.Header.Set("Authorization", "Bearer "+rawToken)
	readOnlyRec := httptest.NewRecorder()
	readOnlyRouter.ServeHTTP(readOnlyRec, readOnlyReq)
	if readOnlyRec.Code != http.StatusForbidden {
		t.Fatalf("read-only token status = %d, want %d; body=%s", readOnlyRec.Code, http.StatusForbidden, readOnlyRec.Body.String())
	}

	writeRouter := NewRouter(&config.Config{}, RouterDependencies{
		JWT:          jwtMgr,
		AuthQueries:  routeSecurityAPITokenQuerier(rawToken, userID, json.RawMessage(`["clusters:write"]`)),
		RBACEngine:   rbac.NewEngine(),
		RBACQueries:  routeSecurityRBACQuerier{bindings: routeSecurityAdminBindings()},
		ServiceProxy: routeSecurityServiceProxy(),
	})
	writeReq := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/"+clusterID.String()+"/proxy/service/observability/grafana:3000/", nil)
	writeReq.Header.Set("Authorization", "Bearer "+rawToken)
	writeRec := httptest.NewRecorder()
	writeRouter.ServeHTTP(writeRec, writeReq)
	if writeRec.Code != http.StatusNoContent {
		t.Fatalf("write-scoped token status = %d, want %d; body=%s", writeRec.Code, http.StatusNoContent, writeRec.Body.String())
	}
}

func TestServiceProxyRejectsTargetsOutsideAllowlist(t *testing.T) {
	jwtMgr := auth.NewJWTManager("route-security-test-secret", 60)
	userID := uuid.New()
	token, err := jwtMgr.GenerateAccessToken(userID)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	clusterID := uuid.New()
	router := NewRouter(&config.Config{}, RouterDependencies{
		JWT:          jwtMgr,
		RBACEngine:   rbac.NewEngine(),
		RBACQueries:  routeSecurityRBACQuerier{bindings: routeSecurityAdminBindings()},
		ServiceProxy: routeSecurityServiceProxy(),
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/"+clusterID.String()+"/proxy/service/observability/prometheus:9090/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
}

func TestArgoCDInternalK8sProxyRequiresClusterScopedToken(t *testing.T) {
	clusterID := uuid.New()
	otherClusterID := uuid.New()
	token := auth.ArgoCDClusterProxyTokenPrefix + "test-token"
	tokens := &routeSecurityArgoTokenQuerier{
		tokenHash: auth.HashArgoCDClusterProxyToken(token),
		clusterID: clusterID,
	}
	router := NewRouter(&config.Config{}, RouterDependencies{
		Proxy:             tunnel.NewProxyHandler(tunnel.NewHub(slog.Default()), slog.Default()),
		ArgoCDProxyTokens: tokens,
	})
	path := "/api/v1/internal/argocd/clusters/" + clusterID.String() + "/k8s/api/v1/pods"

	unauthReq := httptest.NewRequest(http.MethodGet, path, nil)
	unauthRec := httptest.NewRecorder()
	router.ServeHTTP(unauthRec, unauthReq)
	if unauthRec.Code != http.StatusUnauthorized {
		t.Fatalf("unauth status = %d, want %d; body=%s", unauthRec.Code, http.StatusUnauthorized, unauthRec.Body.String())
	}

	wrongClusterReq := httptest.NewRequest(http.MethodGet, "/api/v1/internal/argocd/clusters/"+otherClusterID.String()+"/k8s/api/v1/pods", nil)
	wrongClusterReq.Header.Set("Authorization", "Bearer "+token)
	wrongClusterRec := httptest.NewRecorder()
	router.ServeHTTP(wrongClusterRec, wrongClusterReq)
	if wrongClusterRec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong-cluster status = %d, want %d; body=%s", wrongClusterRec.Code, http.StatusUnauthorized, wrongClusterRec.Body.String())
	}

	validReq := httptest.NewRequest(http.MethodGet, path, nil)
	validReq.Header.Set("Authorization", "Bearer "+token)
	validRec := httptest.NewRecorder()
	router.ServeHTTP(validRec, validReq)
	if validRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("valid status = %d, want proxy handler %d; body=%s", validRec.Code, http.StatusServiceUnavailable, validRec.Body.String())
	}
	if tokens.touched != 1 {
		t.Fatalf("touch count = %d, want 1", tokens.touched)
	}
}

func routeSecurityAdminBindings() []rbac.RoleBinding {
	return []rbac.RoleBinding{{
		RoleRules: []rbac.Rule{{Resource: "*", Verbs: []string{"*"}}},
	}}
}

func routeSecurityReadOnlyBindings() []rbac.RoleBinding {
	return []rbac.RoleBinding{{
		RoleRules: []rbac.Rule{{Resource: "*", Verbs: []string{"read", "list"}}},
	}}
}

func routeSecurityPodExecBindings() []rbac.RoleBinding {
	return []rbac.RoleBinding{{
		RoleRules: []rbac.Rule{{Resource: string(rbac.ResourcePods), Verbs: []string{string(rbac.VerbExec)}}},
	}}
}

func routeSecurityServiceProxy() *handler.ServiceProxyHandler {
	h := handler.NewServiceProxyHandler(routeSecurityServiceProxyRequester{})
	h.SetToolQuerier(routeSecurityServiceProxyTools{tools: []sqlc.ClusterTool{{
		ServiceName: "grafana",
		ServicePort: pgtype.Int4{Int32: 3000, Valid: true},
	}}})
	return h
}

func routeSecurityAPITokenQuerier(rawToken string, userID uuid.UUID, scopes json.RawMessage) routeSecurityTokenAuthQuerier {
	sum := sha256.Sum256([]byte(rawToken))
	return routeSecurityTokenAuthQuerier{
		tokenHash: hex.EncodeToString(sum[:]),
		token: sqlc.ApiToken{
			ID:     uuid.New(),
			UserID: userID,
			Scopes: scopes,
		},
		user: sqlc.User{
			ID:       userID,
			Email:    "route-security@example.com",
			Username: "route-security",
			IsActive: true,
		},
	}
}

func methodForRoute(method string) string {
	if method == "" || method == "*" {
		return http.MethodGet
	}
	return method
}

func highRiskRouteName(route string) string {
	switch {
	case route == "/api/v1/clusters/{id}/registration/events/":
		return "registration-events"
	case route == "/api/v1/clusters/{id}/v2/pods/":
		return "remotedialer-pods"
	case route == "/api/v1/internal/argocd/clusters/{cluster_id}/k8s/*":
		return "argocd-k8s-proxy"
	case route == "/api/v1/clusters/{cluster_id}/k8s/*":
		return "generic-k8s-proxy"
	case strings.Contains(route, "/proxy/service/"):
		return "service-proxy"
	default:
		return ""
	}
}

func looksLikeRouteRegistration(line string) bool {
	for _, method := range []string{".Get(", ".Post(", ".Put(", ".Patch(", ".Delete(", ".Handle(", ".HandleFunc(", ".Mount(", ".Route("} {
		if strings.Contains(line, method) {
			return true
		}
	}
	return false
}
