package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

// fakeGroupMappings implements GroupMappingsQuerier with hand-rolled
// state plus the audit + group-sync surfaces. It mirrors the shape
// production *sqlc.Queries would satisfy, scoped to what the admin
// handler reaches for.
type fakeGroupMappings struct {
	user      sqlc.User
	userErr   error
	mappings  map[uuid.UUID]sqlc.IdentityGroupMapping
	createErr error
	deleteErr error

	// Snapshot of user_idp_groups (one row max in this test fake).
	snapshot    sqlc.UserIdpGroup
	snapshotErr error

	// Group-sync stub state mirrors the auth-package fake.
	global  map[uuid.UUID]sqlc.GlobalRoleBinding
	cluster map[uuid.UUID]sqlc.ClusterRoleBinding
	project map[uuid.UUID]sqlc.ProjectRoleBinding

	// CreateAuditLogV1 satisfies auditWriterV1; counted so the test
	// can assert on audit calls.
	auditCalls int
}

func newFakeMappings() *fakeGroupMappings {
	return &fakeGroupMappings{
		mappings: map[uuid.UUID]sqlc.IdentityGroupMapping{},
		global:   map[uuid.UUID]sqlc.GlobalRoleBinding{},
		cluster:  map[uuid.UUID]sqlc.ClusterRoleBinding{},
		project:  map[uuid.UUID]sqlc.ProjectRoleBinding{},
	}
}

// Compile-time guard that the fake satisfies the handler's interface.
var _ GroupMappingsQuerier = (*fakeGroupMappings)(nil)
var _ auth.GroupSyncQuerier = (*fakeGroupMappings)(nil)

func (f *fakeGroupMappings) GetUserByID(_ context.Context, _ uuid.UUID) (sqlc.User, error) {
	return f.user, f.userErr
}
func (f *fakeGroupMappings) CreateGroupMapping(_ context.Context, arg sqlc.CreateGroupMappingParams) (sqlc.IdentityGroupMapping, error) {
	if f.createErr != nil {
		return sqlc.IdentityGroupMapping{}, f.createErr
	}
	row := sqlc.IdentityGroupMapping{
		ID:          uuid.New(),
		ConnectorID: arg.ConnectorID,
		GroupName:   arg.GroupName,
		Scope:       arg.Scope,
		RoleID:      arg.RoleID,
		ClusterID:   arg.ClusterID,
		ProjectID:   arg.ProjectID,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	f.mappings[row.ID] = row
	return row, nil
}
func (f *fakeGroupMappings) GetGroupMappingByID(_ context.Context, id uuid.UUID) (sqlc.IdentityGroupMapping, error) {
	row, ok := f.mappings[id]
	if !ok {
		return sqlc.IdentityGroupMapping{}, pgx.ErrNoRows
	}
	return row, nil
}
func (f *fakeGroupMappings) ListGroupMappings(_ context.Context, _ sqlc.ListGroupMappingsParams) ([]sqlc.IdentityGroupMapping, error) {
	out := make([]sqlc.IdentityGroupMapping, 0, len(f.mappings))
	for _, m := range f.mappings {
		out = append(out, m)
	}
	return out, nil
}
func (f *fakeGroupMappings) CountGroupMappings(_ context.Context) (int64, error) {
	return int64(len(f.mappings)), nil
}
func (f *fakeGroupMappings) DeleteGroupMapping(_ context.Context, id uuid.UUID) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	delete(f.mappings, id)
	return nil
}

// --- auth.GroupSyncQuerier surface ----------------------------------

