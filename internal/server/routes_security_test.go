package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/events"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
	"github.com/alphabravocompany/astronomer-go/internal/kubectl"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	appmiddleware "github.com/alphabravocompany/astronomer-go/internal/server/middleware"
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
	mu   sync.Mutex
	rows []sqlc.CreateAuditLogV1Params
}

func (w *routeSecurityAuditWriter) CreateAuditLogV1(_ context.Context, arg sqlc.CreateAuditLogV1Params) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.rows = append(w.rows, arg)
	return nil
}

func (w *routeSecurityAuditWriter) Rows() []sqlc.CreateAuditLogV1Params {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]sqlc.CreateAuditLogV1Params(nil), w.rows...)
}

func waitForAuditRows(t *testing.T, audit *routeSecurityAuditWriter, want int) []sqlc.CreateAuditLogV1Params {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		rows := audit.Rows()
		if len(rows) >= want {
			return rows
		}
		if time.Now().After(deadline) {
			t.Fatalf("audit rows = %d, want at least %d", len(rows), want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func findAuditRow(t *testing.T, rows []sqlc.CreateAuditLogV1Params, action string) sqlc.CreateAuditLogV1Params {
	t.Helper()
	for _, row := range rows {
		if row.Action == action {
			return row
		}
	}
	t.Fatalf("audit action %q not found in %#v", action, rows)
	return sqlc.CreateAuditLogV1Params{}
}

type routeSecurityAuditReader struct{}

func (routeSecurityAuditReader) GetAuditLogV1ByID(_ context.Context, id uuid.UUID) (sqlc.AuditLog, error) {
	return sqlc.AuditLog{ID: id, CreatedAt: time.Now().UTC(), Action: "audit.viewed"}, nil
}

func (routeSecurityAuditReader) ListAuditLogV1(_ context.Context, _ sqlc.ListAuditLogsParams) ([]sqlc.AuditLog, error) {
	return []sqlc.AuditLog{{ID: uuid.New(), CreatedAt: time.Now().UTC(), Action: "audit.viewed"}}, nil
}

func (routeSecurityAuditReader) ListAuditLogV1ByUser(_ context.Context, _ sqlc.ListAuditLogsByUserParams) ([]sqlc.AuditLog, error) {
	return []sqlc.AuditLog{{ID: uuid.New(), CreatedAt: time.Now().UTC(), Action: "audit.viewed"}}, nil
}

func (routeSecurityAuditReader) ListAuditLogV1ByResourceType(_ context.Context, _ sqlc.ListAuditLogsByResourceTypeParams) ([]sqlc.AuditLog, error) {
	return []sqlc.AuditLog{{ID: uuid.New(), CreatedAt: time.Now().UTC(), Action: "audit.viewed"}}, nil
}

func (routeSecurityAuditReader) ListAuditLogV1ByAction(_ context.Context, _ sqlc.ListAuditLogsByActionParams) ([]sqlc.AuditLog, error) {
	return []sqlc.AuditLog{{ID: uuid.New(), CreatedAt: time.Now().UTC(), Action: "audit.viewed"}}, nil
}

func (routeSecurityAuditReader) ListAuditLogV1ByActionClass(_ context.Context, _ sqlc.ListAuditLogsByActionClassParams) ([]sqlc.AuditLog, error) {
	return []sqlc.AuditLog{{ID: uuid.New(), CreatedAt: time.Now().UTC(), Action: "audit.viewed"}}, nil
}

func (routeSecurityAuditReader) ListAuditLogV1Since(_ context.Context, _ sqlc.ListAuditLogsSinceParams) ([]sqlc.AuditLog, error) {
	return []sqlc.AuditLog{{ID: uuid.New(), CreatedAt: time.Now().UTC(), Action: "audit.viewed"}}, nil
}

func (routeSecurityAuditReader) CountAuditLogV1(_ context.Context) (int64, error) {
	return 1, nil
}

func (routeSecurityAuditReader) CountAuditLogV1ByUser(_ context.Context, _ pgtype.UUID) (int64, error) {
	return 1, nil
}

func (routeSecurityAuditReader) CountAuditLogV1ByActionClass(_ context.Context, _ string) (int64, error) {
	return 1, nil
}

func assertStreamAuditRow(t *testing.T, row sqlc.CreateAuditLogV1Params, action string, userID uuid.UUID, clusterID uuid.UUID, resourceName string, streamKind string) {
	t.Helper()
	if row.Action != action {
		t.Fatalf("audit action = %q, want %q", row.Action, action)
	}
	if row.ResourceType != "cluster" || row.ResourceID != clusterID.String() || row.ResourceName != resourceName {
		t.Fatalf("audit resource = %q/%q/%q, want cluster/%s/%s", row.ResourceType, row.ResourceID, row.ResourceName, clusterID.String(), resourceName)
	}
	if !row.UserID.Valid || row.UserID.Bytes != [16]byte(userID) {
		t.Fatalf("audit user id = %#v, want %s", row.UserID, userID.String())
	}
	if row.ActorAuthMethod != "jwt" {
		t.Fatalf("actor auth method = %q, want jwt", row.ActorAuthMethod)
	}
	var detail map[string]any
	if err := json.Unmarshal(row.Detail, &detail); err != nil {
		t.Fatalf("unmarshal audit detail: %v", err)
	}
	if detail["cluster_id"] != clusterID.String() ||
		detail["namespace"] != "default" ||
		detail["pod"] != "example" ||
		detail["stream_kind"] != streamKind {
		t.Fatalf("audit detail = %#v", detail)
	}
	if streamKind == "exec" && detail["container"] != "shell" {
		t.Fatalf("exec audit container = %#v, want shell", detail["container"])
	}
	if streamKind == "logs" && detail["container"] != "app" {
		t.Fatalf("logs audit container = %#v, want app", detail["container"])
	}
}

type routeSecurityServiceProxyRequester struct{}

func (routeSecurityServiceProxyRequester) Do(context.Context, string, string, string, []byte, map[string]string) (*protocol.K8sResponsePayload, error) {
	return &protocol.K8sResponsePayload{StatusCode: http.StatusNoContent}, nil
}

type routeSecurityGenericResourceRequester struct{}

func (routeSecurityGenericResourceRequester) Do(context.Context, string, string, string, []byte, map[string]string) (*protocol.K8sResponsePayload, error) {
	body := base64.StdEncoding.EncodeToString([]byte(`{"items":[]}`))
	return &protocol.K8sResponsePayload{StatusCode: http.StatusOK, Body: body}, nil
}

type routeSecurityServiceProxyTools struct {
	tools []sqlc.ClusterTool
}

func (q routeSecurityServiceProxyTools) ListEnabledTools(context.Context) ([]sqlc.ClusterTool, error) {
	return q.tools, nil
}

type routeSecurityClusterQuerier struct{}

func (routeSecurityClusterQuerier) GetClusterByID(context.Context, uuid.UUID) (sqlc.Cluster, error) {
	return sqlc.Cluster{}, pgx.ErrNoRows
}
func (routeSecurityClusterQuerier) GetClusterByName(context.Context, string) (sqlc.Cluster, error) {
	return sqlc.Cluster{}, pgx.ErrNoRows
}
func (routeSecurityClusterQuerier) ListClusters(context.Context, sqlc.ListClustersParams) ([]sqlc.Cluster, error) {
	return nil, nil
}
func (routeSecurityClusterQuerier) CreateCluster(context.Context, sqlc.CreateClusterParams) (sqlc.Cluster, error) {
	return sqlc.Cluster{}, nil
}
func (routeSecurityClusterQuerier) UpdateCluster(context.Context, sqlc.UpdateClusterParams) (sqlc.Cluster, error) {
	return sqlc.Cluster{}, nil
}
func (routeSecurityClusterQuerier) DeleteCluster(context.Context, uuid.UUID) error { return nil }
func (routeSecurityClusterQuerier) CountClusters(context.Context) (int64, error)   { return 0, nil }
func (routeSecurityClusterQuerier) CreateClusterDecommission(context.Context, sqlc.CreateClusterDecommissionParams) (sqlc.ClusterDecommission, error) {
	return sqlc.ClusterDecommission{}, nil
}
func (routeSecurityClusterQuerier) GetLatestClusterDecommissionByCluster(context.Context, uuid.UUID) (sqlc.ClusterDecommission, error) {
	return sqlc.ClusterDecommission{}, pgx.ErrNoRows
}
func (routeSecurityClusterQuerier) ListPendingClusterDecommissions(context.Context, int32) ([]sqlc.ClusterDecommission, error) {
	return nil, nil
}

func (routeSecurityClusterQuerier) SetClusterDecommissionForce(context.Context, uuid.UUID) (sqlc.ClusterDecommission, error) {
	return sqlc.ClusterDecommission{}, nil
}
func (routeSecurityClusterQuerier) GetClusterHealthStatus(context.Context, uuid.UUID) (sqlc.ClusterHealthStatus, error) {
	return sqlc.ClusterHealthStatus{}, nil
}
func (routeSecurityClusterQuerier) ListClusterConditions(context.Context, uuid.UUID) ([]sqlc.ClusterCondition, error) {
	return nil, nil
}
func (routeSecurityClusterQuerier) CreateClusterRegistrationToken(context.Context, sqlc.CreateClusterRegistrationTokenParams) (sqlc.ClusterRegistrationToken, error) {
	return sqlc.ClusterRegistrationToken{}, nil
}
func (routeSecurityClusterQuerier) GetRegistrationTokenByToken(context.Context, string) (sqlc.ClusterRegistrationToken, error) {
	return sqlc.ClusterRegistrationToken{}, pgx.ErrNoRows
}
func (routeSecurityClusterQuerier) MarkRegistrationTokenUsed(context.Context, uuid.UUID) error {
	return nil
}
func (routeSecurityClusterQuerier) SetClusterAgentTokenRotationPending(context.Context, uuid.UUID) (int64, error) {
	return 0, nil
}
func (routeSecurityClusterQuerier) RevokeClusterAgentToken(context.Context, uuid.UUID) (int64, error) {
	return 0, nil
}
func (routeSecurityClusterQuerier) GetClusterRegistryConfig(context.Context, uuid.UUID) (sqlc.ClusterRegistryConfig, error) {
	return sqlc.ClusterRegistryConfig{}, pgx.ErrNoRows
}
func (routeSecurityClusterQuerier) UpsertClusterRegistryConfig(context.Context, sqlc.UpsertClusterRegistryConfigParams) (sqlc.ClusterRegistryConfig, error) {
	return sqlc.ClusterRegistryConfig{}, nil
}
func (routeSecurityClusterQuerier) DeleteClusterRegistryConfig(context.Context, uuid.UUID) error {
	return nil
}
func (routeSecurityClusterQuerier) GetPlatformConfig(context.Context) (sqlc.PlatformConfiguration, error) {
	return sqlc.PlatformConfiguration{}, nil
}
func (routeSecurityClusterQuerier) GetClusterTemplateByID(context.Context, uuid.UUID) (sqlc.ClusterTemplate, error) {
	return sqlc.ClusterTemplate{}, pgx.ErrNoRows
}
func (routeSecurityClusterQuerier) UpsertClusterTemplateApplication(context.Context, sqlc.UpsertClusterTemplateApplicationParams) (sqlc.ClusterTemplateApplication, error) {
	return sqlc.ClusterTemplateApplication{}, nil
}
func (routeSecurityClusterQuerier) GetPlatformSetting(context.Context, string) (sqlc.PlatformSetting, error) {
	return sqlc.PlatformSetting{}, pgx.ErrNoRows
}
func (routeSecurityClusterQuerier) ListArgoCDManagedClustersByCluster(context.Context, uuid.UUID) ([]sqlc.ArgocdManagedCluster, error) {
	return nil, nil
}
func (routeSecurityClusterQuerier) ListArgoCDApplicationsByManagedClusterTargets(context.Context, sqlc.ListArgoCDApplicationsByManagedClusterTargetsParams) ([]sqlc.ArgocdApplication, error) {
	return nil, nil
}
func (routeSecurityClusterQuerier) ListClusterConditionRemediationByCluster(context.Context, uuid.UUID) ([]sqlc.ClusterConditionRemediationAttempt, error) {
	return nil, nil
}

type routeSecuritySCIMTokenQuerier struct{}

func (routeSecuritySCIMTokenQuerier) GetUserByID(context.Context, uuid.UUID) (sqlc.User, error) {
	return sqlc.User{}, pgx.ErrNoRows
}
func (routeSecuritySCIMTokenQuerier) CreateSCIMToken(context.Context, sqlc.CreateSCIMTokenParams) (sqlc.ScimToken, error) {
	return sqlc.ScimToken{}, nil
}
func (routeSecuritySCIMTokenQuerier) ListSCIMTokens(context.Context) ([]sqlc.ScimToken, error) {
	return nil, nil
}
func (routeSecuritySCIMTokenQuerier) DeleteSCIMToken(context.Context, uuid.UUID) error {
	return nil
}

type routeSecurityShellQuerier struct{}

func (routeSecurityShellQuerier) GetUserByID(context.Context, uuid.UUID) (sqlc.User, error) {
	return sqlc.User{}, pgx.ErrNoRows
}
func (routeSecurityShellQuerier) GetClusterByID(context.Context, uuid.UUID) (sqlc.Cluster, error) {
	return sqlc.Cluster{}, pgx.ErrNoRows
}
func (routeSecurityShellQuerier) CreateKubectlSession(context.Context, sqlc.CreateKubectlSessionParams) (sqlc.KubectlSession, error) {
	return sqlc.KubectlSession{}, nil
}
func (routeSecurityShellQuerier) GetKubectlSessionByID(context.Context, uuid.UUID) (sqlc.KubectlSession, error) {
	return sqlc.KubectlSession{}, pgx.ErrNoRows
}
func (routeSecurityShellQuerier) ListActiveKubectlSessionsByCluster(context.Context, uuid.UUID) ([]sqlc.KubectlSession, error) {
	return nil, nil
}
func (routeSecurityShellQuerier) ListAllActiveKubectlSessions(context.Context) ([]sqlc.KubectlSession, error) {
	return nil, nil
}
func (routeSecurityShellQuerier) ListExpiredKubectlSessions(context.Context) ([]sqlc.KubectlSession, error) {
	return nil, nil
}
func (routeSecurityShellQuerier) SetKubectlSessionStatus(context.Context, sqlc.SetKubectlSessionStatusParams) error {
	return nil
}
func (routeSecurityShellQuerier) TouchKubectlSessionInput(context.Context, uuid.UUID) error {
	return nil
}
func (routeSecurityShellQuerier) InsertKubectlSessionCommand(context.Context, sqlc.InsertKubectlSessionCommandParams) error {
	return nil
}
func (routeSecurityShellQuerier) ListKubectlSessionCommands(context.Context, sqlc.ListKubectlSessionCommandsParams) ([]sqlc.KubectlSessionCommand, error) {
	return nil, nil
}
func (routeSecurityShellQuerier) CountKubectlSessionCommands(context.Context, uuid.UUID) (int64, error) {
	return 0, nil
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
	router, clusterID := newRouteSecurityRouter(t)
	routeRegistry := loadSecuritySensitiveRouteRegistry(t, clusterID)
	for _, entry := range routeRegistry {
		t.Run(entry.ID, func(t *testing.T) {
			req := httptest.NewRequest(methodForRoute(entry.Method), entry.SamplePath, nil)
			req.Header.Set("Accept", "application/json")
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != entry.ExpectedUnauthenticatedStatus {
				t.Fatalf("%s %s unauthenticated status = %d, want %d; body=%s", entry.Method, entry.Route, rec.Code, entry.ExpectedUnauthenticatedStatus, rec.Body.String())
			}
		})
	}
}

func TestRouteInventoryCanBeGenerated(t *testing.T) {
	router, _ := newRouteSecurityRouter(t)
	entries := collectRouteInventory(t, router)
	if len(entries) < 100 {
		t.Fatalf("route inventory entries = %d, want at least 100", len(entries))
	}
	metadataEntries := 0
	for _, entry := range entries {
		if entry.SecurityRegistryID != "" || entry.RiskClassification != "" {
			metadataEntries++
		}
	}
	if metadataEntries < 190 {
		t.Fatalf("route inventory security metadata entries = %d, want at least 190", metadataEntries)
	}
	assertRouteInventoryEntriesHaveCompleteSecurityPosture(t, entries)
	for _, expected := range []struct {
		method             string
		pattern            string
		securityRegistryID string
	}{
		{method: http.MethodGet, pattern: "/api/v1/audit/", securityRegistryID: "audit-log-list"},
		{method: http.MethodGet, pattern: "/api/v1/audit/export/", securityRegistryID: "audit-log-export"},
		{method: http.MethodGet, pattern: "/api/v1/audit/{id}/", securityRegistryID: "audit-log-detail"},
		{method: http.MethodGet, pattern: "/api/v1/settings/audit-logs/", securityRegistryID: "legacy-settings-audit-log-list"},
	} {
		if !routeInventoryHasSecurityRegistryID(entries, expected.method, expected.pattern, expected.securityRegistryID) {
			t.Fatalf("route inventory missing security metadata %s for %s %s", expected.securityRegistryID, expected.method, expected.pattern)
		}
	}
	t.Logf("route inventory entries = %d", len(entries))
	if os.Getenv("ASTRONOMER_WRITE_ROUTE_INVENTORY") != "1" {
		return
	}
	path := filepath.Join("..", "..", "docs", "generated-route-inventory.json")
	raw, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		t.Fatalf("marshal route inventory: %v", err)
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write route inventory: %v", err)
	}
	t.Logf("wrote %d route inventory entries to %s", len(entries), path)
}

func TestRouteInventoryEntriesHaveCompleteSecurityPosture(t *testing.T) {
	router, _ := newRouteSecurityRouter(t)
	assertRouteInventoryEntriesHaveCompleteSecurityPosture(t, collectRouteInventory(t, router))
}

func TestForwardingRoutesAreDocumentedInProxyInventory(t *testing.T) {
	router, _ := newRouteSecurityRouter(t)
	entries := collectRouteInventory(t, router)
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "kubernetes-proxy-inventory.md"))
	if err != nil {
		t.Fatalf("read kubernetes proxy inventory: %v", err)
	}
	doc := string(raw)

	var missing []string
	seen := map[string]bool{}
	for _, entry := range entries {
		pattern := normalizeRoutePattern(entry.Pattern)
		if !routeRequiresProxyInventory(pattern) {
			continue
		}
		key := entry.Method + " " + pattern
		if seen[key] {
			continue
		}
		seen[key] = true
		if proxyInventoryContainsPattern(doc, pattern) {
			continue
		}
		missing = append(missing, key)
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("forwarding routes missing from docs/kubernetes-proxy-inventory.md:\n%s", strings.Join(missing, "\n"))
	}
}

