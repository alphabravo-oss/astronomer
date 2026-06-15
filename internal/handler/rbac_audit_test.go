package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type fakeRBACAuditQuerier struct {
	mu sync.Mutex

	globalRoles  map[uuid.UUID]sqlc.GlobalRole
	clusterRoles map[uuid.UUID]sqlc.ClusterRole
	projectRoles map[uuid.UUID]sqlc.ProjectRole

	globalBindings  map[uuid.UUID]sqlc.GlobalRoleBinding
	clusterBindings map[uuid.UUID]sqlc.ClusterRoleBinding
	projectBindings map[uuid.UUID]sqlc.ProjectRoleBinding

	auditRows []sqlc.CreateAuditLogV1Params
}

func newFakeRBACAuditQuerier() *fakeRBACAuditQuerier {
	return &fakeRBACAuditQuerier{
		globalRoles:     map[uuid.UUID]sqlc.GlobalRole{},
		clusterRoles:    map[uuid.UUID]sqlc.ClusterRole{},
		projectRoles:    map[uuid.UUID]sqlc.ProjectRole{},
		globalBindings:  map[uuid.UUID]sqlc.GlobalRoleBinding{},
		clusterBindings: map[uuid.UUID]sqlc.ClusterRoleBinding{},
		projectBindings: map[uuid.UUID]sqlc.ProjectRoleBinding{},
	}
}

func (f *fakeRBACAuditQuerier) CountGlobalRoles(context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return int64(len(f.globalRoles)), nil
}

func (f *fakeRBACAuditQuerier) CountClusterRoles(context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return int64(len(f.clusterRoles)), nil
}

func (f *fakeRBACAuditQuerier) CountProjectRoles(context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return int64(len(f.projectRoles)), nil
}

func (f *fakeRBACAuditQuerier) ListGlobalRoles(context.Context, sqlc.ListGlobalRolesParams) ([]sqlc.GlobalRole, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]sqlc.GlobalRole, 0, len(f.globalRoles))
	for _, role := range f.globalRoles {
		out = append(out, role)
	}
	return out, nil
}

func (f *fakeRBACAuditQuerier) ListClusterRoles(context.Context, sqlc.ListClusterRolesParams) ([]sqlc.ClusterRole, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]sqlc.ClusterRole, 0, len(f.clusterRoles))
	for _, role := range f.clusterRoles {
		out = append(out, role)
	}
	return out, nil
}

func (f *fakeRBACAuditQuerier) ListProjectRoles(context.Context, sqlc.ListProjectRolesParams) ([]sqlc.ProjectRole, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]sqlc.ProjectRole, 0, len(f.projectRoles))
	for _, role := range f.projectRoles {
		out = append(out, role)
	}
	return out, nil
}

func (f *fakeRBACAuditQuerier) GetGlobalRoleByID(_ context.Context, id uuid.UUID) (sqlc.GlobalRole, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	role, ok := f.globalRoles[id]
	if !ok {
		return sqlc.GlobalRole{}, pgx.ErrNoRows
	}
	return role, nil
}

func (f *fakeRBACAuditQuerier) GetClusterRoleByID(_ context.Context, id uuid.UUID) (sqlc.ClusterRole, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	role, ok := f.clusterRoles[id]
	if !ok {
		return sqlc.ClusterRole{}, pgx.ErrNoRows
	}
	return role, nil
}

func (f *fakeRBACAuditQuerier) GetProjectRoleByID(_ context.Context, id uuid.UUID) (sqlc.ProjectRole, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	role, ok := f.projectRoles[id]
	if !ok {
		return sqlc.ProjectRole{}, pgx.ErrNoRows
	}
	return role, nil
}

