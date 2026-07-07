package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

type fakeEffectiveBindingQuerier struct {
	bindings map[string][]rbac.RoleBinding
}

func (f fakeEffectiveBindingQuerier) GetUserBindings(_ context.Context, userID string) ([]rbac.RoleBinding, error) {
	out := f.bindings[userID]
	copied := make([]rbac.RoleBinding, len(out))
	copy(copied, out)
	return copied, nil
}

func TestMyEffectivePermissionsReturnsSources(t *testing.T) {
	userID := "11111111-1111-1111-1111-111111111111"
	h := &RBACHandler{}
	h.SetAuthorization(rbac.NewEngine(), fakeEffectiveBindingQuerier{bindings: map[string][]rbac.RoleBinding{
		userID: {
			{
				UserID:    userID,
				BindingID: "binding-1",
				RoleID:    "role-1",
				RoleName:  "Project Deployer",
				Scope:     "project",
				ProjectID: "project-1",
				RoleRules: []rbac.Rule{{Resource: "workloads", Verbs: []string{"read", "update"}}},
			},
		},
	}})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/rbac/my-permissions/", nil)
	req = req.WithContext(middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{ID: userID}))
	rr := httptest.NewRecorder()
	h.MyEffectivePermissions(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var envelope struct {
		Data effectivePermissionResponse `json:"data"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got := envelope.Data
	if !got.Subject.Self || got.Subject.UserID != userID {
		t.Fatalf("subject = %+v", got.Subject)
	}
	if len(got.Permissions) != 2 {
		t.Fatalf("permissions len = %d, want 2", len(got.Permissions))
	}
	if got.Permissions[0].Sources[0].RoleName != "Project Deployer" {
		t.Fatalf("source role = %+v", got.Permissions[0].Sources[0])
	}
}

func TestMyEffectivePermissionsReturnsSelectedNamespaceContext(t *testing.T) {
	userID := "11111111-1111-1111-1111-111111111111"
	projectID := "22222222-2222-2222-2222-222222222222"
	clusterID := "33333333-3333-3333-3333-333333333333"
	otherProjectID := "44444444-4444-4444-4444-444444444444"
	h := &RBACHandler{}
	h.SetAuthorization(rbac.NewEngine(), fakeEffectiveBindingQuerier{bindings: map[string][]rbac.RoleBinding{
		userID: {
			{
				UserID:    userID,
				BindingID: "binding-project",
				RoleName:  "Project Deployer",
				Scope:     "project",
				ProjectID: projectID,
				RoleRules: []rbac.Rule{{Resource: "workloads", Verbs: []string{"read"}}},
			},
			{
				UserID:    userID,
				BindingID: "binding-project-namespace",
				RoleName:  "Config Manager",
				Scope:     "project",
				ProjectID: projectID,
				Namespace: "payments",
				RoleRules: []rbac.Rule{{Resource: "configmaps", Verbs: []string{"list"}}},
			},
			{
				UserID:    userID,
				BindingID: "binding-cluster",
				RoleName:  "Cluster Viewer",
				Scope:     "cluster",
				ClusterID: clusterID,
				RoleRules: []rbac.Rule{{Resource: "clusters", Verbs: []string{"read"}}},
			},
			{
				UserID:    userID,
				BindingID: "binding-global",
				RoleName:  "Audit Viewer",
				Scope:     "global",
				RoleRules: []rbac.Rule{{Resource: "audit_logs", Verbs: []string{"read"}}},
			},
		},
	}})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/rbac/my-permissions/?project_id="+projectID+"&namespace=payments", nil)
	req = req.WithContext(middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{ID: userID}))
	rr := httptest.NewRecorder()
	h.MyEffectivePermissions(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var envelope struct {
		Data effectivePermissionResponse `json:"data"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got := envelope.Data
	if got.Context.ProjectID != projectID || got.Context.Namespace != "payments" {
		t.Fatalf("context = %+v", got.Context)
	}
	if got.Context.NamespaceScopedBindingsSupported {
		t.Fatalf("namespace-scoped bindings should not be reported as supported: %+v", got.Context)
	}
	if len(got.Context.Warnings) != 1 || !strings.Contains(got.Context.Warnings[0], "enforced for namespace-scoped bindings") {
		t.Fatalf("context warnings = %+v", got.Context.Warnings)
	}
	if !effectiveGrantByKey(t, got.Permissions, "workloads", "read").AppliesToContext {
		t.Fatalf("project workload grant should apply to selected project context: %+v", got.Permissions)
	}
	if !effectiveGrantByKey(t, got.Permissions, "audit_logs", "read").AppliesToContext {
		t.Fatalf("global audit grant should apply to selected project context: %+v", got.Permissions)
	}
	if effectiveGrantByKey(t, got.Permissions, "clusters", "read").AppliesToContext {
		t.Fatalf("cluster grant should not apply to selected project context: %+v", got.Permissions)
	}
	configMapGrant := effectiveGrantByKey(t, got.Permissions, "configmaps", "list")
	if !configMapGrant.AppliesToContext {
		t.Fatalf("namespace-scoped configmap grant should apply to matching namespace context: %+v", got.Permissions)
	}
	if len(configMapGrant.Sources) != 1 || configMapGrant.Sources[0].Namespace != "payments" {
		t.Fatalf("namespace-scoped source missing namespace: %+v", configMapGrant.Sources)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/rbac/my-permissions/?project_id="+otherProjectID, nil)
	req = req.WithContext(middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{ID: userID}))
	rr = httptest.NewRecorder()
	h.MyEffectivePermissions(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("nonmatching project status = %d body=%s", rr.Code, rr.Body.String())
	}
	if err := json.NewDecoder(rr.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode nonmatching project: %v", err)
	}
	if effectiveGrantByKey(t, envelope.Data.Permissions, "workloads", "read").AppliesToContext {
		t.Fatalf("project workload grant should not apply to another project: %+v", envelope.Data.Permissions)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/rbac/my-permissions/?project_id="+projectID+"&namespace=default", nil)
	req = req.WithContext(middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{ID: userID}))
	rr = httptest.NewRecorder()
	h.MyEffectivePermissions(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("nonmatching namespace status = %d body=%s", rr.Code, rr.Body.String())
	}
	if err := json.NewDecoder(rr.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode nonmatching namespace: %v", err)
	}
	if effectiveGrantByKey(t, envelope.Data.Permissions, "configmaps", "list").AppliesToContext {
		t.Fatalf("namespace-scoped grant should not apply to another namespace: %+v", envelope.Data.Permissions)
	}
}

func TestMyEffectivePermissionsRejectsInvalidContext(t *testing.T) {
	userID := "11111111-1111-1111-1111-111111111111"
	h := &RBACHandler{}
	h.SetAuthorization(rbac.NewEngine(), fakeEffectiveBindingQuerier{bindings: map[string][]rbac.RoleBinding{userID: nil}})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/rbac/my-permissions/?namespace=bad_namespace", nil)
	req = req.WithContext(middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{ID: userID}))
	rr := httptest.NewRecorder()
	h.MyEffectivePermissions(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
}

func effectiveGrantByKey(t *testing.T, grants []effectivePermissionGrant, resource, verb string) effectivePermissionGrant {
	t.Helper()
	for _, grant := range grants {
		if grant.Resource == resource && grant.Verb == verb {
			return grant
		}
	}
	t.Fatalf("missing grant %s:%s in %+v", resource, verb, grants)
	return effectivePermissionGrant{}
}

func TestPermissionPreviewTemplateFlagsSensitiveAccess(t *testing.T) {
	cat, err := rbac.LoadCatalog()
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	h := &RBACHandler{}
	h.SetTemplateCatalog(cat)
	body := strings.NewReader(`{"scope":"project","template_name":"secret-manager","project_id":"project-1"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/rbac/permission-preview/", body)
	rr := httptest.NewRecorder()
	h.PermissionPreview(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var envelope struct {
		Data permissionPreviewResponse `json:"data"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got := envelope.Data
	if got.TemplateName != "secret-manager" || got.RiskLevel != "critical" {
		t.Fatalf("preview = %+v", got)
	}
	if !got.SensitiveFlags.CanReadSecrets || !got.SensitiveFlags.CanDelete {
		t.Fatalf("sensitive flags = %+v", got.SensitiveFlags)
	}
}

// TestGrantsFromEffectiveMarksInheritance verifies the preview renderer carries
// the direct/inherited provenance of a template's flattened grants into the API
// response so the UI can distinguish inherited permissions from direct ones.
func TestGrantsFromEffectiveMarksInheritance(t *testing.T) {
	src := effectivePermissionSource{Scope: "project", RoleName: "Top Admin"}
	grants := grantsFromEffective([]rbac.EffectiveGrant{
		{Resource: "workloads", Verb: "delete"},
		{Resource: "pods", Verb: "read", Inherited: true, InheritedFrom: "base-viewer"},
	}, src)

	direct := effectiveGrantByKey(t, grants, "workloads", "delete")
	if direct.Inherited || direct.InheritedFrom != "" {
		t.Errorf("workloads:delete should be direct, got %+v", direct)
	}
	inherited := effectiveGrantByKey(t, grants, "pods", "read")
	if !inherited.Inherited || inherited.InheritedFrom != "base-viewer" {
		t.Errorf("pods:read should be inherited from base-viewer, got %+v", inherited)
	}
	if len(inherited.Sources) != 1 || inherited.Sources[0].RoleName != "Top Admin" {
		t.Errorf("inherited grant sources = %+v", inherited.Sources)
	}
}

func TestPermissionPreviewRejectsScopeMismatch(t *testing.T) {
	cat, err := rbac.LoadCatalog()
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	h := &RBACHandler{}
	h.SetTemplateCatalog(cat)
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/rbac/permission-preview/",
		strings.NewReader(`{"scope":"cluster","template_name":"secret-manager"}`),
	)
	rr := httptest.NewRecorder()
	h.PermissionPreview(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
}