func TestMutatingRoutesHaveSecurityClassification(t *testing.T) {
	router, _ := newRouteSecurityRouter(t)
	inventory := collectRouteInventory(t, router)
	registry := loadSecuritySensitiveRouteRegistry(t, uuid.NewString())
	classifications := loadRouteRiskClassifications(t)

	var missing []string
	for _, route := range inventory {
		if !isMutatingHTTPMethod(route.Method) {
			continue
		}
		pattern := normalizeRoutePattern(route.Pattern)
		if securityRegistryCoversRoute(registry, route.Method, pattern) {
			continue
		}
		if routeRiskClassified(classifications, route.Method, pattern) {
			continue
		}
		missing = append(missing, route.Method+" "+pattern)
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("mutating routes missing from security registry or route-risk classifications:\n%s", strings.Join(missing, "\n"))
	}
}

func TestBrowserCookieMutatingRoutesRequireCSRF(t *testing.T) {
	router, clusterID := newRouteSecurityRouter(t)
	jwtMgr := auth.NewJWTManager("route-security-test-secret", 60)
	token, err := jwtMgr.GenerateAccessToken(uuid.New())
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}

	entries := loadSecuritySensitiveRouteRegistry(t, clusterID)
	checked := 0
	for _, entry := range entries {
		if !routeRequiresBrowserCSRF(entry) {
			continue
		}
		method := methodForCSRFSample(entry.Method)
		if !isMutatingHTTPMethod(method) {
			continue
		}
		checked++
		t.Run(entry.ID, func(t *testing.T) {
			req := httptest.NewRequest(method, entry.SamplePath, nil)
			req.AddCookie(&http.Cookie{Name: appmiddleware.SessionCookieName, Value: token})
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("%s %s status = %d, want %d; body=%s", method, entry.SamplePath, rec.Code, http.StatusUnauthorized, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), "csrf_required") {
				t.Fatalf("%s %s body = %s, want csrf_required", method, entry.SamplePath, rec.Body.String())
			}
		})
	}
	if checked < 100 {
		t.Fatalf("CSRF-checked route count = %d, want at least 100", checked)
	}
}

func TestUserManagementRoutesRequireUsersRBACAndAdminScope(t *testing.T) {
	jwtMgr := auth.NewJWTManager("route-security-test-secret", 60)
	userID := uuid.New()
	token, err := jwtMgr.GenerateAccessToken(userID)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}

	noUsersRBAC := NewRouter(&config.Config{}, RouterDependencies{
		JWT:         jwtMgr,
		RBACEngine:  rbac.NewEngine(),
		RBACQueries: routeSecurityRBACQuerier{bindings: routeSecurityReadOnlyBindings()},
		Resources:   handler.NewResourceHandler(),
	})
	deniedReq := httptest.NewRequest(http.MethodPost, "/api/v1/users/", nil)
	deniedReq.Header.Set("Authorization", "Bearer "+token)
	deniedRec := httptest.NewRecorder()
	noUsersRBAC.ServeHTTP(deniedRec, deniedReq)
	if deniedRec.Code != http.StatusForbidden {
		t.Fatalf("no users RBAC status = %d, want %d; body=%s", deniedRec.Code, http.StatusForbidden, deniedRec.Body.String())
	}

	usersRBAC := NewRouter(&config.Config{}, RouterDependencies{
		JWT:         jwtMgr,
		RBACEngine:  rbac.NewEngine(),
		RBACQueries: routeSecurityRBACQuerier{bindings: routeSecurityUserBindings(rbac.VerbCreate)},
		Resources:   handler.NewResourceHandler(),
	})
	allowedReq := httptest.NewRequest(http.MethodPost, "/api/v1/users/", nil)
	allowedReq.Header.Set("Authorization", "Bearer "+token)
	allowedRec := httptest.NewRecorder()
	usersRBAC.ServeHTTP(allowedRec, allowedReq)
	if allowedRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("users RBAC status = %d, want handler %d; body=%s", allowedRec.Code, http.StatusServiceUnavailable, allowedRec.Body.String())
	}

	rawToken := "astro_user_route_scope_test"
	scopedAPIRouter := NewRouter(&config.Config{}, RouterDependencies{
		JWT: jwtMgr,
		AuthQueries: routeSecurityAPITokenQuerier(
			rawToken,
			userID,
			json.RawMessage(`["read"]`),
		),
		RBACEngine:  rbac.NewEngine(),
		RBACQueries: routeSecurityRBACQuerier{bindings: routeSecurityUserBindings(rbac.VerbCreate)},
		Resources:   handler.NewResourceHandler(),
	})
	scopeReq := httptest.NewRequest(http.MethodPost, "/api/v1/users/", nil)
	scopeReq.Header.Set("Authorization", "Bearer "+rawToken)
	scopeRec := httptest.NewRecorder()
	scopedAPIRouter.ServeHTTP(scopeRec, scopeReq)
	if scopeRec.Code != http.StatusForbidden {
		t.Fatalf("non-admin API token status = %d, want %d; body=%s", scopeRec.Code, http.StatusForbidden, scopeRec.Body.String())
	}
	if !strings.Contains(scopeRec.Body.String(), "scope_denied") {
		t.Fatalf("non-admin API token body = %s, want scope_denied", scopeRec.Body.String())
	}
}