func (f *fakeRBACAuditQuerier) CreateGlobalRole(_ context.Context, arg sqlc.CreateGlobalRoleParams) (sqlc.GlobalRole, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	role := sqlc.GlobalRole{
		ID:          uuid.New(),
		Name:        arg.Name,
		DisplayName: arg.DisplayName,
		Description: arg.Description,
		Permissions: arg.Permissions,
		Rules:       arg.Rules,
		IsBuiltin:   arg.IsBuiltin,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	f.globalRoles[role.ID] = role
	return role, nil
}

func (f *fakeRBACAuditQuerier) CreateClusterRole(_ context.Context, arg sqlc.CreateClusterRoleParams) (sqlc.ClusterRole, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	role := sqlc.ClusterRole{
		ID:          uuid.New(),
		Name:        arg.Name,
		DisplayName: arg.DisplayName,
		Description: arg.Description,
		Permissions: arg.Permissions,
		Rules:       arg.Rules,
		IsBuiltin:   arg.IsBuiltin,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	f.clusterRoles[role.ID] = role
	return role, nil
}

func (f *fakeRBACAuditQuerier) CreateProjectRole(_ context.Context, arg sqlc.CreateProjectRoleParams) (sqlc.ProjectRole, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	role := sqlc.ProjectRole{
		ID:          uuid.New(),
		Name:        arg.Name,
		DisplayName: arg.DisplayName,
		Description: arg.Description,
		Permissions: arg.Permissions,
		Rules:       arg.Rules,
		IsBuiltin:   arg.IsBuiltin,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	f.projectRoles[role.ID] = role
	return role, nil
}

func (f *fakeRBACAuditQuerier) UpdateGlobalRole(_ context.Context, arg sqlc.UpdateGlobalRoleParams) (sqlc.GlobalRole, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	role, ok := f.globalRoles[arg.ID]
	if !ok {
		return sqlc.GlobalRole{}, pgx.ErrNoRows
	}
	role.Name = arg.Name
	role.DisplayName = arg.DisplayName
	role.Description = arg.Description
	role.Permissions = arg.Permissions
	role.Rules = arg.Rules
	role.UpdatedAt = time.Now()
	f.globalRoles[arg.ID] = role
	return role, nil
}

func (f *fakeRBACAuditQuerier) UpdateClusterRole(_ context.Context, arg sqlc.UpdateClusterRoleParams) (sqlc.ClusterRole, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	role, ok := f.clusterRoles[arg.ID]
	if !ok {
		return sqlc.ClusterRole{}, pgx.ErrNoRows
	}
	role.Name = arg.Name
	role.DisplayName = arg.DisplayName
	role.Description = arg.Description
	role.Permissions = arg.Permissions
	role.Rules = arg.Rules
	role.UpdatedAt = time.Now()
	f.clusterRoles[arg.ID] = role
	return role, nil
}

func (f *fakeRBACAuditQuerier) UpdateProjectRole(_ context.Context, arg sqlc.UpdateProjectRoleParams) (sqlc.ProjectRole, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	role, ok := f.projectRoles[arg.ID]
	if !ok {
		return sqlc.ProjectRole{}, pgx.ErrNoRows
	}
	role.Name = arg.Name
	role.DisplayName = arg.DisplayName
	role.Description = arg.Description
	role.Permissions = arg.Permissions
	role.Rules = arg.Rules
	role.UpdatedAt = time.Now()
	f.projectRoles[arg.ID] = role
	return role, nil
}

func (f *fakeRBACAuditQuerier) DeleteGlobalRole(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.globalRoles[id]; !ok {
		return pgx.ErrNoRows
	}
	delete(f.globalRoles, id)
	return nil
}

func (f *fakeRBACAuditQuerier) DeleteClusterRole(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.clusterRoles[id]; !ok {
		return pgx.ErrNoRows
	}
	delete(f.clusterRoles, id)
	return nil
}

func (f *fakeRBACAuditQuerier) DeleteProjectRole(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.projectRoles[id]; !ok {
		return pgx.ErrNoRows
	}
	delete(f.projectRoles, id)
	return nil
}

func (f *fakeRBACAuditQuerier) ListGlobalRoleBindings(context.Context, sqlc.ListGlobalRoleBindingsParams) ([]sqlc.GlobalRoleBinding, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]sqlc.GlobalRoleBinding, 0, len(f.globalBindings))
	for _, binding := range f.globalBindings {
		out = append(out, binding)
	}
	return out, nil
}

func (f *fakeRBACAuditQuerier) ListClusterRoleBindings(context.Context, sqlc.ListClusterRoleBindingsParams) ([]sqlc.ClusterRoleBinding, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]sqlc.ClusterRoleBinding, 0, len(f.clusterBindings))
	for _, binding := range f.clusterBindings {
		out = append(out, binding)
	}
	return out, nil
}

func (f *fakeRBACAuditQuerier) ListClusterRoleBindingsByCluster(_ context.Context, arg sqlc.ListClusterRoleBindingsByClusterParams) ([]sqlc.ClusterRoleBinding, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]sqlc.ClusterRoleBinding, 0, len(f.clusterBindings))
	for _, binding := range f.clusterBindings {
		if binding.ClusterID == arg.ClusterID {
			out = append(out, binding)
		}
	}
	return out, nil
}