func (f *fakeGroupMappings) ListGroupMappingsForConnector(_ context.Context, connectorID pgtype.UUID) ([]sqlc.IdentityGroupMapping, error) {
	out := []sqlc.IdentityGroupMapping{}
	for _, m := range f.mappings {
		switch {
		case !m.ConnectorID.Valid:
			out = append(out, m)
		case connectorID.Valid && m.ConnectorID.Bytes == connectorID.Bytes:
			out = append(out, m)
		}
	}
	return out, nil
}
func (f *fakeGroupMappings) UpsertUserIDPGroups(_ context.Context, arg sqlc.UpsertUserIDPGroupsParams) (sqlc.UserIdpGroup, error) {
	f.snapshot = sqlc.UserIdpGroup(arg)
	return f.snapshot, nil
}
func (f *fakeGroupMappings) GetUserIDPGroups(_ context.Context, _ uuid.UUID) (sqlc.UserIdpGroup, error) {
	if f.snapshotErr != nil {
		return sqlc.UserIdpGroup{}, f.snapshotErr
	}
	return f.snapshot, nil
}
func (f *fakeGroupMappings) ListGroupSyncGlobalBindings(_ context.Context, userID pgtype.UUID) ([]sqlc.GlobalRoleBinding, error) {
	out := []sqlc.GlobalRoleBinding{}
	for _, b := range f.global {
		if b.UserID.Valid && userID.Valid && b.UserID.Bytes == userID.Bytes {
			out = append(out, b)
		}
	}
	return out, nil
}
func (f *fakeGroupMappings) ListGroupSyncClusterBindings(_ context.Context, userID pgtype.UUID) ([]sqlc.ClusterRoleBinding, error) {
	out := []sqlc.ClusterRoleBinding{}
	for _, b := range f.cluster {
		if b.UserID.Valid && userID.Valid && b.UserID.Bytes == userID.Bytes {
			out = append(out, b)
		}
	}
	return out, nil
}
func (f *fakeGroupMappings) ListGroupSyncProjectBindings(_ context.Context, userID pgtype.UUID) ([]sqlc.ProjectRoleBinding, error) {
	out := []sqlc.ProjectRoleBinding{}
	for _, b := range f.project {
		if b.UserID.Valid && userID.Valid && b.UserID.Bytes == userID.Bytes {
			out = append(out, b)
		}
	}
	return out, nil
}
func (f *fakeGroupMappings) CreateGroupSyncGlobalBinding(_ context.Context, arg sqlc.CreateGroupSyncGlobalBindingParams) (sqlc.GlobalRoleBinding, error) {
	row := sqlc.GlobalRoleBinding{
		ID:     uuid.New(),
		UserID: arg.UserID,
		RoleID: arg.RoleID,
		Source: "group_sync",
	}
	f.global[row.ID] = row
	return row, nil
}
func (f *fakeGroupMappings) CreateGroupSyncClusterBinding(_ context.Context, arg sqlc.CreateGroupSyncClusterBindingParams) (sqlc.ClusterRoleBinding, error) {
	row := sqlc.ClusterRoleBinding{
		ID:        uuid.New(),
		UserID:    arg.UserID,
		RoleID:    arg.RoleID,
		ClusterID: arg.ClusterID,
		Source:    "group_sync",
	}
	f.cluster[row.ID] = row
	return row, nil
}
func (f *fakeGroupMappings) CreateGroupSyncProjectBinding(_ context.Context, arg sqlc.CreateGroupSyncProjectBindingParams) (sqlc.ProjectRoleBinding, error) {
	row := sqlc.ProjectRoleBinding{
		ID:        uuid.New(),
		UserID:    arg.UserID,
		RoleID:    arg.RoleID,
		ProjectID: arg.ProjectID,
		Source:    "group_sync",
	}
	f.project[row.ID] = row
	return row, nil
}