// TestManifestEndpointRequiresWriteScope (D3 / H3): GET /clusters/{id}/manifest/
// mints a live registration token, so it must require the write-clusters scope
// just like POST /register/ — a read-only API token must be denied (scope_denied)
// before any token is minted. FAILS WITHOUT THE FIX (route was VerbRead/no scope).
func TestManifestEndpointRequiresWriteScope(t *testing.T) {
	jwtMgr := auth.NewJWTManager("route-security-test-secret", 60)
	userID := uuid.New()
	rawToken := "astro_user_manifest_scope_test"
	router := NewRouter(&config.Config{}, RouterDependencies{
		JWT: jwtMgr,
		AuthQueries: routeSecurityAPITokenQuerier(
			rawToken,
			userID,
			json.RawMessage(`["read"]`),
		),
		RBACEngine:  rbac.NewEngine(),
		RBACQueries: routeSecurityRBACQuerier{bindings: routeSecurityBindings(rbac.ResourceClusters, rbac.VerbUpdate)},
		Clusters:    handler.NewClusterHandler(nil),
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/"+uuid.New().String()+"/manifest/", nil)
	req.Header.Set("Authorization", "Bearer "+rawToken)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("read-only token on /manifest/ status = %d, want %d (write scope required); body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "scope_denied") {
		t.Fatalf("/manifest/ deny body = %s, want scope_denied", rec.Body.String())
	}
}

func TestLegacySettingsMutatorsRequireRBACAndAdminScope(t *testing.T) {
	jwtMgr := auth.NewJWTManager("route-security-test-secret", 60)
	userID := uuid.New()
	token, err := jwtMgr.GenerateAccessToken(userID)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}

	noSettingsRBAC := NewRouter(&config.Config{}, RouterDependencies{
		JWT:         jwtMgr,
		RBACEngine:  rbac.NewEngine(),
		RBACQueries: routeSecurityRBACQuerier{bindings: routeSecurityReadOnlyBindings()},
		Resources:   handler.NewResourceHandler(),
	})
	settingsReq := httptest.NewRequest(http.MethodPut, "/api/v1/settings/general/", nil)
	settingsReq.Header.Set("Authorization", "Bearer "+token)
	settingsRec := httptest.NewRecorder()
	noSettingsRBAC.ServeHTTP(settingsRec, settingsReq)
	if settingsRec.Code != http.StatusForbidden {
		t.Fatalf("settings without RBAC status = %d, want %d; body=%s", settingsRec.Code, http.StatusForbidden, settingsRec.Body.String())
	}

	withSettingsRBAC := NewRouter(&config.Config{}, RouterDependencies{
		JWT:         jwtMgr,
		RBACEngine:  rbac.NewEngine(),
		RBACQueries: routeSecurityRBACQuerier{bindings: routeSecurityBindings(rbac.ResourceSettings, rbac.VerbUpdate)},
		Resources:   handler.NewResourceHandler(),
	})
	allowedReq := httptest.NewRequest(http.MethodPut, "/api/v1/settings/general/", strings.NewReader(`{}`))
	allowedReq.Header.Set("Authorization", "Bearer "+token)
	allowedRec := httptest.NewRecorder()
	withSettingsRBAC.ServeHTTP(allowedRec, allowedReq)
	if allowedRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("settings with RBAC status = %d, want handler %d; body=%s", allowedRec.Code, http.StatusServiceUnavailable, allowedRec.Body.String())
	}

	rawToken := "astro_settings_route_scope_test"
	scopedAPIRouter := NewRouter(&config.Config{}, RouterDependencies{
		JWT: jwtMgr,
		AuthQueries: routeSecurityAPITokenQuerier(
			rawToken,
			userID,
			json.RawMessage(`["read"]`),
		),
		RBACEngine:  rbac.NewEngine(),
		RBACQueries: routeSecurityRBACQuerier{bindings: routeSecurityBindings(rbac.ResourceSSO, rbac.VerbCreate)},
		Resources:   handler.NewResourceHandler(),
	})
	scopeReq := httptest.NewRequest(http.MethodPost, "/api/v1/settings/sso/", nil)
	scopeReq.Header.Set("Authorization", "Bearer "+rawToken)
	scopeRec := httptest.NewRecorder()
	scopedAPIRouter.ServeHTTP(scopeRec, scopeReq)
	if scopeRec.Code != http.StatusForbidden {
		t.Fatalf("settings SSO non-admin API token status = %d, want %d; body=%s", scopeRec.Code, http.StatusForbidden, scopeRec.Body.String())
	}
	if !strings.Contains(scopeRec.Body.String(), "scope_denied") {
		t.Fatalf("settings SSO non-admin API token body = %s, want scope_denied", scopeRec.Body.String())
	}
}

func TestAuditLogRoutesRequireAuditLogRBAC(t *testing.T) {
	jwtMgr := auth.NewJWTManager("route-security-test-secret", 60)
	userID := uuid.New()
	token, err := jwtMgr.GenerateAccessToken(userID)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	paths := []string{"/api/v1/audit/", "/api/v1/settings/audit-logs/"}

	noAuditRBAC := NewRouter(&config.Config{}, RouterDependencies{
		JWT:         jwtMgr,
		RBACEngine:  rbac.NewEngine(),
		RBACQueries: routeSecurityRBACQuerier{bindings: routeSecurityBindings(rbac.ResourceSettings, rbac.VerbRead)},
		Audit:       handler.NewAuditHandler(routeSecurityAuditReader{}),
		Resources:   handler.NewResourceHandler(),
	})
	for _, path := range paths {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		noAuditRBAC.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s without audit RBAC status = %d, want %d; body=%s", path, rec.Code, http.StatusForbidden, rec.Body.String())
		}
	}

	withAuditRBAC := NewRouter(&config.Config{}, RouterDependencies{
		JWT:         jwtMgr,
		RBACEngine:  rbac.NewEngine(),
		RBACQueries: routeSecurityRBACQuerier{bindings: routeSecurityBindings(rbac.ResourceAuditLogs, rbac.VerbRead)},
		Audit:       handler.NewAuditHandler(routeSecurityAuditReader{}),
		Resources:   handler.NewResourceHandler(),
	})
	for _, path := range paths {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		withAuditRBAC.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s with audit RBAC status = %d, want %d; body=%s", path, rec.Code, http.StatusOK, rec.Body.String())
		}
	}
}