func (f *fakeRBACAuditQuerier) ListProjectRoleBindings(context.Context, sqlc.ListProjectRoleBindingsParams) ([]sqlc.ProjectRoleBinding, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]sqlc.ProjectRoleBinding, 0, len(f.projectBindings))
	for _, binding := range f.projectBindings {
		out = append(out, binding)
	}
	return out, nil
}

func (f *fakeRBACAuditQuerier) ListProjectRoleBindingsByProject(_ context.Context, arg sqlc.ListProjectRoleBindingsByProjectParams) ([]sqlc.ProjectRoleBinding, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]sqlc.ProjectRoleBinding, 0, len(f.projectBindings))
	for _, binding := range f.projectBindings {
		if binding.ProjectID == arg.ProjectID {
			out = append(out, binding)
		}
	}
	return out, nil
}

func (f *fakeRBACAuditQuerier) GetGlobalRoleBindingByID(_ context.Context, id uuid.UUID) (sqlc.GlobalRoleBinding, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	binding, ok := f.globalBindings[id]
	if !ok {
		return sqlc.GlobalRoleBinding{}, pgx.ErrNoRows
	}
	return binding, nil
}

func (f *fakeRBACAuditQuerier) GetClusterRoleBindingByID(_ context.Context, id uuid.UUID) (sqlc.ClusterRoleBinding, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	binding, ok := f.clusterBindings[id]
	if !ok {
		return sqlc.ClusterRoleBinding{}, pgx.ErrNoRows
	}
	return binding, nil
}

func (f *fakeRBACAuditQuerier) GetProjectRoleBindingByID(_ context.Context, id uuid.UUID) (sqlc.ProjectRoleBinding, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	binding, ok := f.projectBindings[id]
	if !ok {
		return sqlc.ProjectRoleBinding{}, pgx.ErrNoRows
	}
	return binding, nil
}

func (f *fakeRBACAuditQuerier) CreateGlobalRoleBinding(_ context.Context, arg sqlc.CreateGlobalRoleBindingParams) (sqlc.GlobalRoleBinding, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	binding := sqlc.GlobalRoleBinding{
		ID:        uuid.New(),
		UserID:    arg.UserID,
		Group:     arg.Group,
		RoleID:    arg.RoleID,
		CreatedAt: now,
		UpdatedAt: now,
	}
	f.globalBindings[binding.ID] = binding
	return binding, nil
}

func (f *fakeRBACAuditQuerier) CreateClusterRoleBinding(_ context.Context, arg sqlc.CreateClusterRoleBindingParams) (sqlc.ClusterRoleBinding, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	binding := sqlc.ClusterRoleBinding{
		ID:        uuid.New(),
		UserID:    arg.UserID,
		Group:     arg.Group,
		RoleID:    arg.RoleID,
		ClusterID: arg.ClusterID,
		CreatedAt: now,
		UpdatedAt: now,
	}
	f.clusterBindings[binding.ID] = binding
	return binding, nil
}

func (f *fakeRBACAuditQuerier) CreateProjectRoleBinding(_ context.Context, arg sqlc.CreateProjectRoleBindingParams) (sqlc.ProjectRoleBinding, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	binding := sqlc.ProjectRoleBinding{
		ID:        uuid.New(),
		UserID:    arg.UserID,
		Group:     arg.Group,
		RoleID:    arg.RoleID,
		ProjectID: arg.ProjectID,
		CreatedAt: now,
		UpdatedAt: now,
	}
	f.projectBindings[binding.ID] = binding
	return binding, nil
}

func (f *fakeRBACAuditQuerier) DeleteGlobalRoleBinding(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.globalBindings[id]; !ok {
		return pgx.ErrNoRows
	}
	delete(f.globalBindings, id)
	return nil
}

func (f *fakeRBACAuditQuerier) DeleteClusterRoleBinding(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.clusterBindings[id]; !ok {
		return pgx.ErrNoRows
	}
	delete(f.clusterBindings, id)
	return nil
}

func (f *fakeRBACAuditQuerier) DeleteProjectRoleBinding(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.projectBindings[id]; !ok {
		return pgx.ErrNoRows
	}
	delete(f.projectBindings, id)
	return nil
}

func (f *fakeRBACAuditQuerier) CreateAuditLogV1(_ context.Context, arg sqlc.CreateAuditLogV1Params) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.auditRows = append(f.auditRows, arg)
	return nil
}

func (f *fakeRBACAuditQuerier) lastAuditRow() sqlc.CreateAuditLogV1Params {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.auditRows) == 0 {
		return sqlc.CreateAuditLogV1Params{}
	}
	return f.auditRows[len(f.auditRows)-1]
}