// Connector-scoped variants (used by SyncUserGroups). Filter/stamp by connector
// mirroring `group_sync_connector_id IS NOT DISTINCT FROM`.
func gmConnMatch(row, arg pgtype.UUID) bool {
	if arg.Valid != row.Valid {
		return false
	}
	if !arg.Valid {
		return true
	}
	return row.Bytes == arg.Bytes
}
func (f *fakeGroupMappings) ListGroupSyncGlobalBindingsForConnector(_ context.Context, arg sqlc.ListGroupSyncGlobalBindingsForConnectorParams) ([]sqlc.GlobalRoleBinding, error) {
	out := []sqlc.GlobalRoleBinding{}
	for _, b := range f.global {
		if b.UserID.Valid && arg.UserID.Valid && b.UserID.Bytes == arg.UserID.Bytes && gmConnMatch(b.GroupSyncConnectorID, arg.GroupSyncConnectorID) {
			out = append(out, b)
		}
	}
	return out, nil
}
func (f *fakeGroupMappings) ListGroupSyncClusterBindingsForConnector(_ context.Context, arg sqlc.ListGroupSyncClusterBindingsForConnectorParams) ([]sqlc.ClusterRoleBinding, error) {
	out := []sqlc.ClusterRoleBinding{}
	for _, b := range f.cluster {
		if b.UserID.Valid && arg.UserID.Valid && b.UserID.Bytes == arg.UserID.Bytes && gmConnMatch(b.GroupSyncConnectorID, arg.GroupSyncConnectorID) {
			out = append(out, b)
		}
	}
	return out, nil
}
func (f *fakeGroupMappings) ListGroupSyncProjectBindingsForConnector(_ context.Context, arg sqlc.ListGroupSyncProjectBindingsForConnectorParams) ([]sqlc.ProjectRoleBinding, error) {
	out := []sqlc.ProjectRoleBinding{}
	for _, b := range f.project {
		if b.UserID.Valid && arg.UserID.Valid && b.UserID.Bytes == arg.UserID.Bytes && gmConnMatch(b.GroupSyncConnectorID, arg.GroupSyncConnectorID) {
			out = append(out, b)
		}
	}
	return out, nil
}
func (f *fakeGroupMappings) CreateGroupSyncGlobalBindingForConnector(_ context.Context, arg sqlc.CreateGroupSyncGlobalBindingForConnectorParams) (sqlc.GlobalRoleBinding, error) {
	row := sqlc.GlobalRoleBinding{ID: uuid.New(), UserID: arg.UserID, RoleID: arg.RoleID, Source: "group_sync", GroupSyncConnectorID: arg.GroupSyncConnectorID}
	f.global[row.ID] = row
	return row, nil
}
func (f *fakeGroupMappings) CreateGroupSyncClusterBindingForConnector(_ context.Context, arg sqlc.CreateGroupSyncClusterBindingForConnectorParams) (sqlc.ClusterRoleBinding, error) {
	row := sqlc.ClusterRoleBinding{ID: uuid.New(), UserID: arg.UserID, RoleID: arg.RoleID, ClusterID: arg.ClusterID, Source: "group_sync", GroupSyncConnectorID: arg.GroupSyncConnectorID}
	f.cluster[row.ID] = row
	return row, nil
}
func (f *fakeGroupMappings) CreateGroupSyncProjectBindingForConnector(_ context.Context, arg sqlc.CreateGroupSyncProjectBindingForConnectorParams) (sqlc.ProjectRoleBinding, error) {
	row := sqlc.ProjectRoleBinding{ID: uuid.New(), UserID: arg.UserID, RoleID: arg.RoleID, ProjectID: arg.ProjectID, Source: "group_sync", GroupSyncConnectorID: arg.GroupSyncConnectorID}
	f.project[row.ID] = row
	return row, nil
}
func (f *fakeGroupMappings) DeleteGroupSyncGlobalBinding(_ context.Context, id uuid.UUID) error {
	delete(f.global, id)
	return nil
}
func (f *fakeGroupMappings) DeleteGroupSyncClusterBinding(_ context.Context, id uuid.UUID) error {
	delete(f.cluster, id)
	return nil
}
func (f *fakeGroupMappings) DeleteGroupSyncProjectBinding(_ context.Context, id uuid.UUID) error {
	delete(f.project, id)
	return nil
}

// CreateAuditLogV1 satisfies auditWriterV1 (the v1 audit path the
// handler's recordAudit looks up via type assertion).
func (f *fakeGroupMappings) CreateAuditLogV1(_ context.Context, _ sqlc.CreateAuditLogV1Params) error {
	f.auditCalls++
	return nil
}

// makeAuthedRequest builds a *http.Request with an authenticated user
// context attached. The optional URL param under "id" is for path-
// parameter handlers (Get/Delete/ResyncUser).
func makeAuthedRequest(t *testing.T, method, target string, body []byte, callerID uuid.UUID, pathID string) *http.Request {
	t.Helper()
	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(method, target, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, target, nil)
	}
	ctx := middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{
		ID:         callerID.String(),
		AuthMethod: "jwt",
	})
	if pathID != "" {
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", pathID)
		ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)
	}
	return req.WithContext(ctx)
}