func newRouteSecurityRouter(t *testing.T) (chi.Router, string) {
	t.Helper()
	jwtMgr := auth.NewJWTManager("route-security-test-secret", 60)
	clusterID := uuid.New()
	hub := tunnel.NewHub(slog.Default())
	execConsumer := tunnel.NewExecConsumer(hub, slog.Default())
	execConsumer.SetAuth(jwtMgr, nil)
	logsConsumer := tunnel.NewLogsConsumer(hub, slog.Default())
	logsConsumer.SetAuth(jwtMgr, nil)
	argoUIProxy, err := handler.NewArgoCDUIProxy("http://127.0.0.1:65535", slog.Default())
	if err != nil {
		t.Fatalf("create argocd ui proxy: %v", err)
	}
	shellHandler := handler.NewKubectlShellHandler(routeSecurityShellQuerier{}, nil, rbac.NewEngine(), kubectl.Deps{})
	shellHandler.SetStreamAuth(jwtMgr, nil)
	router := NewRouter(&config.Config{}, RouterDependencies{
		JWT:                 jwtMgr,
		RBACEngine:          rbac.NewEngine(),
		RBACQueries:         routeSecurityRBACQuerier{bindings: routeSecurityAdminBindings()},
		Clusters:            handler.NewClusterHandler(routeSecurityClusterQuerier{}),
		ClusterRegistration: handler.NewClusterRegistrationHandler(nil, events.NewBus()),
		Auth:                handler.NewAuthHandler(nil, jwtMgr),
		TOTP:                handler.NewTOTPHandler(nil, nil, nil, jwtMgr),
		Audit:               handler.NewAuditHandler(routeSecurityAuditReader{}),
		Resources:           handler.NewResourceHandler(),
		ResourcesSearch:     handler.NewResourcesSearchHandler(nil, nil),
		ClusterRegistries:   handler.NewClusterRegistriesHandler(nil),
		CloudCredentials:    handler.NewCloudCredentialHandler(nil),
		Vault:               handler.NewVaultHandler(nil),
		SMTP:                handler.NewSMTPHandler(nil, nil, nil),
		Webhooks:            handler.NewWebhookHandler(nil, nil, nil),
		DexConfig:           handler.NewDexHandler(nil),
		Projects:            handler.NewProjectHandler(nil),
		RBAC:                handler.NewRBACHandler(nil),
		Backups:             handler.NewBackupHandler(nil),
		ClusterSnapshots:    handler.NewClusterSnapshotsHandler(nil),
		Catalog:             handler.NewCatalogHandler(nil),
		ProjectCatalogs:     handler.NewProjectCatalogHandler(nil),
		ClusterGroups:       handler.NewClusterGroupHandler(nil),
		FleetOperations:     handler.NewFleetOperationHandler(nil),
		AgentFleet:          handler.NewAgentFleetHandler(nil),
		ApiserverAudit:      handler.NewApiserverAuditHandler(nil),
		ApiserverAllowlist:  handler.NewApiserverAllowlistHandler(nil),
		ClusterTemplates:    handler.NewClusterTemplateHandler(nil),
		NetworkPolicies:     handler.NewNetworkPolicyHandler(nil),
		Workloads:           handler.NewWorkloadHandler(),
		ServiceMesh:         handler.NewServiceMeshHandler(nil),
		Proxy:               tunnel.NewProxyHandler(hub, slog.Default()),
		ArgoCDProxyTokens:   &routeSecurityArgoTokenQuerier{clusterID: clusterID},
		ServiceProxy:        routeSecurityServiceProxy(),
		InternalK8s:         tunnel.NewInternalK8sHandler(hub, "route-security-psk", slog.Default()),
		InternalHelm:        tunnel.NewInternalHelmHandler(hub, "route-security-psk", slog.Default()),
		Exec:                execConsumer,
		Logs:                logsConsumer,
		RemoteServer:        tunnel2.NewRemoteServer(slog.Default(), nil),
		ArgoCDUIProxy:       argoUIProxy,
		KubectlShell:        shellHandler,
		SCIMTokenAdmin:      handler.NewSCIMTokenAdminHandler(routeSecuritySCIMTokenQuerier{}),
	})
	return router, clusterID.String()
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

func TestExecAndLogsRejectQueryJWT(t *testing.T) {
	jwtMgr := auth.NewJWTManager("route-security-test-secret", 60)
	userID := uuid.New()
	token, err := jwtMgr.GenerateAccessToken(userID)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	clusterID := uuid.New()
	hub := tunnel.NewHub(slog.Default())
	authQueries := routeSecurityTokenAuthQuerier{user: sqlc.User{ID: userID, IsActive: true}}
	execConsumer := tunnel.NewExecConsumer(hub, slog.Default())
	execConsumer.SetAuth(jwtMgr, authQueries)
	logsConsumer := tunnel.NewLogsConsumer(hub, slog.Default())
	logsConsumer.SetAuth(jwtMgr, authQueries)
	router := NewRouter(&config.Config{}, RouterDependencies{
		JWT:  jwtMgr,
		Exec: execConsumer,
		Logs: logsConsumer,
	})

	cases := []struct {
		name string
		path string
	}{
		{
			name: "exec",
			path: "/api/v1/ws/exec/" + clusterID.String() + "/default/example/shell/?token=" + token,
		},
		{
			name: "logs",
			path: "/api/v1/ws/logs/" + clusterID.String() + "/default/example/app/?token=" + token,
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

func TestDirectExecAndLogsStreamsAuditOpen(t *testing.T) {
	jwtMgr := auth.NewJWTManager("route-security-test-secret", 60)
	userID := uuid.New()
	token, err := jwtMgr.GenerateAccessToken(userID)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	clusterID := uuid.New()
	audit := &routeSecurityAuditWriter{}
	hub := tunnel.NewHub(slog.Default())
	execConsumer := tunnel.NewExecConsumer(hub, slog.Default())
	execConsumer.SetAuth(jwtMgr, nil)
	execConsumer.SetAuditWriter(audit)
	logsConsumer := tunnel.NewLogsConsumer(hub, slog.Default())
	logsConsumer.SetAuth(jwtMgr, nil)
	logsConsumer.SetAuditWriter(audit)
	router := NewRouter(&config.Config{}, RouterDependencies{
		JWT:  jwtMgr,
		Exec: execConsumer,
		Logs: logsConsumer,
	})
	server := httptest.NewServer(router)
	defer server.Close()

	dialWS := func(path string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+path, &websocket.DialOptions{
			HTTPHeader: http.Header{"Authorization": []string{"Bearer " + token}},
		})
		if err != nil {
			t.Fatalf("dial websocket %s: %v", path, err)
		}
		_ = conn.Close(websocket.StatusNormalClosure, "test complete")
	}

	dialWS("/api/v1/ws/exec/" + clusterID.String() + "/default/example/shell/")
	dialWS("/api/v1/ws/logs/" + clusterID.String() + "/default/example/app/")

	rows := waitForAuditRows(t, audit, 2)
	assertStreamAuditRow(t, findAuditRow(t, rows, "pod.exec.opened"), "pod.exec.opened", userID, clusterID, "default/example/shell", "exec")
	assertStreamAuditRow(t, findAuditRow(t, rows, "pod.logs.opened"), "pod.logs.opened", userID, clusterID, "default/example/app", "logs")
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

func TestK8sProxyMutationsRequireWritePermission(t *testing.T) {
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

func TestK8sProxyUsesCanonicalResourceRBAC(t *testing.T) {
	jwtMgr := auth.NewJWTManager("route-security-test-secret", 60)
	userID := uuid.New()
	token, err := jwtMgr.GenerateAccessToken(userID)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	clusterID := uuid.New()

	tests := []struct {
		name        string
		method      string
		path        string
		deniedRules []rbac.RoleBinding
		allowRules  []rbac.RoleBinding
	}{
		{
			name:        "pod list requires pods list",
			method:      http.MethodGet,
			path:        "/api/v1/clusters/" + clusterID.String() + "/k8s/api/v1/pods",
			deniedRules: routeSecurityBindings(rbac.ResourceClusters, rbac.VerbRead),
			allowRules:  routeSecurityBindings(rbac.ResourcePods, rbac.VerbList),
		},
		{
			name:        "service read requires services read",
			method:      http.MethodGet,
			path:        "/api/v1/clusters/" + clusterID.String() + "/k8s/api/v1/namespaces/default/services/web",
			deniedRules: routeSecurityBindings(rbac.ResourceClusters, rbac.VerbRead),
			allowRules:  routeSecurityBindings(rbac.ResourceServices, rbac.VerbRead),
		},
		{
			name:        "service create requires services create",
			method:      http.MethodPost,
			path:        "/api/v1/clusters/" + clusterID.String() + "/k8s/api/v1/namespaces/default/services",
			deniedRules: routeSecurityBindings(rbac.ResourceClusters, rbac.VerbUpdate),
			allowRules:  routeSecurityBindings(rbac.ResourceServices, rbac.VerbCreate),
		},
		{
			name:        "ingress update requires ingresses update",
			method:      http.MethodPut,
			path:        "/api/v1/clusters/" + clusterID.String() + "/k8s/apis/networking.k8s.io/v1/namespaces/default/ingresses/web",
			deniedRules: routeSecurityBindings(rbac.ResourceClusters, rbac.VerbUpdate),
			allowRules:  routeSecurityBindings(rbac.ResourceIngresses, rbac.VerbUpdate),
		},
		{
			name:        "pvc delete requires storage delete",
			method:      http.MethodDelete,
			path:        "/api/v1/clusters/" + clusterID.String() + "/k8s/api/v1/namespaces/default/persistentvolumeclaims/data",
			deniedRules: routeSecurityBindings(rbac.ResourceClusters, rbac.VerbUpdate),
			allowRules:  routeSecurityBindings(rbac.ResourceStorage, rbac.VerbDelete),
		},
		{
			name:        "pod log subresource requires pods logs",
			method:      http.MethodGet,
			path:        "/api/v1/clusters/" + clusterID.String() + "/k8s/api/v1/namespaces/default/pods/app-0/log",
			deniedRules: routeSecurityBindings(rbac.ResourcePods, rbac.VerbRead),
			allowRules:  routeSecurityBindings(rbac.ResourcePods, rbac.VerbLogs),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deniedRouter := NewRouter(&config.Config{}, RouterDependencies{
				JWT:         jwtMgr,
				RBACEngine:  rbac.NewEngine(),
				RBACQueries: routeSecurityRBACQuerier{bindings: tt.deniedRules},
				Proxy:       tunnel.NewProxyHandler(tunnel.NewHub(slog.Default()), slog.Default()),
			})
			deniedReq := httptest.NewRequest(tt.method, tt.path, nil)
			deniedReq.Header.Set("Authorization", "Bearer "+token)
			deniedRec := httptest.NewRecorder()
			deniedRouter.ServeHTTP(deniedRec, deniedReq)
			if deniedRec.Code != http.StatusForbidden {
				t.Fatalf("denied status = %d, want %d; body=%s", deniedRec.Code, http.StatusForbidden, deniedRec.Body.String())
			}

			allowedRouter := NewRouter(&config.Config{}, RouterDependencies{
				JWT:         jwtMgr,
				RBACEngine:  rbac.NewEngine(),
				RBACQueries: routeSecurityRBACQuerier{bindings: tt.allowRules},
				Proxy:       tunnel.NewProxyHandler(tunnel.NewHub(slog.Default()), slog.Default()),
			})
			allowedReq := httptest.NewRequest(tt.method, tt.path, nil)
			allowedReq.Header.Set("Authorization", "Bearer "+token)
			allowedRec := httptest.NewRecorder()
			allowedRouter.ServeHTTP(allowedRec, allowedReq)
			if allowedRec.Code != http.StatusServiceUnavailable {
				t.Fatalf("allowed status = %d, want proxy handler %d; body=%s", allowedRec.Code, http.StatusServiceUnavailable, allowedRec.Body.String())
			}
		})
	}
}

func TestK8sProxySecretReadsRequireSecretsRBACAndAudit(t *testing.T) {
	jwtMgr := auth.NewJWTManager("route-security-test-secret", 60)
	userID := uuid.New()
	token, err := jwtMgr.GenerateAccessToken(userID)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	clusterID := uuid.New()

	podListRouter := NewRouter(&config.Config{}, RouterDependencies{
		JWT:         jwtMgr,
		RBACEngine:  rbac.NewEngine(),
		RBACQueries: routeSecurityRBACQuerier{bindings: routeSecurityBindings(rbac.ResourcePods, rbac.VerbList)},
		Proxy:       tunnel.NewProxyHandler(tunnel.NewHub(slog.Default()), slog.Default()),
	})
	podReq := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/"+clusterID.String()+"/k8s/api/v1/pods", nil)
	podReq.Header.Set("Authorization", "Bearer "+token)
	podRec := httptest.NewRecorder()
	podListRouter.ServeHTTP(podRec, podReq)
	if podRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("pod list status = %d, want proxy handler %d; body=%s", podRec.Code, http.StatusServiceUnavailable, podRec.Body.String())
	}

	clusterReadRouter := NewRouter(&config.Config{}, RouterDependencies{
		JWT:         jwtMgr,
		RBACEngine:  rbac.NewEngine(),
		RBACQueries: routeSecurityRBACQuerier{bindings: routeSecurityBindings(rbac.ResourceClusters, rbac.VerbRead)},
		Proxy:       tunnel.NewProxyHandler(tunnel.NewHub(slog.Default()), slog.Default()),
	})
	secretReq := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/"+clusterID.String()+"/k8s/api/v1/namespaces/default/secrets/db-password", nil)
	secretReq.Header.Set("Authorization", "Bearer "+token)
	secretRec := httptest.NewRecorder()
	clusterReadRouter.ServeHTTP(secretRec, secretReq)
	if secretRec.Code != http.StatusForbidden {
		t.Fatalf("cluster-read secret status = %d, want %d; body=%s", secretRec.Code, http.StatusForbidden, secretRec.Body.String())
	}

	secretReadAudit := &routeSecurityAuditWriter{}
	secretReadRouter := NewRouter(&config.Config{}, RouterDependencies{
		JWT:         jwtMgr,
		RBACEngine:  rbac.NewEngine(),
		RBACQueries: routeSecurityRBACQuerier{bindings: routeSecurityBindings(rbac.ResourceSecrets, rbac.VerbRead)},
		AuditWriter: secretReadAudit,
		Proxy:       tunnel.NewProxyHandler(tunnel.NewHub(slog.Default()), slog.Default()),
	})
	namedReq := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/"+clusterID.String()+"/k8s/api/v1/namespaces/default/secrets/db-password", nil)
	namedReq.Header.Set("Authorization", "Bearer "+token)
	namedRec := httptest.NewRecorder()
	secretReadRouter.ServeHTTP(namedRec, namedReq)
	if namedRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("secret read status = %d, want proxy handler %d; body=%s", namedRec.Code, http.StatusServiceUnavailable, namedRec.Body.String())
	}
	rows := waitForAuditRows(t, secretReadAudit, 1)
	row := findAuditRow(t, rows, "cluster.secret.read")
	if row.ResourceType != "cluster" || row.ResourceID != clusterID.String() || row.ResourceName != "default/db-password" {
		t.Fatalf("secret audit resource = %q/%q/%q, want cluster/%s/default/db-password", row.ResourceType, row.ResourceID, row.ResourceName, clusterID.String())
	}
	var detail map[string]any
	if err := json.Unmarshal(row.Detail, &detail); err != nil {
		t.Fatalf("unmarshal secret audit detail: %v", err)
	}
	if detail["method"] != http.MethodGet ||
		detail["k8s_path"] != "/api/v1/namespaces/default/secrets/db-password" ||
		detail["verb"] != "read" ||
		detail["namespace"] != "default" ||
		detail["resource"] != "secrets" ||
		detail["name"] != "db-password" {
		t.Fatalf("secret audit detail = %#v", detail)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/"+clusterID.String()+"/k8s/api/v1/namespaces/default/secrets", nil)
	listReq.Header.Set("Authorization", "Bearer "+token)
	listRec := httptest.NewRecorder()
	secretReadRouter.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusForbidden {
		t.Fatalf("secret list with read-only status = %d, want %d; body=%s", listRec.Code, http.StatusForbidden, listRec.Body.String())
	}

	secretListRouter := NewRouter(&config.Config{}, RouterDependencies{
		JWT:         jwtMgr,
		RBACEngine:  rbac.NewEngine(),
		RBACQueries: routeSecurityRBACQuerier{bindings: routeSecurityBindings(rbac.ResourceSecrets, rbac.VerbList)},
		Proxy:       tunnel.NewProxyHandler(tunnel.NewHub(slog.Default()), slog.Default()),
	})
	allowedListReq := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/"+clusterID.String()+"/k8s/api/v1/namespaces/default/secrets", nil)
	allowedListReq.Header.Set("Authorization", "Bearer "+token)
	allowedListRec := httptest.NewRecorder()
	secretListRouter.ServeHTTP(allowedListRec, allowedListReq)
	if allowedListRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("secret list status = %d, want proxy handler %d; body=%s", allowedListRec.Code, http.StatusServiceUnavailable, allowedListRec.Body.String())
	}

	secretWatchRouter := NewRouter(&config.Config{}, RouterDependencies{
		JWT:         jwtMgr,
		RBACEngine:  rbac.NewEngine(),
		RBACQueries: routeSecurityRBACQuerier{bindings: routeSecurityBindings(rbac.ResourceSecrets, rbac.VerbList)},
		Proxy:       tunnel.NewProxyHandler(tunnel.NewHub(slog.Default()), slog.Default()),
	})
	watchReq := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/"+clusterID.String()+"/k8s/api/v1/namespaces/default/secrets?watch=true", nil)
	watchReq.Header.Set("Authorization", "Bearer "+token)
	watchRec := httptest.NewRecorder()
	secretWatchRouter.ServeHTTP(watchRec, watchReq)
	if watchRec.Code != http.StatusForbidden {
		t.Fatalf("secret watch with list-only status = %d, want %d; body=%s", watchRec.Code, http.StatusForbidden, watchRec.Body.String())
	}
}

func TestGenericSecretResourcesRequireSecretsListAndAudit(t *testing.T) {
	jwtMgr := auth.NewJWTManager("route-security-test-secret", 60)
	userID := uuid.New()
	token, err := jwtMgr.GenerateAccessToken(userID)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	clusterID := uuid.New()
	resourceHandler := handler.NewResourceHandlerWithRequester(routeSecurityGenericResourceRequester{})
	path := "/api/v1/clusters/" + clusterID.String() + "/resources/generic/secrets/?namespace=default"

	clusterReadRouter := NewRouter(&config.Config{}, RouterDependencies{
		JWT:         jwtMgr,
		RBACEngine:  rbac.NewEngine(),
		RBACQueries: routeSecurityRBACQuerier{bindings: routeSecurityBindings(rbac.ResourceClusters, rbac.VerbRead)},
		Resources:   resourceHandler,
	})
	deniedReq := httptest.NewRequest(http.MethodGet, path, nil)
	deniedReq.Header.Set("Authorization", "Bearer "+token)
	deniedRec := httptest.NewRecorder()
	clusterReadRouter.ServeHTTP(deniedRec, deniedReq)
	if deniedRec.Code != http.StatusForbidden {
		t.Fatalf("cluster-read secret list status = %d, want %d; body=%s", deniedRec.Code, http.StatusForbidden, deniedRec.Body.String())
	}

	audit := &routeSecurityAuditWriter{}
	secretListRouter := NewRouter(&config.Config{}, RouterDependencies{
		JWT:         jwtMgr,
		RBACEngine:  rbac.NewEngine(),
		RBACQueries: routeSecurityRBACQuerier{bindings: routeSecurityBindings(rbac.ResourceSecrets, rbac.VerbList)},
		AuditWriter: audit,
		Resources:   resourceHandler,
	})
	allowedReq := httptest.NewRequest(http.MethodGet, path, nil)
	allowedReq.Header.Set("Authorization", "Bearer "+token)
	allowedRec := httptest.NewRecorder()
	secretListRouter.ServeHTTP(allowedRec, allowedReq)
	if allowedRec.Code != http.StatusOK {
		t.Fatalf("secret list status = %d, want %d; body=%s", allowedRec.Code, http.StatusOK, allowedRec.Body.String())
	}
	row := findAuditRow(t, waitForAuditRows(t, audit, 1), "cluster.secret.read")
	if row.ResourceType != "cluster" || row.ResourceID != clusterID.String() || row.ResourceName != "default/secrets" {
		t.Fatalf("secret list audit resource = %q/%q/%q, want cluster/%s/default/secrets", row.ResourceType, row.ResourceID, row.ResourceName, clusterID.String())
	}
	var detail map[string]any
	if err := json.Unmarshal(row.Detail, &detail); err != nil {
		t.Fatalf("unmarshal audit detail: %v", err)
	}
	if detail["scope"] != "generic_resource_list" ||
		detail["resource_type"] != "secrets" ||
		detail["verb"] != "list" ||
		detail["namespace"] != "default" {
		t.Fatalf("secret list audit detail = %#v", detail)
	}
}

func TestGenericResourceListsRequireCanonicalRBAC(t *testing.T) {
	jwtMgr := auth.NewJWTManager("route-security-test-secret", 60)
	userID := uuid.New()
	token, err := jwtMgr.GenerateAccessToken(userID)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	clusterID := uuid.New()
	resourceHandler := handler.NewResourceHandlerWithRequester(routeSecurityGenericResourceRequester{})

	tests := []struct {
		name          string
		path          string
		deniedRules   []rbac.RoleBinding
		allowResource rbac.Resource
	}{
		{
			name:          "generic configmap list requires configmaps list",
			path:          "/api/v1/clusters/" + clusterID.String() + "/resources/generic/configmaps/?namespace=default",
			deniedRules:   routeSecurityBindings(rbac.ResourceClusters, rbac.VerbRead),
			allowResource: rbac.ResourceConfigMaps,
		},
		{
			name:          "generic service list requires services list",
			path:          "/api/v1/clusters/" + clusterID.String() + "/resources/generic/services/?namespace=default",
			deniedRules:   routeSecurityBindings(rbac.ResourceWorkloads, rbac.VerbList),
			allowResource: rbac.ResourceServices,
		},
		{
			name:          "generic storage list requires storage list",
			path:          "/api/v1/clusters/" + clusterID.String() + "/resources/generic/persistentvolumeclaims/?namespace=default",
			deniedRules:   routeSecurityBindings(rbac.ResourceWorkloads, rbac.VerbList),
			allowResource: rbac.ResourceStorage,
		},
		{
			name:          "generic networkpolicy list requires network policies list",
			path:          "/api/v1/clusters/" + clusterID.String() + "/resources/generic/networkpolicies/?namespace=default",
			deniedRules:   routeSecurityBindings(rbac.ResourceWorkloads, rbac.VerbList),
			allowResource: rbac.ResourceNetworkPolicies,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deniedRouter := NewRouter(&config.Config{}, RouterDependencies{
				JWT:         jwtMgr,
				RBACEngine:  rbac.NewEngine(),
				RBACQueries: routeSecurityRBACQuerier{bindings: tt.deniedRules},
				Resources:   resourceHandler,
			})
			deniedReq := httptest.NewRequest(http.MethodGet, tt.path, nil)
			deniedReq.Header.Set("Authorization", "Bearer "+token)
			deniedRec := httptest.NewRecorder()
			deniedRouter.ServeHTTP(deniedRec, deniedReq)
			if deniedRec.Code != http.StatusForbidden {
				t.Fatalf("denied status = %d, want %d; body=%s", deniedRec.Code, http.StatusForbidden, deniedRec.Body.String())
			}

			allowedRouter := NewRouter(&config.Config{}, RouterDependencies{
				JWT:         jwtMgr,
				RBACEngine:  rbac.NewEngine(),
				RBACQueries: routeSecurityRBACQuerier{bindings: routeSecurityBindings(tt.allowResource, rbac.VerbList)},
				Resources:   resourceHandler,
			})
			allowedReq := httptest.NewRequest(http.MethodGet, tt.path, nil)
			allowedReq.Header.Set("Authorization", "Bearer "+token)
			allowedRec := httptest.NewRecorder()
			allowedRouter.ServeHTTP(allowedRec, allowedReq)
			if allowedRec.Code != http.StatusOK {
				t.Fatalf("allowed status = %d, want %d; body=%s", allowedRec.Code, http.StatusOK, allowedRec.Body.String())
			}
		})
	}
}

func TestNamespaceScopedBindingsAreEnforcedOnGenericResourceRoutes(t *testing.T) {
	jwtMgr := auth.NewJWTManager("route-security-test-secret", 60)
	userID := uuid.New()
	token, err := jwtMgr.GenerateAccessToken(userID)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	clusterID := uuid.New()

	tests := []struct {
		name     string
		resource rbac.Resource
		path     string
	}{
		{
			name:     "configmap list",
			resource: rbac.ResourceConfigMaps,
			path:     "/api/v1/clusters/" + clusterID.String() + "/resources/generic/configmaps/",
		},
		{
			name:     "secret list",
			resource: rbac.ResourceSecrets,
			path:     "/api/v1/clusters/" + clusterID.String() + "/resources/generic/secrets/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := NewRouter(&config.Config{}, RouterDependencies{
				JWT:        jwtMgr,
				RBACEngine: rbac.NewEngine(),
				RBACQueries: routeSecurityRBACQuerier{bindings: []rbac.RoleBinding{{
					ClusterID: clusterID.String(),
					Namespace: "payments",
					RoleRules: []rbac.Rule{{
						Resource: string(tt.resource),
						Verbs:    []string{string(rbac.VerbList)},
					}},
				}}},
				Resources: handler.NewResourceHandlerWithRequester(routeSecurityGenericResourceRequester{}),
			})

			allowedReq := httptest.NewRequest(http.MethodGet, tt.path+"?namespace=payments", nil)
			allowedReq.Header.Set("Authorization", "Bearer "+token)
			allowedRec := httptest.NewRecorder()
			router.ServeHTTP(allowedRec, allowedReq)
			if allowedRec.Code != http.StatusOK {
				t.Fatalf("matching namespace status = %d, want %d; body=%s", allowedRec.Code, http.StatusOK, allowedRec.Body.String())
			}

			for _, suffix := range []string{"?namespace=default", ""} {
				deniedReq := httptest.NewRequest(http.MethodGet, tt.path+suffix, nil)
				deniedReq.Header.Set("Authorization", "Bearer "+token)
				deniedRec := httptest.NewRecorder()
				router.ServeHTTP(deniedRec, deniedReq)
				if deniedRec.Code != http.StatusForbidden {
					t.Fatalf("suffix %q status = %d, want %d; body=%s", suffix, deniedRec.Code, http.StatusForbidden, deniedRec.Body.String())
				}
			}
		})
	}
}