func TestRBACGlobalRoleMutationsAreAudited(t *testing.T) {
	callerID := uuid.New()
	q := newFakeRBACAuditQuerier()
	h := NewRBACHandler(q)

	createBody := []byte(`{"name":"platform-admin","display_name":"Platform Admin","rules":[]}`)
	createReq := authedRequest(http.MethodPost, "/api/v1/rbac/global-roles/", callerID, createBody)
	createRec := httptest.NewRecorder()
	h.CreateGlobalRole(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", createRec.Code, createRec.Body.String())
	}
	createAudit := requireRBACAudit(t, q, "role.create", "global_role")
	requireAuditDetail(t, createAudit, "scope", "global")
	roleID := uuid.MustParse(createAudit.ResourceID)

	updateBody := []byte(`{"name":"platform-owner","display_name":"Platform Owner","rules":[]}`)
	updateReq := withURLParam(
		authedRequest(http.MethodPut, "/api/v1/rbac/global-roles/"+roleID.String()+"/", callerID, updateBody),
		"id",
		roleID.String(),
	)
	updateRec := httptest.NewRecorder()
	h.UpdateGlobalRole(updateRec, updateReq)
	if updateRec.Code != http.StatusOK {
		t.Fatalf("update status = %d body=%s", updateRec.Code, updateRec.Body.String())
	}
	updateAudit := requireRBACAudit(t, q, "role.update", "global_role")
	if updateAudit.ResourceID != roleID.String() {
		t.Fatalf("update audit resource_id = %q, want %q", updateAudit.ResourceID, roleID.String())
	}
	requireAuditDetail(t, updateAudit, "scope", "global")

	deleteReq := withURLParam(
		authedRequest(http.MethodDelete, "/api/v1/rbac/global-roles/"+roleID.String()+"/", callerID, nil),
		"id",
		roleID.String(),
	)
	deleteRec := httptest.NewRecorder()
	h.DeleteGlobalRole(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d body=%s", deleteRec.Code, deleteRec.Body.String())
	}
	deleteAudit := requireRBACAudit(t, q, "role.delete", "global_role")
	if deleteAudit.ResourceID != roleID.String() {
		t.Fatalf("delete audit resource_id = %q, want %q", deleteAudit.ResourceID, roleID.String())
	}
	requireAuditDetail(t, deleteAudit, "scope", "global")
}

