package handler

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// TestCreateBinding_RejectsGroupScopedBindings locks in the guard that
// blocks the manual role-binding API from persisting group-scoped (or
// user-less) bindings. The authorization engine resolves bindings strictly
// by user_id (ListUserBindingsWithRoles / GetUserBindings), so a stored
// group binding is a silent no-op grant. The handlers must fail closed with
// a 400 and write nothing.
func TestCreateBinding_RejectsGroupScopedBindings(t *testing.T) {
	callerID := uuid.New()
	roleID := uuid.New()
	clusterID := uuid.New()
	projectID := uuid.New()

	cases := []struct {
		name string
		path string
		body []byte
		call func(h *RBACHandler, w http.ResponseWriter, r *http.Request)
	}{
		{
			name: "global group-only",
			path: "/api/v1/rbac/global-role-bindings/",
			body: []byte(fmt.Sprintf(`{"group":"admins","role_id":"%s"}`, roleID)),
			call: (*RBACHandler).CreateGlobalRoleBinding,
		},
		{
			name: "global user-less",
			path: "/api/v1/rbac/global-role-bindings/",
			body: []byte(fmt.Sprintf(`{"role_id":"%s"}`, roleID)),
			call: (*RBACHandler).CreateGlobalRoleBinding,
		},
		{
			name: "cluster group-only",
			path: "/api/v1/rbac/cluster-role-bindings/",
			body: []byte(fmt.Sprintf(`{"group":"ops","role_id":"%s","cluster_id":"%s"}`, roleID, clusterID)),
			call: (*RBACHandler).CreateClusterRoleBinding,
		},
		{
			name: "project group-only",
			path: "/api/v1/rbac/project-role-bindings/",
			body: []byte(fmt.Sprintf(`{"group":"devs","role_id":"%s","project_id":"%s"}`, roleID, projectID)),
			call: (*RBACHandler).CreateProjectRoleBinding,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := newFakeRBACAuditQuerier()
			h := NewRBACHandler(q)

			req := authedRequest(http.MethodPost, tc.path, callerID, tc.body)
			rec := httptest.NewRecorder()
			tc.call(h, rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
			}
			if n := len(q.globalBindings) + len(q.clusterBindings) + len(q.projectBindings); n != 0 {
				t.Fatalf("bindings persisted = %d, want 0 (group binding must not be stored)", n)
			}
		})
	}
}

// TestCreateGlobalBinding_UserBindingStillSucceeds guards against the reject
// helper being over-broad: a concrete user_id binding (no group) must still
// be created.
func TestCreateGlobalBinding_UserBindingStillSucceeds(t *testing.T) {
	callerID := uuid.New()
	userID := uuid.New()
	roleID := uuid.New()

	q := newFakeRBACAuditQuerier()
	h := NewRBACHandler(q)

	body := []byte(fmt.Sprintf(`{"user_id":"%s","role_id":"%s"}`, userID, roleID))
	req := authedRequest(http.MethodPost, "/api/v1/rbac/global-role-bindings/", callerID, body)
	rec := httptest.NewRecorder()
	h.CreateGlobalRoleBinding(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if len(q.globalBindings) != 1 {
		t.Fatalf("global bindings = %d, want 1", len(q.globalBindings))
	}
}