func TestDynamicResourceListRequiresClusterRead(t *testing.T) {
	jwtMgr := auth.NewJWTManager("route-security-test-secret", 60)
	userID := uuid.New()
	token, err := jwtMgr.GenerateAccessToken(userID)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	clusterID := uuid.New()
	path := "/api/v1/clusters/" + clusterID.String() + "/resources/apps/v1/deployments/"

	deniedRouter := NewRouter(&config.Config{}, RouterDependencies{
		JWT:         jwtMgr,
		RBACEngine:  rbac.NewEngine(),
		RBACQueries: routeSecurityRBACQuerier{bindings: routeSecurityBindings(rbac.ResourceWorkloads, rbac.VerbList)},
		Resources:   handler.NewResourceHandlerWithRequester(routeSecurityGenericResourceRequester{}),
	})
	deniedReq := httptest.NewRequest(http.MethodGet, path, nil)
	deniedReq.Header.Set("Authorization", "Bearer "+token)
	deniedRec := httptest.NewRecorder()
	deniedRouter.ServeHTTP(deniedRec, deniedReq)
	if deniedRec.Code != http.StatusForbidden {
		t.Fatalf("workloads:list status = %d, want %d; body=%s", deniedRec.Code, http.StatusForbidden, deniedRec.Body.String())
	}

	allowedRouter := NewRouter(&config.Config{}, RouterDependencies{
		JWT:         jwtMgr,
		RBACEngine:  rbac.NewEngine(),
		RBACQueries: routeSecurityRBACQuerier{bindings: routeSecurityBindings(rbac.ResourceClusters, rbac.VerbRead)},
		Resources:   handler.NewResourceHandlerWithRequester(routeSecurityGenericResourceRequester{}),
	})
	allowedReq := httptest.NewRequest(http.MethodGet, path, nil)
	allowedReq.Header.Set("Authorization", "Bearer "+token)
	allowedRec := httptest.NewRecorder()
	allowedRouter.ServeHTTP(allowedRec, allowedReq)
	if allowedRec.Code != http.StatusOK {
		t.Fatalf("clusters:read status = %d, want %d; body=%s", allowedRec.Code, http.StatusOK, allowedRec.Body.String())
	}
}

func TestNamedResourceRoutesRequireCanonicalRBAC(t *testing.T) {
	jwtMgr := auth.NewJWTManager("route-security-test-secret", 60)
	userID := uuid.New()
	token, err := jwtMgr.GenerateAccessToken(userID)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	clusterID := uuid.New()

	tests := []struct {
		name        string
		method      string
		path        string
		body        string
		deniedRules []rbac.RoleBinding
		allowRules  []rbac.RoleBinding
	}{
		{
			name:        "service create requires services create",
			method:      http.MethodPost,
			path:        "/api/v1/clusters/" + clusterID.String() + "/resources/services/",
			body:        `{"metadata":{"namespace":"default","name":"svc"}}`,
			deniedRules: routeSecurityBindings(rbac.ResourceClusters, rbac.VerbUpdate),
			allowRules:  routeSecurityBindings(rbac.ResourceServices, rbac.VerbCreate),
		},
		{
			name:        "persistent volume delete requires storage delete",
			method:      http.MethodDelete,
			path:        "/api/v1/clusters/" + clusterID.String() + "/resources/persistentvolumes/pv-example/",
			deniedRules: routeSecurityBindings(rbac.ResourceClusters, rbac.VerbUpdate),
			allowRules:  routeSecurityBindings(rbac.ResourceStorage, rbac.VerbDelete),
		},
		{
			name:        "deployment rest update requires workloads update",
			method:      http.MethodPut,
			path:        "/api/v1/resources/" + clusterID.String() + "/deployments/default/example/",
			body:        `{"metadata":{"namespace":"default","name":"example"}}`,
			deniedRules: routeSecurityBindings(rbac.ResourceClusters, rbac.VerbUpdate),
			allowRules:  routeSecurityBindings(rbac.ResourceWorkloads, rbac.VerbUpdate),
		},
		{
			name:        "secret rest delete requires secrets delete",
			method:      http.MethodDelete,
			path:        "/api/v1/resources/" + clusterID.String() + "/secrets/default/example/",
			deniedRules: routeSecurityBindings(rbac.ResourceClusters, rbac.VerbUpdate),
			allowRules:  routeSecurityBindings(rbac.ResourceSecrets, rbac.VerbDelete),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deniedRouter := NewRouter(&config.Config{}, RouterDependencies{
				JWT:         jwtMgr,
				RBACEngine:  rbac.NewEngine(),
				RBACQueries: routeSecurityRBACQuerier{bindings: tt.deniedRules},
				Resources:   handler.NewResourceHandlerWithRequester(routeSecurityGenericResourceRequester{}),
			})
			deniedReq := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			deniedReq.Header.Set("Authorization", "Bearer "+token)
			deniedRec := httptest.NewRecorder()
			deniedRouter.ServeHTTP(deniedRec, deniedReq)
			if deniedRec.Code != http.StatusForbidden {
				t.Fatalf("denied status = %d, want %d; body=%s", deniedRec.Code, http.StatusForbidden, deniedRec.Body.String())
			}

			allowedRouter := NewRouter(&config.Config{}, RouterDependencies{
				JWT:         jwtMgr,
				RBACEngine:  rbac.NewEngine(),
				RBACQueries: routeSecurityRBACQuerier{bindings: tt.allowRules},
				Resources:   handler.NewResourceHandlerWithRequester(routeSecurityGenericResourceRequester{}),
			})
			allowedReq := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			allowedReq.Header.Set("Authorization", "Bearer "+token)
			allowedRec := httptest.NewRecorder()
			allowedRouter.ServeHTTP(allowedRec, allowedReq)
			if allowedRec.Code != http.StatusOK {
				t.Fatalf("allowed status = %d, want handler %d; body=%s", allowedRec.Code, http.StatusOK, allowedRec.Body.String())
			}
		})
	}
}

