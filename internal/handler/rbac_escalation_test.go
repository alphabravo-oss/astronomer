package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

// fakeCallerBindings is a middleware.RBACQuerier that returns a fixed set of
// bindings per user ID, letting the escalation guard resolve the caller's own
// effective permissions without a database.
type fakeCallerBindings struct {
	byUser map[string][]rbac.RoleBinding
}

func (f *fakeCallerBindings) GetUserBindings(_ context.Context, userID string) ([]rbac.RoleBinding, error) {
	return f.byUser[userID], nil
}

func wildcardRuleJSON() json.RawMessage {
	return json.RawMessage(`[{"resource":"*","verbs":["*"]}]`)
}

func rbacCreateRuleJSON() json.RawMessage {
	return json.RawMessage(`[{"resource":"rbac","verbs":["create"]}]`)
}

// TestCreateGlobalRoleBinding_BlocksPrivilegeEscalation asserts that a caller
// holding only {rbac:create} cannot grant themselves a wildcard admin role,
// while a superuser (and a caller who already holds the target role's rules)
// can.
func TestCreateGlobalRoleBinding_BlocksPrivilegeEscalation(t *testing.T) {
	q := newFakeRBACAuditQuerier()
	engine := rbac.NewEngine()

	// The wildcard "full admin" role the attacker wants to self-grant.
	wildcardRole, err := q.CreateGlobalRole(context.Background(), sqlc.CreateGlobalRoleParams{
		Name:  "platform-admin",
		Rules: wildcardRuleJSON(),
	})
	if err != nil {
		t.Fatalf("seed wildcard role: %v", err)
	}
	// A benign role granting exactly what the low-priv caller holds.
	benignRole, err := q.CreateGlobalRole(context.Background(), sqlc.CreateGlobalRoleParams{
		Name:  "rbac-creator",
		Rules: rbacCreateRuleJSON(),
	})
	if err != nil {
		t.Fatalf("seed benign role: %v", err)
	}

	lowPriv := uuid.New()
	holder := uuid.New()
	superuser := uuid.New()

	bindings := &fakeCallerBindings{byUser: map[string][]rbac.RoleBinding{
		// lowPriv holds only rbac:create at global scope.
		lowPriv.String(): {{UserID: lowPriv.String(), RoleRules: []rbac.Rule{{Resource: "rbac", Verbs: []string{"create"}}}}},
		// holder holds rbac:create too — enough to grant the benign role.
		holder.String(): {{UserID: holder.String(), RoleRules: []rbac.Rule{{Resource: "rbac", Verbs: []string{"create"}}}}},
		// superuser short-circuits every check.
		superuser.String(): {{UserID: superuser.String(), IsSuperuser: true}},
	}}

	h := NewRBACHandler(q)
	h.SetAuthorization(engine, bindings)

	post := func(caller, target uuid.UUID, roleID uuid.UUID) *httptest.ResponseRecorder {
		body := []byte(fmt.Sprintf(`{"user_id":"%s","role_id":"%s"}`, target, roleID))
		req := authedRequest(http.MethodPost, "/api/v1/rbac/global-role-bindings/", caller, body)
		rec := httptest.NewRecorder()
		h.CreateGlobalRoleBinding(rec, req)
		return rec
	}

	// 1) Escalation attempt: lowPriv self-grants the wildcard role → 403.
	if rec := post(lowPriv, lowPriv, wildcardRole.ID); rec.Code != http.StatusForbidden {
		t.Fatalf("escalation attempt: status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	if len(q.globalBindings) != 0 {
		t.Fatalf("escalation attempt persisted a binding: %d present", len(q.globalBindings))
	}

	// 2) Superuser grants the wildcard role → 201.
	if rec := post(superuser, lowPriv, wildcardRole.ID); rec.Code != http.StatusCreated {
		t.Fatalf("superuser grant: status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}

	// 3) Caller who already holds the target role's rules → 201.
	if rec := post(holder, holder, benignRole.ID); rec.Code != http.StatusCreated {
		t.Fatalf("in-scope grant: status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
}

// TestCreateClusterAndProjectRoleBinding_BlockEscalation covers the cluster- and
// project-scoped create handlers with the same low-priv-vs-wildcard escalation.
func TestCreateClusterAndProjectRoleBinding_BlockEscalation(t *testing.T) {
	q := newFakeRBACAuditQuerier()
	engine := rbac.NewEngine()

	clusterRole, err := q.CreateClusterRole(context.Background(), sqlc.CreateClusterRoleParams{
		Name:  "cluster-admin",
		Rules: wildcardRuleJSON(),
	})
	if err != nil {
		t.Fatalf("seed cluster role: %v", err)
	}
	projectRole, err := q.CreateProjectRole(context.Background(), sqlc.CreateProjectRoleParams{
		Name:  "project-admin",
		Rules: wildcardRuleJSON(),
	})
	if err != nil {
		t.Fatalf("seed project role: %v", err)
	}

	lowPriv := uuid.New()
	clusterID := uuid.New()
	projectID := uuid.New()

	bindings := &fakeCallerBindings{byUser: map[string][]rbac.RoleBinding{
		lowPriv.String(): {{UserID: lowPriv.String(), RoleRules: []rbac.Rule{{Resource: "rbac", Verbs: []string{"create"}}}}},
	}}

	h := NewRBACHandler(q)
	h.SetAuthorization(engine, bindings)

	clusterBody := []byte(fmt.Sprintf(`{"user_id":"%s","role_id":"%s","cluster_id":"%s"}`, lowPriv, clusterRole.ID, clusterID))
	clusterReq := authedRequest(http.MethodPost, "/api/v1/rbac/cluster-role-bindings/", lowPriv, clusterBody)
	clusterRec := httptest.NewRecorder()
	h.CreateClusterRoleBinding(clusterRec, clusterReq)
	if clusterRec.Code != http.StatusForbidden {
		t.Fatalf("cluster escalation: status = %d, want 403; body=%s", clusterRec.Code, clusterRec.Body.String())
	}
	if len(q.clusterBindings) != 0 {
		t.Fatalf("cluster escalation persisted a binding: %d present", len(q.clusterBindings))
	}

	projectBody := []byte(fmt.Sprintf(`{"user_id":"%s","role_id":"%s","project_id":"%s"}`, lowPriv, projectRole.ID, projectID))
	projectReq := authedRequest(http.MethodPost, "/api/v1/rbac/project-role-bindings/", lowPriv, projectBody)
	projectRec := httptest.NewRecorder()
	h.CreateProjectRoleBinding(projectRec, projectReq)
	if projectRec.Code != http.StatusForbidden {
		t.Fatalf("project escalation: status = %d, want 403; body=%s", projectRec.Code, projectRec.Body.String())
	}
	if len(q.projectBindings) != 0 {
		t.Fatalf("project escalation persisted a binding: %d present", len(q.projectBindings))
	}
}