func TestRBACRoleBindingMutationsAreAudited(t *testing.T) {
	callerID := uuid.New()
	userID := uuid.New()
	roleID := uuid.New()
	clusterID := uuid.New()
	projectID := uuid.New()
	q := newFakeRBACAuditQuerier()
	h := NewRBACHandler(q)

	globalBody := []byte(fmt.Sprintf(`{"user_id":"%s","role_id":"%s"}`, userID, roleID))
	globalCreateReq := authedRequest(http.MethodPost, "/api/v1/rbac/global-bindings/", callerID, globalBody)
	globalCreateRec := httptest.NewRecorder()
	h.CreateGlobalRoleBinding(globalCreateRec, globalCreateReq)
	if globalCreateRec.Code != http.StatusCreated {
		t.Fatalf("global binding create status = %d body=%s", globalCreateRec.Code, globalCreateRec.Body.String())
	}
	globalCreateAudit := requireRBACAudit(t, q, "binding.create", "global_role_binding")
	requireAuditDetail(t, globalCreateAudit, "scope", "global")
	requireAuditDetail(t, globalCreateAudit, "role_id", roleID.String())
	globalBindingID := uuid.MustParse(globalCreateAudit.ResourceID)

	globalDeleteReq := withURLParam(
		authedRequest(http.MethodDelete, "/api/v1/rbac/global-bindings/"+globalBindingID.String()+"/", callerID, nil),
		"id",
		globalBindingID.String(),
	)
	globalDeleteRec := httptest.NewRecorder()
	h.DeleteGlobalRoleBinding(globalDeleteRec, globalDeleteReq)
	if globalDeleteRec.Code != http.StatusNoContent {
		t.Fatalf("global binding delete status = %d body=%s", globalDeleteRec.Code, globalDeleteRec.Body.String())
	}
	globalDeleteAudit := requireRBACAudit(t, q, "binding.delete", "global_role_binding")
	requireAuditDetail(t, globalDeleteAudit, "scope", "global")

	clusterBody := []byte(fmt.Sprintf(`{"user_id":"%s","role_id":"%s","cluster_id":"%s"}`, userID, roleID, clusterID))
	clusterCreateReq := authedRequest(http.MethodPost, "/api/v1/rbac/cluster-bindings/", callerID, clusterBody)
	clusterCreateRec := httptest.NewRecorder()
	h.CreateClusterRoleBinding(clusterCreateRec, clusterCreateReq)
	if clusterCreateRec.Code != http.StatusCreated {
		t.Fatalf("cluster binding create status = %d body=%s", clusterCreateRec.Code, clusterCreateRec.Body.String())
	}
	clusterCreateAudit := requireRBACAudit(t, q, "binding.create", "cluster_role_binding")
	requireAuditDetail(t, clusterCreateAudit, "scope", "cluster")
	requireAuditDetail(t, clusterCreateAudit, "cluster_id", clusterID.String())
	clusterBindingID := uuid.MustParse(clusterCreateAudit.ResourceID)

	clusterDeleteReq := withURLParam(
		authedRequest(http.MethodDelete, "/api/v1/rbac/cluster-bindings/"+clusterBindingID.String()+"/", callerID, nil),
		"id",
		clusterBindingID.String(),
	)
	clusterDeleteRec := httptest.NewRecorder()
	h.DeleteClusterRoleBinding(clusterDeleteRec, clusterDeleteReq)
	if clusterDeleteRec.Code != http.StatusNoContent {
		t.Fatalf("cluster binding delete status = %d body=%s", clusterDeleteRec.Code, clusterDeleteRec.Body.String())
	}
	clusterDeleteAudit := requireRBACAudit(t, q, "binding.delete", "cluster_role_binding")
	requireAuditDetail(t, clusterDeleteAudit, "scope", "cluster")

	projectBody := []byte(fmt.Sprintf(`{"user_id":"%s","role_id":"%s","project_id":"%s"}`, userID, roleID, projectID))
	projectCreateReq := authedRequest(http.MethodPost, "/api/v1/rbac/project-bindings/", callerID, projectBody)
	projectCreateRec := httptest.NewRecorder()
	h.CreateProjectRoleBinding(projectCreateRec, projectCreateReq)
	if projectCreateRec.Code != http.StatusCreated {
		t.Fatalf("project binding create status = %d body=%s", projectCreateRec.Code, projectCreateRec.Body.String())
	}
	projectCreateAudit := requireRBACAudit(t, q, "binding.create", "project_role_binding")
	requireAuditDetail(t, projectCreateAudit, "scope", "project")
	requireAuditDetail(t, projectCreateAudit, "project_id", projectID.String())
	projectBindingID := uuid.MustParse(projectCreateAudit.ResourceID)

	projectDeleteReq := withURLParam(
		authedRequest(http.MethodDelete, "/api/v1/rbac/project-bindings/"+projectBindingID.String()+"/", callerID, nil),
		"id",
		projectBindingID.String(),
	)
	projectDeleteRec := httptest.NewRecorder()
	h.DeleteProjectRoleBinding(projectDeleteRec, projectDeleteReq)
	if projectDeleteRec.Code != http.StatusNoContent {
		t.Fatalf("project binding delete status = %d body=%s", projectDeleteRec.Code, projectDeleteRec.Body.String())
	}
	projectDeleteAudit := requireRBACAudit(t, q, "binding.delete", "project_role_binding")
	requireAuditDetail(t, projectDeleteAudit, "scope", "project")
}

func requireRBACAudit(t *testing.T, q *fakeRBACAuditQuerier, action, resourceType string) sqlc.CreateAuditLogV1Params {
	t.Helper()
	row := q.lastAuditRow()
	if row.Action != action {
		t.Fatalf("audit action = %q, want %q; row=%+v", row.Action, action, row)
	}
	if row.ResourceType != resourceType {
		t.Fatalf("audit resource_type = %q, want %q; row=%+v", row.ResourceType, resourceType, row)
	}
	return row
}

func requireAuditDetail(t *testing.T, row sqlc.CreateAuditLogV1Params, key, want string) {
	t.Helper()
	var detail map[string]any
	if err := json.Unmarshal(row.Detail, &detail); err != nil {
		t.Fatalf("decode audit detail %s: %v", row.Detail, err)
	}
	got, ok := detail[key].(string)
	if !ok {
		t.Fatalf("audit detail[%q] = %#v, want string %q; detail=%v", key, detail[key], want, detail)
	}
	if got != want {
		t.Fatalf("audit detail[%q] = %q, want %q", key, got, want)
	}
}

var _ RBACQuerier = (*fakeRBACAuditQuerier)(nil)
var _ auditWriterV1 = (*fakeRBACAuditQuerier)(nil)
