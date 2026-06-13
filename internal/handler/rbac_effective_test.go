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