func TestNodeRoutesRequireNodeRBAC(t *testing.T) {
	jwtMgr := auth.NewJWTManager("route-security-test-secret", 60)
	userID := uuid.New()
	token, err := jwtMgr.GenerateAccessToken(userID)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	clusterID := uuid.New()

	tests := []struct {
		name          string
		method        string
		path          string
		deniedRules   []rbac.RoleBinding
		allowRules    []rbac.RoleBinding
		allowedStatus int
	}{
		{
			name:          "node list requires nodes list",
			method:        http.MethodGet,
			path:          "/api/v1/clusters/" + clusterID.String() + "/nodes/",
			deniedRules:   routeSecurityBindings(rbac.ResourceClusters, rbac.VerbRead),
			allowRules:    routeSecurityBindings(rbac.ResourceNodes, rbac.VerbList),
			allowedStatus: http.StatusOK,
		},
		{
			name:          "node detail requires nodes read",
			method:        http.MethodGet,
			path:          "/api/v1/clusters/" + clusterID.String() + "/nodes/node-1/",
			deniedRules:   routeSecurityBindings(rbac.ResourceClusters, rbac.VerbRead),
			allowRules:    routeSecurityBindings(rbac.ResourceNodes, rbac.VerbRead),
			allowedStatus: http.StatusOK,
		},
		{
			name:          "cordon requires nodes update",
			method:        http.MethodPost,
			path:          "/api/v1/nodes/" + clusterID.String() + "/node-1/cordon/",
			deniedRules:   routeSecurityBindings(rbac.ResourceClusters, rbac.VerbUpdate),
			allowRules:    routeSecurityBindings(rbac.ResourceNodes, rbac.VerbUpdate),
			allowedStatus: http.StatusOK,
		},
		{
			name:          "drain requires nodes manage",
			method:        http.MethodPost,
			path:          "/api/v1/nodes/" + clusterID.String() + "/node-1/drain/",
			deniedRules:   routeSecurityBindings(rbac.ResourceNodes, rbac.VerbUpdate),
			allowRules:    routeSecurityBindings(rbac.ResourceNodes, rbac.VerbManage),
			allowedStatus: http.StatusAccepted,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deniedRouter := NewRouter(&config.Config{}, RouterDependencies{
				JWT:         jwtMgr,
				RBACEngine:  rbac.NewEngine(),
				RBACQueries: routeSecurityRBACQuerier{bindings: tt.deniedRules},
				Resources:   handler.NewResourceHandlerWithRequester(routeSecurityGenericResourceRequester{}),
				Workloads:   handler.NewWorkloadHandlerWithRequester(routeSecurityGenericResourceRequester{}),
			})
			deniedReq := httptest.NewRequest(tt.method, tt.path, nil)
			deniedReq.Header.Set("Authorization", "Bearer "+token)
			deniedRec := httptest.NewRecorder()
			deniedRouter.ServeHTTP(deniedRec, deniedReq)
			if deniedRec.Code != http.StatusForbidden {
				t.Fatalf("denied status = %d, want %d; body=%s", deniedRec.Code, http.StatusForbidden, deniedRec.Body.String())
			}

			allowedRouter := NewRouter(&config.Config{}, RouterDependencies{
				JWT:         jwtMgr,
				RBACEngine:  rbac.NewEngine(),
				RBACQueries: routeSecurityRBACQuerier{bindings: tt.allowRules},
				Resources:   handler.NewResourceHandlerWithRequester(routeSecurityGenericResourceRequester{}),
				Workloads:   handler.NewWorkloadHandlerWithRequester(routeSecurityGenericResourceRequester{}),
			})
			allowedReq := httptest.NewRequest(tt.method, tt.path, nil)
			allowedReq.Header.Set("Authorization", "Bearer "+token)
			allowedRec := httptest.NewRecorder()
			allowedRouter.ServeHTTP(allowedRec, allowedReq)
			if allowedRec.Code != tt.allowedStatus {
				t.Fatalf("allowed status = %d, want %d; body=%s", allowedRec.Code, tt.allowedStatus, allowedRec.Body.String())
			}
		})
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

func TestApiserverAuditIngestRequiresWriteScope(t *testing.T) {
	jwtMgr := auth.NewJWTManager("route-security-test-secret", 60)
	clusterID := uuid.New()
	userID := uuid.New()
	rawToken := "astro_route_security_apiserver_audit_scope"

	// Token carries full admin RBAC (so the cluster:update permission gate
	// passes) and a NON-read write scope (projects:write). That write scope
	// clears the group-level RequireWriteScopeForMutations("") backstop, so
	// only the per-route clusters:write pin can reject it on the audit-ingest
	// POST — this isolates the fail-open this fix closes.
	router := NewRouter(&config.Config{}, RouterDependencies{
		JWT:            jwtMgr,
		AuthQueries:    routeSecurityAPITokenQuerier(rawToken, userID, json.RawMessage(`["projects:write"]`)),
		RBACEngine:     rbac.NewEngine(),
		RBACQueries:    routeSecurityRBACQuerier{bindings: routeSecurityAdminBindings()},
		ApiserverAudit: handler.NewApiserverAuditHandler(nil),
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/"+clusterID.String()+"/apiserver-audit/", nil)
	req.Header.Set("Authorization", "Bearer "+rawToken)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("read-only token status = %d, want %d; body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
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

func TestInternalArgoCDProxyRouterServesTokenlessRequests(t *testing.T) {
	clusterID := uuid.New()
	handler := NewInternalArgoCDProxyRouter(RouterDependencies{
		Proxy: tunnel.NewProxyHandler(tunnel.NewHub(slog.Default()), slog.Default()),
	})
	base := "/api/v1/internal/argocd/clusters/" + clusterID.String() + "/k8s"
	// The internal listener is network-isolated, not token-gated: a tokenless
	// PATCH (ArgoCD's apply path) must reach the proxy handler (503 — no tunnel),
	// not 401. This is exactly what the public route rejects.
	for _, tc := range []struct {
		method, path string
	}{
		{http.MethodPatch, base + "/api/v1/namespaces/trivy-system"},
		{http.MethodGet, base + "/openapi/v2"},
		{http.MethodGet, base + "/api/v1/secrets"},
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s %s: status = %d, want %d (handler reached, no token); body=%s", tc.method, tc.path, rec.Code, http.StatusServiceUnavailable, rec.Body.String())
		}
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

func TestArgoCDInternalK8sProxyMutationsAreAudited(t *testing.T) {
	clusterID := uuid.New()
	token := auth.ArgoCDClusterProxyTokenPrefix + "audit-test-token"
	audit := &routeSecurityAuditWriter{}
	router := NewRouter(&config.Config{}, RouterDependencies{
		AuditWriter: audit,
		Proxy:       tunnel.NewProxyHandler(tunnel.NewHub(slog.Default()), slog.Default()),
		ArgoCDProxyTokens: &routeSecurityArgoTokenQuerier{
			tokenHash: auth.HashArgoCDClusterProxyToken(token),
			clusterID: clusterID,
		},
	})

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/internal/argocd/clusters/"+clusterID.String()+"/k8s/api/v1/namespaces/default/secrets/example", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want proxy handler %d; body=%s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
	if len(audit.rows) != 1 {
		t.Fatalf("audit rows = %d, want 1", len(audit.rows))
	}
	row := audit.rows[0]
	if row.Action != "argocd.k8s_proxy.forwarded" {
		t.Fatalf("audit action = %q", row.Action)
	}
	if row.ResourceType != "cluster" || row.ResourceID != clusterID.String() {
		t.Fatalf("audit resource = %q/%q, want cluster/%s", row.ResourceType, row.ResourceID, clusterID.String())
	}
	var detail map[string]any
	if err := json.Unmarshal(row.Detail, &detail); err != nil {
		t.Fatalf("unmarshal audit detail: %v", err)
	}
	if detail["method"] != http.MethodPatch ||
		detail["k8s_path"] != "/api/v1/namespaces/default/secrets/example" ||
		detail["namespace"] != "default" ||
		detail["resource"] != "secrets" ||
		detail["name"] != "example" ||
		detail["proxy"] != "argocd_internal" {
		t.Fatalf("audit detail = %#v", detail)
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

func routeSecurityBindings(resource rbac.Resource, verbs ...rbac.Verb) []rbac.RoleBinding {
	values := make([]string, 0, len(verbs))
	for _, verb := range verbs {
		values = append(values, string(verb))
	}
	return []rbac.RoleBinding{{
		RoleRules: []rbac.Rule{{Resource: string(resource), Verbs: values}},
	}}
}

func routeSecurityUserBindings(verbs ...rbac.Verb) []rbac.RoleBinding {
	return routeSecurityBindings(rbac.ResourceUsers, verbs...)
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

func methodForCSRFSample(method string) string {
	if method == "" || method == "*" {
		return http.MethodPost
	}
	return method
}

func routeRequiresBrowserCSRF(entry securitySensitiveRoute) bool {
	csrf := strings.ToLower(entry.CSRF)
	return strings.Contains(csrf, "required") && strings.Contains(csrf, "browser cookie")
}

func routeRequiresProxyInventory(pattern string) bool {
	switch {
	case pattern == "/argocd" || strings.HasPrefix(pattern, "/argocd/"):
		return true
	case pattern == "/api/v1/connect/{cluster_id}":
		return true
	case strings.Contains(pattern, "/v2/pods"):
		return true
	case strings.Contains(pattern, "/k8s/"):
		return true
	case strings.Contains(pattern, "/proxy/service/"):
		return true
	case strings.Contains(pattern, "/ws/exec/"):
		return true
	case strings.Contains(pattern, "/ws/logs/"):
		return true
	case strings.Contains(pattern, "/ws/clusters/") && strings.Contains(pattern, "/shell/sessions/"):
		return true
	case strings.HasPrefix(pattern, "/internal/tunnel/"):
		return true
	default:
		return false
	}
}

func proxyInventoryContainsPattern(doc string, pattern string) bool {
	candidates := []string{
		pattern,
		strings.TrimSuffix(pattern, "/"),
		strings.TrimSuffix(pattern, "/") + "/",
	}
	for _, candidate := range candidates {
		if candidate != "" && strings.Contains(doc, candidate) {
			return true
		}
	}
	return false
}

func looksLikeRouteRegistration(line string) bool {
	for _, method := range []string{".Get(", ".Post(", ".Put(", ".Patch(", ".Delete(", ".Handle(", ".HandleFunc(", ".Mount(", ".Route("} {
		if strings.Contains(line, method) {
			return true
		}
	}
	return false
}

type securitySensitiveRoute struct {
	ID                            string   `json:"id"`
	Route                         string   `json:"route"`
	SamplePath                    string   `json:"sample_path"`
	Method                        string   `json:"method"`
	ExpectedUnauthenticatedStatus int      `json:"expected_unauthenticated_status"`
	Surface                       string   `json:"surface"`
	Auth                          string   `json:"auth"`
	RBAC                          string   `json:"rbac"`
	CSRF                          string   `json:"csrf"`
	Audit                         string   `json:"audit"`
	Tests                         []string `json:"tests"`
}

func loadSecuritySensitiveRouteRegistry(t *testing.T, clusterID string) []securitySensitiveRoute {
	t.Helper()
	path := filepath.Join("..", "..", "docs", "security-sensitive-routes.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read security-sensitive route registry: %v", err)
	}
	var entries []securitySensitiveRoute
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("decode security-sensitive route registry: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("security-sensitive route registry is empty")
	}
	seenIDs := map[string]bool{}
	for i := range entries {
		entry := &entries[i]
		if strings.TrimSpace(entry.ID) == "" {
			t.Fatalf("security-sensitive route registry entry %d has empty id", i)
		}
		if seenIDs[entry.ID] {
			t.Fatalf("security-sensitive route registry id %q is duplicated", entry.ID)
		}
		seenIDs[entry.ID] = true
		if strings.TrimSpace(entry.Route) == "" {
			t.Fatalf("security-sensitive route registry entry %s has empty route", entry.ID)
		}
		if strings.TrimSpace(entry.SamplePath) == "" {
			t.Fatalf("security-sensitive route registry entry %s has empty sample_path", entry.ID)
		}
		if strings.TrimSpace(entry.Method) == "" {
			t.Fatalf("security-sensitive route registry entry %s has empty method", entry.ID)
		}
		if entry.ExpectedUnauthenticatedStatus == 0 {
			entry.ExpectedUnauthenticatedStatus = http.StatusUnauthorized
		}
		entry.SamplePath = strings.ReplaceAll(entry.SamplePath, "{cluster_id}", clusterID)
	}
	return entries
}

type routeRiskClassificationFile struct {
	Version int                       `json:"version"`
	Rules   []routeRiskClassification `json:"rules"`
}

type routeRiskClassification struct {
	ID             string   `json:"id"`
	Methods        []string `json:"methods"`
	Patterns       []string `json:"patterns"`
	Classification string   `json:"classification"`
	Reason         string   `json:"reason"`
}

func loadRouteRiskClassifications(t *testing.T) []routeRiskClassification {
	t.Helper()
	path := filepath.Join("..", "..", "docs", "route-risk-classifications.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read route risk classifications: %v", err)
	}
	var file routeRiskClassificationFile
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatalf("decode route risk classifications: %v", err)
	}
	if file.Version != 1 {
		t.Fatalf("route risk classifications version = %d, want 1", file.Version)
	}
	seen := map[string]bool{}
	for i, rule := range file.Rules {
		if strings.TrimSpace(rule.ID) == "" {
			t.Fatalf("route risk classification rule %d has empty id", i)
		}
		if seen[rule.ID] {
			t.Fatalf("route risk classification rule id %q is duplicated", rule.ID)
		}
		seen[rule.ID] = true
		if len(rule.Methods) == 0 {
			t.Fatalf("route risk classification rule %s has no methods", rule.ID)
		}
		if len(rule.Patterns) == 0 {
			t.Fatalf("route risk classification rule %s has no patterns", rule.ID)
		}
		if strings.TrimSpace(rule.Classification) == "" {
			t.Fatalf("route risk classification rule %s has empty classification", rule.ID)
		}
		if strings.TrimSpace(rule.Reason) == "" {
			t.Fatalf("route risk classification rule %s has empty reason", rule.ID)
		}
	}
	return file.Rules
}

type routeInventoryEntry struct {
	Method             string   `json:"method"`
	Pattern            string   `json:"pattern"`
	HandlerOwner       string   `json:"handler_owner"`
	Surface            string   `json:"surface"`
	Auth               string   `json:"auth"`
	RBAC               string   `json:"rbac"`
	CSRF               string   `json:"csrf"`
	Audit              string   `json:"audit"`
	Tests              []string `json:"tests"`
	SecurityRegistryID string   `json:"security_registry_id,omitempty"`
	RiskClassification string   `json:"risk_classification,omitempty"`
	RiskReason         string   `json:"risk_reason,omitempty"`
}

func collectRouteInventory(t *testing.T, router chi.Routes) []routeInventoryEntry {
	t.Helper()
	registry := loadSecuritySensitiveRouteRegistry(t, uuid.NewString())
	classifications := loadRouteRiskClassifications(t)
	var entries []routeInventoryEntry
	if err := chi.Walk(router, func(method string, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		pattern := normalizeRoutePattern(route)
		entry := defaultRouteInventoryEntry(method, route, pattern)
		if metadata, ok := securityRegistryRouteMetadata(registry, method, pattern); ok {
			entry.SecurityRegistryID = metadata.ID
			entry.Surface = metadata.Surface
			entry.Auth = metadata.Auth
			entry.RBAC = metadata.RBAC
			entry.CSRF = metadata.CSRF
			entry.Audit = metadata.Audit
			entry.Tests = append([]string(nil), metadata.Tests...)
		} else if classification, ok := routeRiskClassificationMetadata(classifications, method, pattern); ok {
			entry.RiskClassification = classification.Classification
			entry.RiskReason = classification.Reason
		}
		entries = append(entries, entry)
		return nil
	}); err != nil {
		t.Fatalf("walk router: %v", err)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Pattern == entries[j].Pattern {
			return entries[i].Method < entries[j].Method
		}
		return entries[i].Pattern < entries[j].Pattern
	})
	return entries
}

func defaultRouteInventoryEntry(method string, route string, pattern string) routeInventoryEntry {
	return routeInventoryEntry{
		Method:       method,
		Pattern:      route,
		HandlerOwner: routeHandlerOwner(pattern),
		Surface:      routeSurface(pattern),
		Auth:         routeAuthPosture(pattern),
		RBAC:         routeRBACPosture(method, pattern),
		CSRF:         routeCSRFPosture(method, pattern),
		Audit:        routeAuditPosture(method, pattern),
		Tests:        routeRepresentativeTests(method, pattern),
	}
}

func assertRouteInventoryEntriesHaveCompleteSecurityPosture(t *testing.T, entries []routeInventoryEntry) {
	t.Helper()
	var missing []string
	for _, entry := range entries {
		if strings.TrimSpace(entry.HandlerOwner) != "" &&
			strings.TrimSpace(entry.Surface) != "" &&
			strings.TrimSpace(entry.Auth) != "" &&
			strings.TrimSpace(entry.RBAC) != "" &&
			strings.TrimSpace(entry.CSRF) != "" &&
			strings.TrimSpace(entry.Audit) != "" &&
			len(entry.Tests) > 0 {
			continue
		}
		var fields []string
		if strings.TrimSpace(entry.HandlerOwner) == "" {
			fields = append(fields, "handler_owner")
		}
		if strings.TrimSpace(entry.Surface) == "" {
			fields = append(fields, "surface")
		}
		if strings.TrimSpace(entry.Auth) == "" {
			fields = append(fields, "auth")
		}
		if strings.TrimSpace(entry.RBAC) == "" {
			fields = append(fields, "rbac")
		}
		if strings.TrimSpace(entry.CSRF) == "" {
			fields = append(fields, "csrf")
		}
		if strings.TrimSpace(entry.Audit) == "" {
			fields = append(fields, "audit")
		}
		if len(entry.Tests) == 0 {
			fields = append(fields, "tests")
		}
		missing = append(missing, entry.Method+" "+entry.Pattern+" missing "+strings.Join(fields, ", "))
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("route inventory entries missing complete security posture:\n%s", strings.Join(missing, "\n"))
	}
}

func routeHandlerOwner(pattern string) string {
	for _, rule := range []struct {
		match string
		owner string
	}{
		{"/argocd", "internal/handler.ArgoCDUIProxy"},
		{"/helm-repo", "handler.PlatformChartRepoHandler"},
		{"/health", "internal/server health handler"},
		{"/readyz", "internal/server readiness handler"},
		{"/api/v1/openapi.yaml", "handler.DocsHandler"},
		{"/api/v1/docs", "handler.DocsHandler"},
		{"/api/v1/connect", "internal/tunnel2.RemoteServer"},
		{"/api/v1/ws/clusters", "handler.KubectlShellHandler"},
		{"/api/v1/ws/exec", "internal/tunnel.ExecConsumer"},
		{"/api/v1/ws/logs", "internal/tunnel.LogsConsumer"},
		{"/api/v1/internal/argocd", "handler.ArgoCD internal Kubernetes proxy"},
		{"/api/v1/internal/k8s", "internal/tunnel.InternalK8sHandler"},
		{"/api/v1/internal/helm", "internal/tunnel.InternalHelmHandler"},
		{"/internal/tunnel/k8s", "internal/tunnel.InternalK8sHandler"},
		{"/internal/tunnel/helm", "internal/tunnel.InternalHelmHandler"},
		{"/api/v1/auth/dex", "handler.DexHandler"},
		{"/api/v1/auth/totp", "handler.TOTPHandler"},
		{"/api/v1/auth", "handler.AuthHandler / handler.SSOHandler"},
		{"/api/v1/register", "handler.ClusterRegistrationHandler"},
		{"/api/v1/audit", "handler.AuditHandler"},
		{"/api/v1/agents/fleet", "handler.AgentFleetHandler"},
		{"/api/v1/admin/network-policy-templates", "handler.NetworkPolicyHandler"},
		{"/api/v1/admin/vault-connections", "handler.VaultHandler"},
		{"/api/v1/admin/webhooks", "handler.WebhookHandler"},
		{"/api/v1/admin/smtp", "handler.SMTPHandler"},
		{"/api/v1/admin/emails", "handler.SMTPHandler"},
		{"/api/v1/admin/shell-sessions", "handler.KubectlShellHandler"},
		{"/api/v1/admin/compliance-baselines", "handler.ComplianceBaselinesHandler"},
		{"/api/v1/admin/compliance-baseline-applications", "handler.ComplianceBaselinesHandler"},
		{"/api/v1/admin/compliance", "handler.ComplianceHandler"},
		{"/api/v1/admin/queues", "handler.AdminQueuesHandler"},
		{"/api/v1/admin/task-outbox", "handler.AdminTaskOutboxHandler"},
		{"/api/v1/admin/backup-drill", "handler.AdminDrillHandler"},
		{"/api/v1/admin/management-logs", "handler.ManagementLogsHandler"},
		{"/api/v1/admin/key-status", "internal/server keyStatusHandler"},
		{"/api/v1/admin/users/{id}/disable-totp", "handler.TOTPHandler"},
		{"/api/v1/admin/users", "handler.ResourceHandler user administration"},
		{"/api/v1/backups", "handler.BackupHandler"},
		{"/api/v1/catalog/recommendations", "handler.ChartRatingsHandler"},
		{"/api/v1/catalog", "handler.CatalogHandler"},
		{"/api/v1/charts", "handler.ChartRatingsHandler"},
		{"/api/v1/cloud-credentials", "handler.CloudCredentialHandler"},
		{"/api/v1/cluster-groups", "handler.ClusterGroupHandler"},
		{"/api/v1/cluster-templates", "handler.ClusterTemplateHandler"},
		{"/api/v1/clusters/{cluster_id}/apiserver-allowlist", "handler.ApiserverAllowlistHandler"},
		{"/api/v1/clusters/{cluster_id}/network-policies", "handler.NetworkPolicyHandler"},
		{"/api/v1/clusters/{cluster_id}/registries", "handler.ClusterRegistriesHandler"},
		{"/api/v1/clusters/{cluster_id}/service-mesh", "handler.ServiceMeshHandler"},
		{"/api/v1/clusters/{cluster_id}/shell", "handler.KubectlShellHandler"},
		{"/api/v1/clusters/{cluster_id}/snapshot", "handler.ClusterSnapshotsHandler"},
		{"/api/v1/clusters/{cluster_id}/snapshots", "handler.ClusterSnapshotsHandler"},
		{"/api/v1/clusters/{cluster_id}/velero-status", "handler.ClusterSnapshotsHandler"},
		{"/api/v1/clusters/{cluster_id}/template", "handler.ClusterTemplateHandler"},
		{"/api/v1/clusters/{cluster_id}/workloads", "handler.WorkloadHandler"},
		{"/api/v1/clusters/{cluster_id}/resources", "handler.ResourceHandler"},
		{"/api/v1/clusters/{cluster_id}/proxy/service", "handler.ServiceProxyHandler"},
		{"/api/v1/clusters/{cluster_id}/k8s", "handler.ResourceHandler Kubernetes proxy"},
		{"/api/v1/clusters/{cluster_id}", "handler.ResourceHandler / cluster extension handlers"},
		{"/api/v1/clusters/{id}/registration", "handler.ClusterRegistrationHandler"},
		{"/api/v1/clusters/{id}", "handler.ClusterHandler"},
		{"/api/v1/clusters", "handler.ClusterHandler"},
		{"/api/v1/fleet-operations", "handler.FleetOperationHandler"},
		{"/api/v1/nodes", "handler.ResourceHandler"},
		{"/api/v1/projects/{project_id}/catalogs", "handler.ProjectCatalogHandler"},
		{"/api/v1/projects/{project_id}/cloud-credentials", "handler.CloudCredentialHandler"},
		{"/api/v1/projects/{id}/default-vault-connection", "handler.VaultHandler"},
		{"/api/v1/projects/{id}/quota", "handler.QuotaHandler"},
		{"/api/v1/projects/{id}/rbac", "handler.RBACHandler"},
		{"/api/v1/projects", "handler.ProjectHandler"},
		{"/api/v1/rbac", "handler.RBACHandler"},
		{"/api/v1/resources/search", "handler.ResourcesSearchHandler"},
		{"/api/v1/resources", "handler.ResourceHandler"},
		{"/api/v1/settings/monitoring", "handler.MonitoringHandler"},
		{"/api/v1/settings/tokens", "handler.AuthHandler"},
		{"/api/v1/settings", "handler.ResourceHandler / settings handlers"},
		{"/api/v1/streams/tickets", "handler.StreamTicketHandler"},
		{"/api/v1/support-bundle", "handler.SupportBundleHandler"},
		{"/api/v1/compliance/posture", "handler.CompliancePostureHandler"},
		{"/api/v1/license", "handler.LicenseHandler"},
		{"/api/v1/users", "handler.ResourceHandler"},
		{"/api/v1/workloads", "handler.WorkloadHandler"},
		{"/api/v1/activity", "handler.ResourceHandler"},
	} {
		if pattern == rule.match || strings.HasPrefix(pattern, rule.match+"/") {
			return rule.owner
		}
	}
	if strings.HasPrefix(pattern, "/api/v1/argocd") {
		return "handler.ArgoCDHandler"
	}
	return "internal/server route owner requires explicit classification"
}

func routeSurface(pattern string) string {
	switch {
	case pattern == "/health" || pattern == "/readyz":
		return "platform health endpoint"
	case strings.HasPrefix(pattern, "/helm-repo"):
		return "public Helm chart repository"
	case pattern == "/api/v1/openapi.yaml" || strings.HasPrefix(pattern, "/api/v1/docs"):
		return "API documentation"
	case strings.HasPrefix(pattern, "/argocd"):
		return "browser reverse proxy for the Argo CD UI"
	case strings.HasPrefix(pattern, "/api/v1/internal/"):
		return "internal machine-to-machine tunnel surface"
	case strings.HasPrefix(pattern, "/internal/tunnel/"):
		return "server-to-server tunnel forwarding surface"
	case strings.HasPrefix(pattern, "/api/v1/connect"):
		return "agent tunnel connection"
	case strings.HasPrefix(pattern, "/api/v1/ws/"):
		return "browser stream endpoint"
	case strings.HasPrefix(pattern, "/api/v1/auth/"):
		return "authentication and account security API"
	case strings.HasPrefix(pattern, "/api/v1/admin/"):
		return "administrative API"
	case strings.Contains(pattern, "/clusters/") || strings.HasPrefix(pattern, "/api/v1/clusters"):
		return "adopted cluster management API"
	case strings.HasPrefix(pattern, "/api/v1/rbac"):
		return "authorization and RBAC API"
	case strings.HasPrefix(pattern, "/api/v1/projects"):
		return "project and tenancy API"
	case strings.HasPrefix(pattern, "/api/v1/backups"):
		return "backup and restore API"
	case strings.HasPrefix(pattern, "/api/v1/catalog") || strings.HasPrefix(pattern, "/api/v1/charts"):
		return "catalog and application API"
	case strings.HasPrefix(pattern, "/api/v1/settings"):
		return "platform settings API"
	default:
		return "management API"
	}
}

func routeAuthPosture(pattern string) string {
	switch {
	case pattern == "/health" || pattern == "/health/" || strings.HasPrefix(pattern, "/helm-repo") || pattern == "/api/v1/openapi.yaml" || strings.HasPrefix(pattern, "/api/v1/docs"):
		return "public read endpoint"
	case pattern == "/readyz":
		return "public readiness endpoint; response must avoid secrets"
	case strings.HasPrefix(pattern, "/argocd"):
		return "browser session or bearer auth via AuthBrowserOrBearer"
	case strings.HasPrefix(pattern, "/api/v1/connect"):
		return "agent bearer token"
	case strings.HasPrefix(pattern, "/api/v1/internal/"):
		return "machine-to-machine PSK or cluster-scoped proxy token"
	case strings.HasPrefix(pattern, "/internal/tunnel/"):
		return "server-to-server shared-secret route"
	case strings.HasPrefix(pattern, "/api/v1/ws/"):
		return "short-lived stream ticket; long-lived query JWTs rejected"
	case isPublicAuthFlow(pattern):
		return "public or token-gated auth flow with auth-specific rate limits/challenges"
	case isRouterPublicReadPattern(pattern):
		return "router-level public read route; handler response must stay non-sensitive"
	default:
		return "bearer/session auth through route middleware or handler-specific gate"
	}
}

func routeRBACPosture(method string, pattern string) string {
	switch {
	case pattern == "/health" || pattern == "/readyz" || strings.HasPrefix(pattern, "/helm-repo") || pattern == "/api/v1/openapi.yaml" || strings.HasPrefix(pattern, "/api/v1/docs") || isPublicAuthFlow(pattern):
		return "not applicable: public endpoint"
	case strings.HasPrefix(pattern, "/api/v1/connect"):
		return "not user RBAC: valid cluster agent token identifies the tunnel"
	case strings.HasPrefix(pattern, "/api/v1/internal/argocd"):
		return "not user RBAC: cluster-scoped Argo proxy token"
	case strings.HasPrefix(pattern, "/api/v1/internal/"):
		return "not user RBAC: internal shared-secret route"
	case strings.HasPrefix(pattern, "/internal/tunnel/"):
		return "not user RBAC: internal shared-secret route"
	case strings.HasPrefix(pattern, "/argocd"):
		return "authenticated Astronomer users; upstream Argo CD RBAC still applies"
	case strings.HasPrefix(pattern, "/api/v1/ws/"):
		return "stream ticket is issued only after the matching resource permission check"
	case strings.HasPrefix(pattern, "/api/v1/rbac"):
		return "RBAC read/list or mutation permission enforced by RBAC handler/middleware"
	case strings.HasPrefix(pattern, "/api/v1/admin/"):
		return "admin/superuser or explicit resource permission enforced by handler/middleware"
	case isRouterPublicReadPattern(pattern):
		return "not applicable for public read, or handler-specific read filtering"
	case isMutatingHTTPMethod(method):
		return "route-specific write permission documented in high-risk registry"
	default:
		return "route-specific read/list permission or handler-domain gate"
	}
}

func routeCSRFPosture(method string, pattern string) string {
	switch {
	case !isMutatingHTTPMethod(method):
		return "not applicable: safe HTTP method"
	case strings.HasPrefix(pattern, "/api/v1/internal/") || strings.HasPrefix(pattern, "/api/v1/connect"):
		return "not applicable: machine-to-machine route"
	case strings.HasPrefix(pattern, "/internal/tunnel/"):
		return "not applicable: server-to-server shared-secret route"
	case isPublicAuthFlow(pattern):
		return "auth-flow specific cookie/token handling; not generic browser mutation"
	default:
		return "required for browser cookie mutation by authenticated router"
	}
}

func routeAuditPosture(method string, pattern string) string {
	switch {
	case strings.HasPrefix(pattern, "/api/v1/internal/argocd"):
		return "explicit proxy audit for mutating forwarded Kubernetes calls"
	case strings.HasPrefix(pattern, "/internal/tunnel/"):
		return "server-to-server forwarding route; upstream user action audit is recorded before forwarding"
	case strings.HasPrefix(pattern, "/api/v1/ws/exec"):
		return "explicit stream-open audit for pod exec"
	case strings.HasPrefix(pattern, "/api/v1/ws/logs"):
		return "explicit stream-open audit for pod logs"
	case strings.HasPrefix(pattern, "/api/v1/connect"):
		return "agent connection lifecycle metrics/events"
	case isMutatingHTTPMethod(method):
		return "mutating request audit middleware or explicit handler audit"
	case strings.HasPrefix(pattern, "/api/v1/audit") || strings.Contains(pattern, "audit"):
		return "read-side audit policy or explicit audit-log route coverage"
	default:
		return "read-side audit when policy matches; no request/response secrets recorded"
	}
}

func routeRepresentativeTests(method string, pattern string) []string {
	tests := []string{"TestRouteInventoryEntriesHaveCompleteSecurityPosture"}
	if isMutatingHTTPMethod(method) {
		tests = append(tests, "TestMutatingRoutesHaveSecurityClassification")
	}
	if strings.HasPrefix(pattern, "/api/v1/internal/") || strings.HasPrefix(pattern, "/internal/tunnel/") || strings.Contains(pattern, "/k8s") || strings.Contains(pattern, "/proxy/") || strings.HasPrefix(pattern, "/argocd") || strings.HasPrefix(pattern, "/api/v1/ws/") {
		tests = append(tests, "TestForwardingRoutesAreDocumentedInProxyInventory")
	}
	return tests
}

func isPublicAuthFlow(pattern string) bool {
	return pattern == "/api/v1/auth/login" ||
		pattern == "/api/v1/auth/refresh" ||
		pattern == "/api/v1/auth/logout" ||
		pattern == "/api/v1/auth/logout-done" ||
		pattern == "/api/v1/auth/password-reset/request" ||
		pattern == "/api/v1/auth/password-reset/complete" ||
		pattern == "/api/v1/auth/totp/verify" ||
		strings.HasPrefix(pattern, "/api/v1/auth/login/{provider}") ||
		strings.HasPrefix(pattern, "/api/v1/auth/callback/{provider}")
}

func isRouterPublicReadPattern(pattern string) bool {
	switch pattern {
	case "/api/v1/settings/general", "/api/v1/settings/sso", "/api/v1/settings/sso/presets", "/api/v1/settings/branding", "/api/v1/settings/banner", "/api/v1/settings/features":
		return true
	case "/api/v1/register/ca.crt":
		return true
	default:
		return false
	}
}

func routeInventoryHasSecurityRegistryID(entries []routeInventoryEntry, method string, pattern string, securityRegistryID string) bool {
	normalizedPattern := normalizeRoutePattern(pattern)
	for _, entry := range entries {
		if !strings.EqualFold(entry.Method, method) {
			continue
		}
		if normalizeRoutePattern(entry.Pattern) == normalizedPattern && entry.SecurityRegistryID == securityRegistryID {
			return true
		}
	}
	return false
}

func securityRegistryRouteMetadata(entries []securitySensitiveRoute, method string, pattern string) (securitySensitiveRoute, bool) {
	for _, entry := range entries {
		if !routeMethodMatches(entry.Method, method) {
			continue
		}
		if routePatternMatches(normalizeRoutePattern(entry.Route), pattern) {
			return entry, true
		}
	}
	return securitySensitiveRoute{}, false
}

func routeRiskClassificationMetadata(rules []routeRiskClassification, method string, pattern string) (routeRiskClassification, bool) {
	for _, rule := range rules {
		if !routeMethodListMatches(rule.Methods, method) {
			continue
		}
		for _, candidate := range rule.Patterns {
			if routePatternMatches(normalizeRoutePattern(candidate), pattern) {
				return rule, true
			}
		}
	}
	return routeRiskClassification{}, false
}

func isMutatingHTTPMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func securityRegistryCoversRoute(entries []securitySensitiveRoute, method string, pattern string) bool {
	for _, entry := range entries {
		if !routeMethodMatches(entry.Method, method) {
			continue
		}
		if routePatternMatches(normalizeRoutePattern(entry.Route), pattern) {
			return true
		}
	}
	return false
}

func routeRiskClassified(rules []routeRiskClassification, method string, pattern string) bool {
	for _, rule := range rules {
		if !routeMethodListMatches(rule.Methods, method) {
			continue
		}
		for _, candidate := range rule.Patterns {
			if routePatternMatches(normalizeRoutePattern(candidate), pattern) {
				return true
			}
		}
	}
	return false
}

func routeMethodListMatches(methods []string, method string) bool {
	for _, candidate := range methods {
		if routeMethodMatches(candidate, method) {
			return true
		}
	}
	return false
}

func routeMethodMatches(candidate string, method string) bool {
	return candidate == "*" || strings.EqualFold(candidate, method)
}

var (
	singleLiteralRouteParamRE = regexp.MustCompile(`\{[^}:]+:\(\?:([A-Za-z0-9_-]+)\)\}`)
	regexRouteParamRE         = regexp.MustCompile(`\{([^}:]+):[^}]+\}`)
)

func normalizeRoutePattern(pattern string) string {
	pattern = strings.ReplaceAll(pattern, "/api/v1/*/", "/api/v1/")
	pattern = strings.ReplaceAll(pattern, "/api/v1/*", "/api/v1")
	pattern = singleLiteralRouteParamRE.ReplaceAllString(pattern, "$1")
	pattern = regexRouteParamRE.ReplaceAllString(pattern, "{$1}")
	if len(pattern) > 1 {
		pattern = strings.TrimRight(pattern, "/")
	}
	return pattern
}

func routePatternMatches(candidate string, pattern string) bool {
	if candidate == pattern {
		return true
	}
	quoted := regexp.QuoteMeta(candidate)
	quoted = strings.ReplaceAll(quoted, `\*`, `.*`)
	re := regexp.MustCompile(`^` + quoted + `$`)
	return re.MatchString(pattern)
}