// TestGroupMappingsHandler_CRUD walks the full create/list/get/delete
// path with a superuser. The audit call counter doubles as a check
// that the recordAudit helper actually fires.
func TestGroupMappingsHandler_CRUD(t *testing.T) {
	caller := uuid.New()
	f := newFakeMappings()
	f.user = sqlc.User{ID: caller, IsSuperuser: true}

	h := NewGroupMappingsHandler(f)

	// Create
	body := []byte(`{"group_name":"engineering","scope":"global","role_id":"` + uuid.New().String() + `"}`)
	rec := httptest.NewRecorder()
	h.Create(rec, makeAuthedRequest(t, http.MethodPost, "/api/v1/admin/group-mappings/", body, caller, ""))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: code = %d, body = %s", rec.Code, rec.Body.String())
	}
	var createResp struct {
		Data GroupMappingResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if createResp.Data.GroupName != "engineering" {
		t.Fatalf("create group_name = %q", createResp.Data.GroupName)
	}
	if createResp.Data.Scope != "global" {
		t.Fatalf("create scope = %q", createResp.Data.Scope)
	}
	createdID := createResp.Data.ID

	// List
	rec = httptest.NewRecorder()
	h.List(rec, makeAuthedRequest(t, http.MethodGet, "/api/v1/admin/group-mappings/", nil, caller, ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("list: code = %d", rec.Code)
	}
	var listResp struct {
		Data  []GroupMappingResponse `json:"data"`
		Count int64                  `json:"count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if listResp.Count != 1 || len(listResp.Data) != 1 {
		t.Fatalf("list count=%d items=%d", listResp.Count, len(listResp.Data))
	}

	// Get
	rec = httptest.NewRecorder()
	h.Get(rec, makeAuthedRequest(t, http.MethodGet, "/api/v1/admin/group-mappings/"+createdID+"/", nil, caller, createdID))
	if rec.Code != http.StatusOK {
		t.Fatalf("get: code = %d", rec.Code)
	}

	// Delete
	rec = httptest.NewRecorder()
	h.Delete(rec, makeAuthedRequest(t, http.MethodDelete, "/api/v1/admin/group-mappings/"+createdID+"/", nil, caller, createdID))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: code = %d", rec.Code)
	}

	// 404 on a subsequent get
	rec = httptest.NewRecorder()
	h.Get(rec, makeAuthedRequest(t, http.MethodGet, "/api/v1/admin/group-mappings/"+createdID+"/", nil, caller, createdID))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("get after delete: code = %d", rec.Code)
	}

	// At least the created + deleted audit rows should have landed.
	if f.auditCalls < 2 {
		t.Fatalf("audit calls = %d, want >= 2", f.auditCalls)
	}
}

// TestGroupMappingsHandler_RequiresSuperuser asserts a non-superuser
// caller is bounced with 403 instead of leaking the underlying data.
func TestGroupMappingsHandler_RequiresSuperuser(t *testing.T) {
	caller := uuid.New()
	f := newFakeMappings()
	f.user = sqlc.User{ID: caller, IsSuperuser: false}
	h := NewGroupMappingsHandler(f)

	endpoints := []struct {
		name string
		fn   func(http.ResponseWriter, *http.Request)
	}{
		{"List", h.List},
		{"Get", h.Get},
		{"Create", h.Create},
		{"Delete", h.Delete},
		{"ResyncUser", h.ResyncUser},
	}
	for _, ep := range endpoints {
		rec := httptest.NewRecorder()
		req := makeAuthedRequest(t, http.MethodGet, "/api/v1/admin/group-mappings/", nil, caller, uuid.New().String())
		ep.fn(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s: code = %d, want 403; body=%s", ep.name, rec.Code, rec.Body.String())
		}
	}
}

// TestGroupMappingsHandler_CreateValidation checks the scope vs.
// scoped-id matrix that the handler rejects before the DB CHECK
// would.
func TestGroupMappingsHandler_CreateValidation(t *testing.T) {
	caller := uuid.New()
	f := newFakeMappings()
	f.user = sqlc.User{ID: caller, IsSuperuser: true}
	h := NewGroupMappingsHandler(f)

	cases := []struct {
		name string
		body string
		want int
	}{
		{"missing group_name", `{"scope":"global","role_id":"` + uuid.New().String() + `"}`, http.StatusBadRequest},
		{"invalid scope", `{"group_name":"x","scope":"weird","role_id":"` + uuid.New().String() + `"}`, http.StatusBadRequest},
		{"cluster scope without cluster_id", `{"group_name":"x","scope":"cluster","role_id":"` + uuid.New().String() + `"}`, http.StatusBadRequest},
		{"project scope without project_id", `{"group_name":"x","scope":"project","role_id":"` + uuid.New().String() + `"}`, http.StatusBadRequest},
		{"global with cluster_id", `{"group_name":"x","scope":"global","role_id":"` + uuid.New().String() + `","cluster_id":"` + uuid.New().String() + `"}`, http.StatusBadRequest},
		{"cluster scope with project_id", `{"group_name":"x","scope":"cluster","role_id":"` + uuid.New().String() + `","cluster_id":"` + uuid.New().String() + `","project_id":"` + uuid.New().String() + `"}`, http.StatusBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.Create(rec, makeAuthedRequest(t, http.MethodPost, "/api/v1/admin/group-mappings/", []byte(c.body), caller, ""))
			if rec.Code != c.want {
				t.Fatalf("code = %d, want %d, body=%s", rec.Code, c.want, rec.Body.String())
			}
		})
	}
}

// TestGroupMappingsHandler_ResyncUser exercises the admin resync path
// using a pre-seeded user_idp_groups snapshot.
func TestGroupMappingsHandler_ResyncUser(t *testing.T) {
	caller := uuid.New()
	target := uuid.New()
	connectorID := uuid.New()
	roleID := uuid.New()

	f := newFakeMappings()
	f.user = sqlc.User{ID: caller, IsSuperuser: true}

	// Pretend an SSO login earlier wrote the snapshot.
	f.snapshot = sqlc.UserIdpGroup{
		UserID:      target,
		ConnectorID: pgtype.UUID{Bytes: connectorID, Valid: true},
		Groups:      json.RawMessage(`["engineering"]`),
		SyncedAt:    time.Now().UTC(),
	}
	// And the operator has just added a mapping that matches that
	// group. We bypass the Create handler here so the audit-count
	// assertion below only counts the resync's emissions.
	f.mappings[uuid.New()] = sqlc.IdentityGroupMapping{
		ID:          uuid.New(),
		ConnectorID: pgtype.UUID{Bytes: connectorID, Valid: true},
		GroupName:   "engineering",
		Scope:       "global",
		RoleID:      roleID,
	}

	// We need to fix GetUserByID to also return the target user.
	// The fake currently returns `f.user` regardless of ID; that's
	// fine — the handler uses the returned ID to drive sync, not the
	// param, so we patch f.user temporarily.
	f.user = sqlc.User{ID: target, IsSuperuser: true}

	h := NewGroupMappingsHandler(f)
	rec := httptest.NewRecorder()
	h.ResyncUser(rec, makeAuthedRequest(t, http.MethodPost,
		"/api/v1/admin/users/"+target.String()+"/resync-groups/",
		nil, caller, target.String()))
	if rec.Code != http.StatusOK {
		t.Fatalf("resync: code = %d body=%s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["success"] != true {
		t.Fatalf("response success = %v", got["success"])
	}
	if got["added_count"].(float64) != 1 {
		t.Fatalf("added_count = %v, want 1", got["added_count"])
	}
	if len(f.global) != 1 {
		t.Fatalf("global bindings = %d, want 1", len(f.global))
	}
}

// TestGroupMappingsHandler_ResyncUser_NoSnapshot returns 409 with a
// helpful error string when the user has never logged in via SSO.
func TestGroupMappingsHandler_ResyncUser_NoSnapshot(t *testing.T) {
	caller := uuid.New()
	target := uuid.New()
	f := newFakeMappings()
	f.user = sqlc.User{ID: target, IsSuperuser: true}
	f.snapshotErr = pgx.ErrNoRows

	h := NewGroupMappingsHandler(f)
	rec := httptest.NewRecorder()
	h.ResyncUser(rec, makeAuthedRequest(t, http.MethodPost,
		"/api/v1/admin/users/"+target.String()+"/resync-groups/",
		nil, caller, target.String()))
	if rec.Code != http.StatusConflict {
		t.Fatalf("code = %d, want 409", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "no_snapshot") {
		t.Fatalf("body missing no_snapshot error: %s", rec.Body.String())
	}
}
