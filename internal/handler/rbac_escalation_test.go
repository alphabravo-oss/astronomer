package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

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

// rbacAdminRuleJSON is the rule set of the seeded 'RBAC Administrator' built-in
// (migration 032): the delegated role-admin persona that ships in the box.
func rbacAdminRuleJSON() json.RawMessage {
	return json.RawMessage(`[{"resource":"rbac","verbs":["*"]},{"resource":"users","verbs":["read","list"]}]`)
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

// putRole is a small helper: PUT a role definition as caller, with the chi
// {id} URL param wired the way the router would.
func putRole(t *testing.T, handler http.HandlerFunc, caller, roleID uuid.UUID, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := withURLParam(
		authedRequest(http.MethodPut, "/api/v1/rbac/roles/"+roleID.String()+"/", caller, []byte(body)),
		"id",
		roleID.String(),
	)
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

// deleteRole is the DELETE counterpart of putRole.
func deleteRole(t *testing.T, handler http.HandlerFunc, caller, roleID uuid.UUID) *httptest.ResponseRecorder {
	t.Helper()
	req := withURLParam(
		authedRequest(http.MethodDelete, "/api/v1/rbac/roles/"+roleID.String()+"/", caller, nil),
		"id",
		roleID.String(),
	)
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

// TestUpdateRoleRulesBlocksPrivilegeEscalation asserts that rewriting a role's
// rules is held to the same "you cannot grant what you do not hold" bar as
// creating a binding. Without it a caller holding rbac:update simply PUTs
// wildcard rules onto the role they are already bound to and is platform admin
// on the next request — the binding-time guard never runs because no binding is
// created.
func TestUpdateRoleRulesBlocksPrivilegeEscalation(t *testing.T) {
	q := newFakeRBACAuditQuerier()
	engine := rbac.NewEngine()

	globalRole, err := q.CreateGlobalRole(context.Background(), sqlc.CreateGlobalRoleParams{
		Name:  "delegated-rbac",
		Rules: rbacAdminRuleJSON(),
	})
	if err != nil {
		t.Fatalf("seed global role: %v", err)
	}
	clusterRole, err := q.CreateClusterRole(context.Background(), sqlc.CreateClusterRoleParams{
		Name:  "delegated-rbac-cluster",
		Rules: rbacAdminRuleJSON(),
	})
	if err != nil {
		t.Fatalf("seed cluster role: %v", err)
	}
	projectRole, err := q.CreateProjectRole(context.Background(), sqlc.CreateProjectRoleParams{
		Name:  "delegated-rbac-project",
		Rules: rbacAdminRuleJSON(),
	})
	if err != nil {
		t.Fatalf("seed project role: %v", err)
	}

	// The caller is bound to the seeded 'RBAC Administrator' rule set: rbac:*
	// but nothing else. Enough to reach the route, not enough to author *:*.
	lowPriv := uuid.New()
	bindings := &fakeCallerBindings{byUser: map[string][]rbac.RoleBinding{
		lowPriv.String(): {{UserID: lowPriv.String(), RoleRules: []rbac.Rule{
			{Resource: "rbac", Verbs: []string{"*"}},
			{Resource: "users", Verbs: []string{"read", "list"}},
		}}},
	}}

	h := NewRBACHandler(q)
	h.SetAuthorization(engine, bindings)

	escalation := `{"name":"delegated-rbac","rules":[{"resource":"*","verbs":["*"]}]}`

	if rec := putRole(t, h.UpdateGlobalRole, lowPriv, globalRole.ID, escalation); rec.Code != http.StatusForbidden {
		t.Fatalf("global role escalation: status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	if got := string(q.globalRoles[globalRole.ID].Rules); got != string(rbacAdminRuleJSON()) {
		t.Fatalf("global role rules mutated: %s", got)
	}

	if rec := putRole(t, h.UpdateClusterRole, lowPriv, clusterRole.ID, escalation); rec.Code != http.StatusForbidden {
		t.Fatalf("cluster role escalation: status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	if got := string(q.clusterRoles[clusterRole.ID].Rules); got != string(rbacAdminRuleJSON()) {
		t.Fatalf("cluster role rules mutated: %s", got)
	}

	if rec := putRole(t, h.UpdateProjectRole, lowPriv, projectRole.ID, escalation); rec.Code != http.StatusForbidden {
		t.Fatalf("project role escalation: status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	if got := string(q.projectRoles[projectRole.ID].Rules); got != string(rbacAdminRuleJSON()) {
		t.Fatalf("project role rules mutated: %s", got)
	}
}

// TestBuiltinRoleWritesAreRejected covers the second half of the finding: the
// seeded built-ins are frozen against PUT and DELETE. DELETE is the worse half
// — global_role_bindings cascades on role delete, so dropping the seeded
// 'Administrator' row revokes every real admin's binding and locks the platform
// out. The caller here is a superuser, so the rejection is proved to be
// unconditional rather than a side effect of the escalation check.
func TestBuiltinRoleWritesAreRejected(t *testing.T) {
	q := newFakeRBACAuditQuerier()
	engine := rbac.NewEngine()

	administrator, err := q.CreateGlobalRole(context.Background(), sqlc.CreateGlobalRoleParams{
		Name:      "Administrator",
		Rules:     wildcardRuleJSON(),
		IsBuiltin: true,
	})
	if err != nil {
		t.Fatalf("seed builtin role: %v", err)
	}
	admin := uuid.New()
	if _, err := q.CreateGlobalRoleBinding(context.Background(), sqlc.CreateGlobalRoleBindingParams{
		UserID: pgtype.UUID{Bytes: admin, Valid: true},
		RoleID: administrator.ID,
	}); err != nil {
		t.Fatalf("seed admin binding: %v", err)
	}

	superuser := uuid.New()
	bindings := &fakeCallerBindings{byUser: map[string][]rbac.RoleBinding{
		superuser.String(): {{UserID: superuser.String(), IsSuperuser: true}},
	}}

	h := NewRBACHandler(q)
	h.SetAuthorization(engine, bindings)

	rec := putRole(t, h.UpdateGlobalRole, superuser, administrator.ID, `{"name":"Administrator","rules":[{"resource":"users","verbs":["read"]}]}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("builtin update: status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	if got := string(q.globalRoles[administrator.ID].Rules); got != string(wildcardRuleJSON()) {
		t.Fatalf("builtin role rules mutated: %s", got)
	}

	if rec := deleteRole(t, h.DeleteGlobalRole, superuser, administrator.ID); rec.Code != http.StatusForbidden {
		t.Fatalf("builtin delete: status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	if _, ok := q.globalRoles[administrator.ID]; !ok {
		t.Fatal("builtin role was deleted")
	}
	if len(q.globalBindings) != 1 {
		t.Fatalf("administrator bindings cascaded away: %d remain, want 1", len(q.globalBindings))
	}
}

// TestCreatedRolesAreNeverBuiltin closes the primitive the freeze would
// otherwise hand out. is_builtin used to be a cosmetic, client-settable field;
// now it makes a row permanently un-editable and un-deletable through the API,
// so honouring it from a create body would let anyone holding rbac:create plant
// roles nobody — superuser included — can ever remove, and would let a stray
// flag in an operator's payload brick a role. is_builtin is migration-owned:
// the key is accepted and ignored, and the resulting row stays fully mutable.
func TestCreatedRolesAreNeverBuiltin(t *testing.T) {
	q := newFakeRBACAuditQuerier()
	caller := uuid.New()
	bindings := &fakeCallerBindings{byUser: map[string][]rbac.RoleBinding{
		caller.String(): {{UserID: caller.String(), IsSuperuser: true}},
	}}

	h := NewRBACHandler(q)
	h.SetAuthorization(rbac.NewEngine(), bindings)

	body := []byte(`{"name":"planted","is_builtin":true,"rules":[{"resource":"users","verbs":["read"]}]}`)
	rec := httptest.NewRecorder()
	h.CreateGlobalRole(rec, authedRequest(http.MethodPost, "/api/v1/rbac/global-roles/", caller, body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}

	var planted uuid.UUID
	for id, role := range q.globalRoles {
		if role.Name == "planted" {
			planted = id
			if role.IsBuiltin {
				t.Fatal("is_builtin:true from the request body was honoured; the row is now un-editable and un-deletable")
			}
		}
	}
	if planted == uuid.Nil {
		t.Fatal("created role not found")
	}

	// The row must still be administrable — that is the whole point.
	if rec := putRole(t, h.UpdateGlobalRole, caller, planted, `{"name":"planted","rules":[{"resource":"users","verbs":["read","list"]}]}`); rec.Code != http.StatusOK {
		t.Fatalf("update: status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if rec := deleteRole(t, h.DeleteGlobalRole, caller, planted); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if _, ok := q.globalRoles[planted]; ok {
		t.Fatal("role survived delete")
	}
}

// TestRoleWriteGuardPreservesShippedRoleAdministration is the compatibility
// half: the guard must not take away anything a shipped built-in can do today.
//
//   - 'Administrator' (*:*, migration 001) edits any custom role, unchanged.
//   - 'RBAC Administrator' (rbac:* + users:read/list, migration 032) keeps
//     editing custom roles within its own rule set, and keeps DELETE on a
//     custom role whose rules exceed its own — deleting grants nobody anything,
//     so it is deliberately not escalation-checked (see rejectBuiltinRoleWrite).
func TestRoleWriteGuardPreservesShippedRoleAdministration(t *testing.T) {
	q := newFakeRBACAuditQuerier()
	engine := rbac.NewEngine()

	custom, err := q.CreateGlobalRole(context.Background(), sqlc.CreateGlobalRoleParams{
		Name:  "custom-operator",
		Rules: rbacCreateRuleJSON(),
	})
	if err != nil {
		t.Fatalf("seed custom role: %v", err)
	}
	// A custom role carrying more than the delegated RBAC admin holds.
	overPowered, err := q.CreateGlobalRole(context.Background(), sqlc.CreateGlobalRoleParams{
		Name:  "custom-workload-admin",
		Rules: json.RawMessage(`[{"resource":"workloads","verbs":["*"]}]`),
	})
	if err != nil {
		t.Fatalf("seed over-powered role: %v", err)
	}

	fullAdmin := uuid.New()
	rbacAdmin := uuid.New()
	bindings := &fakeCallerBindings{byUser: map[string][]rbac.RoleBinding{
		fullAdmin.String(): {{UserID: fullAdmin.String(), RoleRules: []rbac.Rule{{Resource: "*", Verbs: []string{"*"}}}}},
		rbacAdmin.String(): {{UserID: rbacAdmin.String(), RoleRules: []rbac.Rule{
			{Resource: "rbac", Verbs: []string{"*"}},
			{Resource: "users", Verbs: []string{"read", "list"}},
		}}},
	}}

	h := NewRBACHandler(q)
	h.SetAuthorization(engine, bindings)

	// Administrator edits a custom role to anything.
	if rec := putRole(t, h.UpdateGlobalRole, fullAdmin, custom.ID, `{"name":"custom-operator","rules":[{"resource":"*","verbs":["*"]}]}`); rec.Code != http.StatusOK {
		t.Fatalf("full admin update: status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if got := string(q.globalRoles[custom.ID].Rules); got != string(wildcardRuleJSON()) {
		t.Fatalf("full admin update did not persist rules: %s", got)
	}

	// RBAC Administrator edits a custom role within its own rule set.
	if rec := putRole(t, h.UpdateGlobalRole, rbacAdmin, custom.ID, `{"name":"custom-operator","rules":[{"resource":"rbac","verbs":["create"]}]}`); rec.Code != http.StatusOK {
		t.Fatalf("delegated rbac admin update: status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	// RBAC Administrator deletes a custom role above its own level.
	if rec := deleteRole(t, h.DeleteGlobalRole, rbacAdmin, overPowered.ID); rec.Code != http.StatusNoContent {
		t.Fatalf("delegated rbac admin delete: status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if _, ok := q.globalRoles[overPowered.ID]; ok {
		t.Fatal("custom role was not deleted")
	}
}

// TestShippedTemplatesUnaffectedByRoleWriteGuard pins the compatibility claim to
// the catalog itself: the role-CRUD routes carry no cluster_id/project_id URL
// param, so RequirePermission resolves them at global scope and only a global
// binding can reach them. Any *global*-scope template that grants rbac update or
// delete without holding "*" would, after this change, lose the ability to
// author rules beyond its own — i.e. a shipped role would regress. Today none
// does (project-owner's rbac:update is project-scoped and never reaches these
// handlers); this test fails if a future template changes that, so the loss is a
// deliberate decision rather than an accident.
func TestShippedTemplatesUnaffectedByRoleWriteGuard(t *testing.T) {
	catalog, err := rbac.LoadCatalog()
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	engine := rbac.NewEngine()
	for _, tpl := range catalog.All() {
		if tpl.Scope != rbac.ScopeGlobal {
			continue
		}
		held := []rbac.RoleBinding{{RoleRules: tpl.EffectiveRules()}}
		writesRoles := engine.CheckPermission(held, rbac.ResourceRBAC, rbac.VerbUpdate, uuid.UUID{}, uuid.UUID{}) ||
			engine.CheckPermission(held, rbac.ResourceRBAC, rbac.VerbDelete, uuid.UUID{}, uuid.UUID{})
		if !writesRoles {
			continue
		}
		if !engine.CheckPermission(held, "*", "*", uuid.UUID{}, uuid.UUID{}) {
			t.Fatalf("template %q grants global role writes without holding *:*; the role-write escalation guard narrows what it can author — confirm the loss is intended", tpl.Name)
		}
	}
}
