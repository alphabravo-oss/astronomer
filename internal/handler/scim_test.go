package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
)

// fakeSCIMQuerier is the minimal SCIMQuerier the test needs: an in-memory
// user store keyed by username + a single valid token hash.
type fakeSCIMQuerier struct {
	tokenHash string
	users     map[string]sqlc.User // username -> row
	// lookupErr, when non-nil, is returned by GetUserByUsername to
	// simulate a transient DB failure (not a "no rows" miss).
	lookupErr error
	// invalidatedTokensFor records user ids passed to InvalidateAllTokens so
	// tests can assert SCIM deactivate/deprovision revoked live sessions.
	invalidatedTokensFor []uuid.UUID
	// groups maps displayName -> mapping row for DIR-03 SCIM Group writes.
	groups    map[string]sqlc.IdentityGroupMapping
	idpGroups map[uuid.UUID]sqlc.UserIdpGroup
	outbox    []sqlc.UpsertAuditOutboxParams
	outboxErr error
	idpErr    error
}

func (f *fakeSCIMQuerier) clone() *fakeSCIMQuerier {
	cloned := *f
	cloned.users = make(map[string]sqlc.User, len(f.users))
	for key, value := range f.users {
		cloned.users[key] = value
	}
	cloned.groups = make(map[string]sqlc.IdentityGroupMapping, len(f.groups))
	for key, value := range f.groups {
		cloned.groups[key] = value
	}
	cloned.idpGroups = make(map[uuid.UUID]sqlc.UserIdpGroup, len(f.idpGroups))
	for key, value := range f.idpGroups {
		value.Groups = append([]byte(nil), value.Groups...)
		cloned.idpGroups[key] = value
	}
	cloned.invalidatedTokensFor = append([]uuid.UUID(nil), f.invalidatedTokensFor...)
	cloned.outbox = append([]sqlc.UpsertAuditOutboxParams(nil), f.outbox...)
	return &cloned
}

func (f *fakeSCIMQuerier) runTx(_ context.Context, fn func(SCIMMutationTx) error) error {
	tx := f.clone()
	if err := fn(tx); err != nil {
		return err
	}
	*f = *tx
	return nil
}

func newTestSCIMHandler(q *fakeSCIMQuerier) *SCIMHandler {
	h := NewSCIMHandler(q)
	h.SetRunTx(q.runTx)
	return h
}

func (f *fakeSCIMQuerier) InvalidateAllTokens(_ context.Context, arg sqlc.InvalidateAllTokensParams) error {
	f.invalidatedTokensFor = append(f.invalidatedTokensFor, arg.ID)
	return nil
}

func (f *fakeSCIMQuerier) GetSCIMTokenByHash(_ context.Context, hash string) (sqlc.ScimToken, error) {
	if hash == f.tokenHash {
		return sqlc.ScimToken{ID: uuid.New(), ExpiresAt: time.Now().Add(time.Hour)}, nil
	}
	return sqlc.ScimToken{}, pgx.ErrNoRows
}

func (f *fakeSCIMQuerier) TouchSCIMToken(_ context.Context, _ uuid.UUID) error { return nil }

func (f *fakeSCIMQuerier) CreateUser(_ context.Context, arg sqlc.CreateUserParams) (sqlc.User, error) {
	if _, ok := f.users[arg.Username]; ok {
		return sqlc.User{}, pgx.ErrTxClosed // any non-nil error => 409
	}
	u := sqlc.User{
		ID:        uuid.New(),
		Email:     arg.Email,
		Username:  arg.Username,
		FirstName: arg.FirstName,
		LastName:  arg.LastName,
		IsActive:  arg.IsActive,
	}
	f.users[arg.Username] = u
	return u, nil
}

func (f *fakeSCIMQuerier) UpdateUser(_ context.Context, arg sqlc.UpdateUserParams) (sqlc.User, error) {
	for k, u := range f.users {
		if u.ID == arg.ID {
			u.Email = arg.Email
			u.Username = arg.Username
			u.FirstName = arg.FirstName
			u.LastName = arg.LastName
			u.IsActive = arg.IsActive
			// Re-key in case the username changed.
			delete(f.users, k)
			f.users[u.Username] = u
			return u, nil
		}
	}
	return sqlc.User{}, pgx.ErrNoRows
}

func (f *fakeSCIMQuerier) GetUserByID(_ context.Context, id uuid.UUID) (sqlc.User, error) {
	for _, u := range f.users {
		if u.ID == id {
			return u, nil
		}
	}
	return sqlc.User{}, pgx.ErrNoRows
}

func (f *fakeSCIMQuerier) GetUserByIDForUpdate(ctx context.Context, id uuid.UUID) (sqlc.User, error) {
	return f.GetUserByID(ctx, id)
}

func (f *fakeSCIMQuerier) GetUserByUsername(_ context.Context, username string) (sqlc.User, error) {
	if f.lookupErr != nil {
		return sqlc.User{}, f.lookupErr
	}
	if u, ok := f.users[username]; ok {
		return u, nil
	}
	return sqlc.User{}, pgx.ErrNoRows
}

func (f *fakeSCIMQuerier) GetUserByEmail(_ context.Context, _ string) (sqlc.User, error) {
	return sqlc.User{}, pgx.ErrNoRows
}

func (f *fakeSCIMQuerier) ListUsers(_ context.Context, _ sqlc.ListUsersParams) ([]sqlc.User, error) {
	out := make([]sqlc.User, 0, len(f.users))
	for _, u := range f.users {
		out = append(out, u)
	}
	return out, nil
}

func (f *fakeSCIMQuerier) CountUsers(_ context.Context) (int64, error) {
	return int64(len(f.users)), nil
}

func (f *fakeSCIMQuerier) DeleteUser(_ context.Context, id uuid.UUID) error {
	for k, u := range f.users {
		if u.ID == id {
			delete(f.users, k)
			return nil
		}
	}
	return nil
}

func (f *fakeSCIMQuerier) ListSCIMGroupNames(_ context.Context, _ sqlc.ListSCIMGroupNamesParams) ([]string, error) {
	names := make([]string, 0, len(f.groups))
	for n := range f.groups {
		names = append(names, n)
	}
	return names, nil
}

func (f *fakeSCIMQuerier) CountSCIMGroupNames(_ context.Context) (int64, error) {
	return int64(len(f.groups)), nil
}

func (f *fakeSCIMQuerier) CreateGroupMapping(_ context.Context, arg sqlc.CreateGroupMappingParams) (sqlc.IdentityGroupMapping, error) {
	if f.groups == nil {
		f.groups = map[string]sqlc.IdentityGroupMapping{}
	}
	row := sqlc.IdentityGroupMapping{
		ID:        uuid.New(),
		GroupName: arg.GroupName,
		Scope:     arg.Scope,
		RoleID:    arg.RoleID,
	}
	f.groups[arg.GroupName] = row
	return row, nil
}

func (f *fakeSCIMQuerier) ListGroupMappings(_ context.Context, _ sqlc.ListGroupMappingsParams) ([]sqlc.IdentityGroupMapping, error) {
	out := make([]sqlc.IdentityGroupMapping, 0, len(f.groups))
	for _, g := range f.groups {
		out = append(out, g)
	}
	return out, nil
}

func (f *fakeSCIMQuerier) DeleteGroupMapping(_ context.Context, id uuid.UUID) error {
	for k, g := range f.groups {
		if g.ID == id {
			delete(f.groups, k)
			return nil
		}
	}
	return nil
}

func (f *fakeSCIMQuerier) ListGlobalRoles(_ context.Context, _ sqlc.ListGlobalRolesParams) ([]sqlc.GlobalRole, error) {
	return []sqlc.GlobalRole{{ID: uuid.MustParse("00000000-0000-0000-0000-0000000000aa"), Name: "Auditor"}}, nil
}

func (f *fakeSCIMQuerier) GetUserIDPGroups(_ context.Context, userID uuid.UUID) (sqlc.UserIdpGroup, error) {
	if f.idpGroups == nil {
		return sqlc.UserIdpGroup{}, pgx.ErrNoRows
	}
	if g, ok := f.idpGroups[userID]; ok {
		return g, nil
	}
	return sqlc.UserIdpGroup{}, pgx.ErrNoRows
}

func (f *fakeSCIMQuerier) UpsertUserIDPGroups(_ context.Context, arg sqlc.UpsertUserIDPGroupsParams) (sqlc.UserIdpGroup, error) {
	if f.idpErr != nil {
		return sqlc.UserIdpGroup{}, f.idpErr
	}
	if f.idpGroups == nil {
		f.idpGroups = map[uuid.UUID]sqlc.UserIdpGroup{}
	}
	row := sqlc.UserIdpGroup{UserID: arg.UserID, Groups: arg.Groups, SyncedAt: arg.SyncedAt}
	f.idpGroups[arg.UserID] = row
	return row, nil
}

func (f *fakeSCIMQuerier) UpsertAuditOutbox(_ context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	if f.outboxErr != nil {
		return sqlc.AuditOutbox{}, f.outboxErr
	}
	f.outbox = append(f.outbox, arg)
	return sqlc.AuditOutbox{ID: arg.ID, DedupeKey: arg.DedupeKey}, nil
}

// TestSCIMUserLifecycle exercises the smallest end-to-end slice: a bad
// token is rejected with 401, and a valid token can create then read
// back a user. This fails if the bearer-auth wiring or the SCIM user
// mapping breaks.
func TestSCIMUserLifecycle(t *testing.T) {
	token := "astro_scim_testtoken"
	q := &fakeSCIMQuerier{
		tokenHash: auth.HashSCIMToken(token),
		users:     map[string]sqlc.User{},
	}
	h := newTestSCIMHandler(q)

	// --- 401 on missing/bad token (Auth middleware) ---
	badReq := httptest.NewRequest(http.MethodGet, "/scim/v2/Users", nil)
	badRec := httptest.NewRecorder()
	h.Auth(http.HandlerFunc(h.ListUsers)).ServeHTTP(badRec, badReq)
	if badRec.Code != http.StatusUnauthorized {
		t.Fatalf("missing token: want 401, got %d", badRec.Code)
	}

	authed := func(next http.HandlerFunc) http.Handler { return h.Auth(next) }
	withToken := func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }

	// --- create ---
	body := `{"userName":"alice@example.com","name":{"givenName":"Alice","familyName":"A"},"emails":[{"value":"alice@example.com","primary":true}],"active":true}`
	cReq := httptest.NewRequest(http.MethodPost, "/scim/v2/Users", strings.NewReader(body))
	withToken(cReq)
	cRec := httptest.NewRecorder()
	authed(h.CreateUser).ServeHTTP(cRec, cReq)
	if cRec.Code != http.StatusCreated {
		t.Fatalf("create: want 201, got %d (%s)", cRec.Code, cRec.Body.String())
	}
	var created scimUser
	if err := json.Unmarshal(cRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.UserName != "alice@example.com" || created.ID == "" || !created.Active {
		t.Fatalf("unexpected created user: %+v", created)
	}
	if len(created.Schemas) != 1 || created.Schemas[0] != scimUserSchema {
		t.Fatalf("missing/incorrect SCIM user schema: %+v", created.Schemas)
	}

	// --- list reflects the created user ---
	lReq := httptest.NewRequest(http.MethodGet, "/scim/v2/Users", nil)
	withToken(lReq)
	lRec := httptest.NewRecorder()
	authed(h.ListUsers).ServeHTTP(lRec, lReq)
	if lRec.Code != http.StatusOK {
		t.Fatalf("list: want 200, got %d", lRec.Code)
	}
	var list scimListResponse
	if err := json.Unmarshal(lRec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if list.TotalResults != 1 || len(list.Resources) != 1 {
		t.Fatalf("list: want 1 user, got total=%d resources=%d", list.TotalResults, len(list.Resources))
	}

	// --- PUT active:false deactivates ---
	deReq := httptest.NewRequest(http.MethodPut, "/scim/v2/Users/"+created.ID,
		strings.NewReader(`{"userName":"alice@example.com","active":false}`))
	withToken(deReq)
	deReq = withChiID(deReq, created.ID)
	deRec := httptest.NewRecorder()
	authed(h.PutUser).ServeHTTP(deRec, deReq)
	if deRec.Code != http.StatusOK {
		t.Fatalf("deactivate: want 200, got %d (%s)", deRec.Code, deRec.Body.String())
	}
	var deactivated scimUser
	if err := json.Unmarshal(deRec.Body.Bytes(), &deactivated); err != nil {
		t.Fatalf("decode deactivate response: %v", err)
	}
	if deactivated.Active {
		t.Fatalf("deactivate: want active=false, got true")
	}
	if got := q.users["alice@example.com"]; got.IsActive {
		t.Fatalf("deactivate: stored user still is_active=true")
	}

	// --- PUT active:true reactivates ---
	reReq := httptest.NewRequest(http.MethodPut, "/scim/v2/Users/"+created.ID,
		strings.NewReader(`{"userName":"alice@example.com","active":true}`))
	withToken(reReq)
	reReq = withChiID(reReq, created.ID)
	reRec := httptest.NewRecorder()
	authed(h.PutUser).ServeHTTP(reRec, reReq)
	if reRec.Code != http.StatusOK {
		t.Fatalf("reactivate: want 200, got %d (%s)", reRec.Code, reRec.Body.String())
	}
	var reactivated scimUser
	if err := json.Unmarshal(reRec.Body.Bytes(), &reactivated); err != nil {
		t.Fatalf("decode reactivate response: %v", err)
	}
	if !reactivated.Active {
		t.Fatalf("reactivate: want active=true, got false")
	}

	// --- idempotent re-POST with active:false also deactivates ---
	rpReq := httptest.NewRequest(http.MethodPost, "/scim/v2/Users",
		strings.NewReader(`{"userName":"alice@example.com","active":false}`))
	withToken(rpReq)
	rpRec := httptest.NewRecorder()
	authed(h.CreateUser).ServeHTTP(rpRec, rpReq)
	if rpRec.Code != http.StatusOK {
		t.Fatalf("re-POST: want 200, got %d (%s)", rpRec.Code, rpRec.Body.String())
	}
	if got := q.users["alice@example.com"]; got.IsActive {
		t.Fatalf("re-POST active:false: stored user still is_active=true")
	}
}

// TestSCIMListUsersFilterDBError asserts that a transient DB error in the
// userName-filter branch of ListUsers surfaces as a 500, rather than being
// swallowed into an empty 200 that an IdP would read as "user not found".
func TestSCIMListUsersFilterDBError(t *testing.T) {
	token := "astro_scim_testtoken"
	q := &fakeSCIMQuerier{
		tokenHash: auth.HashSCIMToken(token),
		users:     map[string]sqlc.User{},
		lookupErr: pgx.ErrTxClosed, // any non-ErrNoRows error => transient failure
	}
	h := newTestSCIMHandler(q)

	req := httptest.NewRequest(http.MethodGet, "/scim/v2/Users?filter="+
		url.QueryEscape(`userName eq "alice@example.com"`), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.Auth(http.HandlerFunc(h.ListUsers)).ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("filter DB error: want 500, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestSCIMPatchUser exercises PATCH /scim/v2/Users/{id} for the operations
// real IdPs (Okta/Azure AD) emit: replace active:false (deactivate), replace
// active:true (reactivate), replace displayName, and a path-less merge.
func TestSCIMPatchUser(t *testing.T) {
	token := "astro_scim_testtoken"
	q := &fakeSCIMQuerier{
		tokenHash: auth.HashSCIMToken(token),
		users:     map[string]sqlc.User{},
	}
	h := newTestSCIMHandler(q)

	// Seed a user directly through CreateUser so we have a real id.
	u, err := q.CreateUser(context.Background(), sqlc.CreateUserParams{
		Email: "bob@example.com", Username: "bob@example.com",
		FirstName: "Bob", LastName: "B", IsActive: true,
	})
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	id := u.ID.String()

	patch := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPatch, "/scim/v2/Users/"+id, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req = withChiID(req, id)
		rec := httptest.NewRecorder()
		h.Auth(http.HandlerFunc(h.PatchUser)).ServeHTTP(rec, req)
		return rec
	}

	// --- replace active:false deactivates (path-set, the Okta form) ---
	rec := patch(`{"schemas":["urn:ietf:params:scim:api:messages:2.0:PatchOp"],"Operations":[{"op":"replace","path":"active","value":false}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("deactivate: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got scimUser
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Active {
		t.Fatalf("deactivate: response active=true, want false")
	}
	if q.users["bob@example.com"].IsActive {
		t.Fatalf("deactivate: stored user still is_active=true")
	}

	// --- replace active:true reactivates (case-insensitive op) ---
	rec = patch(`{"Operations":[{"op":"Replace","path":"active","value":true}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("reactivate: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if !q.users["bob@example.com"].IsActive {
		t.Fatalf("reactivate: stored user still is_active=false")
	}

	// --- replace displayName updates the name ---
	rec = patch(`{"Operations":[{"op":"replace","path":"displayName","value":"Robert Builder"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("displayName: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Name.GivenName != "Robert" || got.Name.FamilyName != "Builder" {
		t.Fatalf("displayName: got name %+v, want Robert/Builder", got.Name)
	}

	// --- path-less replace merges a partial User object (Azure AD form) ---
	rec = patch(`{"Operations":[{"op":"replace","value":{"active":false,"name":{"givenName":"Bobby","familyName":"X"}}}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("pathless: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	stored := q.users["bob@example.com"]
	if stored.IsActive {
		t.Fatalf("pathless: stored user still is_active=true")
	}
	if stored.FirstName != "Bobby" || stored.LastName != "X" {
		t.Fatalf("pathless: got name %s/%s, want Bobby/X", stored.FirstName, stored.LastName)
	}
}

// TestSCIMPatchUserRejectsBadSchema asserts an explicit non-PatchOp schema is
// a 400, not a silent misapply.
func TestSCIMPatchUserRejectsBadSchema(t *testing.T) {
	token := "astro_scim_testtoken"
	q := &fakeSCIMQuerier{tokenHash: auth.HashSCIMToken(token), users: map[string]sqlc.User{}}
	h := newTestSCIMHandler(q)
	u, _ := q.CreateUser(context.Background(), sqlc.CreateUserParams{
		Email: "c@example.com", Username: "c@example.com", IsActive: true,
	})
	id := u.ID.String()

	req := httptest.NewRequest(http.MethodPatch, "/scim/v2/Users/"+id,
		strings.NewReader(`{"schemas":["urn:ietf:params:scim:schemas:core:2.0:User"],"Operations":[]}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req = withChiID(req, id)
	rec := httptest.NewRecorder()
	h.Auth(http.HandlerFunc(h.PatchUser)).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad schema: want 400, got %d", rec.Code)
	}
}

// TestSCIMCreateGroup writes an identity_group_mappings row (DIR-03).
func TestSCIMCreateGroup(t *testing.T) {
	token := "astro_scim_grouptoken"
	q := &fakeSCIMQuerier{
		tokenHash: auth.HashSCIMToken(token),
		users:     map[string]sqlc.User{},
		groups:    map[string]sqlc.IdentityGroupMapping{},
	}
	h := newTestSCIMHandler(q)
	body := `{"schemas":["urn:ietf:params:scim:schemas:core:2.0:Group"],"displayName":"engineers"}`
	req := httptest.NewRequest(http.MethodPost, "/scim/v2/Groups", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/scim+json")
	rec := httptest.NewRecorder()
	h.Auth(http.HandlerFunc(h.CreateGroup)).ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if _, ok := q.groups["engineers"]; !ok {
		t.Fatalf("expected group mapping for engineers, got %+v", q.groups)
	}
}

// TestSCIMDeleteGroup removes the mapping and returns 204 (SCIM-R01).
func TestSCIMDeleteGroup(t *testing.T) {
	token := "astro_scim_delgroup"
	q := &fakeSCIMQuerier{
		tokenHash: auth.HashSCIMToken(token),
		users:     map[string]sqlc.User{},
		groups:    map[string]sqlc.IdentityGroupMapping{},
	}
	h := newTestSCIMHandler(q)

	// Seed via CreateGroup so the group exists in the SCIM view.
	createBody := `{"schemas":["urn:ietf:params:scim:schemas:core:2.0:Group"],"displayName":"contractors"}`
	createReq := httptest.NewRequest(http.MethodPost, "/scim/v2/Groups", strings.NewReader(createBody))
	createReq.Header.Set("Authorization", "Bearer "+token)
	createReq.Header.Set("Content-Type", "application/scim+json")
	createRec := httptest.NewRecorder()
	h.Auth(http.HandlerFunc(h.CreateGroup)).ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create: status=%d body=%s", createRec.Code, createRec.Body.String())
	}

	req := httptest.NewRequest(http.MethodDelete, "/scim/v2/Groups/contractors", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req = withChiID(req, "contractors")
	rec := httptest.NewRecorder()
	h.Auth(http.HandlerFunc(h.DeleteGroup)).ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: want 204, got %d (%s)", rec.Code, rec.Body.String())
	}
	if _, ok := q.groups["contractors"]; ok {
		t.Fatalf("group mapping should be gone, still have %+v", q.groups)
	}

	// Second delete → 404.
	req2 := httptest.NewRequest(http.MethodDelete, "/scim/v2/Groups/contractors", nil)
	req2.Header.Set("Authorization", "Bearer "+token)
	req2 = withChiID(req2, "contractors")
	rec2 := httptest.NewRecorder()
	h.Auth(http.HandlerFunc(h.DeleteGroup)).ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusNotFound {
		t.Fatalf("delete missing: want 404, got %d", rec2.Code)
	}
}

func TestSCIMMutationsFailClosedWithoutTransactionRunner(t *testing.T) {
	userID := uuid.New()
	groupID := uuid.New()
	q := &fakeSCIMQuerier{
		users: map[string]sqlc.User{
			"existing@example.com": {ID: userID, Username: "existing@example.com", Email: "existing@example.com", IsActive: true},
		},
		groups: map[string]sqlc.IdentityGroupMapping{
			"engineers": {ID: groupID, GroupName: "engineers", Scope: "global"},
		},
	}
	h := NewSCIMHandler(q)
	tests := []struct {
		name    string
		method  string
		path    string
		body    string
		id      string
		handler http.HandlerFunc
	}{
		{name: "create user", method: http.MethodPost, path: "/scim/v2/Users", body: `{"userName":"new@example.com"}`, handler: h.CreateUser},
		{name: "replace user", method: http.MethodPut, path: "/scim/v2/Users/" + userID.String(), body: `{"userName":"existing@example.com","active":false}`, id: userID.String(), handler: h.PutUser},
		{name: "patch user", method: http.MethodPatch, path: "/scim/v2/Users/" + userID.String(), body: `{"Operations":[{"op":"replace","path":"active","value":false}]}`, id: userID.String(), handler: h.PatchUser},
		{name: "delete user", method: http.MethodDelete, path: "/scim/v2/Users/" + userID.String(), id: userID.String(), handler: h.DeleteUser},
		{name: "create group", method: http.MethodPost, path: "/scim/v2/Groups", body: `{"displayName":"new-group"}`, handler: h.CreateGroup},
		{name: "patch group", method: http.MethodPatch, path: "/scim/v2/Groups/engineers", body: `{"Operations":[{"op":"replace","path":"displayName","value":"renamed"}]}`, id: "engineers", handler: h.PatchGroup},
		{name: "delete group", method: http.MethodDelete, path: "/scim/v2/Groups/engineers", id: "engineers", handler: h.DeleteGroup},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			if test.id != "" {
				req = withChiID(req, test.id)
			}
			rec := httptest.NewRecorder()
			test.handler(rec, req)
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status=%d body=%s, want 503", rec.Code, rec.Body.String())
			}
		})
	}
	if !q.users["existing@example.com"].IsActive || len(q.users) != 1 {
		t.Fatalf("user state changed without transaction runner: %+v", q.users)
	}
	if _, ok := q.groups["engineers"]; !ok || len(q.groups) != 1 {
		t.Fatalf("group state changed without transaction runner: %+v", q.groups)
	}
	if len(q.outbox) != 0 {
		t.Fatalf("unexpected outbox rows without transaction runner: %d", len(q.outbox))
	}
}

func TestSCIMUserDeactivationRollsBackWhenAuditOutboxFails(t *testing.T) {
	userID := uuid.New()
	q := &fakeSCIMQuerier{
		users: map[string]sqlc.User{
			"alice@example.com": {ID: userID, Username: "alice@example.com", Email: "alice@example.com", IsActive: true},
		},
		outboxErr: errors.New("audit database unavailable"),
	}
	h := newTestSCIMHandler(q)
	req := withChiID(httptest.NewRequest(http.MethodPut, "/scim/v2/Users/"+userID.String(),
		strings.NewReader(`{"userName":"alice@example.com","active":false}`)), userID.String())
	rec := httptest.NewRecorder()
	h.PutUser(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s, want 503", rec.Code, rec.Body.String())
	}
	if !q.users["alice@example.com"].IsActive {
		t.Fatal("deactivation committed without its audit outbox row")
	}
	if len(q.invalidatedTokensFor) != 0 {
		t.Fatalf("token revocation committed without audit: %v", q.invalidatedTokensFor)
	}
}

func TestSCIMGroupMembershipFailureRollsBackWholeMutation(t *testing.T) {
	userID := uuid.New()
	q := &fakeSCIMQuerier{
		users: map[string]sqlc.User{
			"member@example.com": {ID: userID, Username: "member@example.com", Email: "member@example.com", IsActive: true},
		},
		groups: map[string]sqlc.IdentityGroupMapping{},
		idpErr: errors.New("membership write failed"),
	}
	h := newTestSCIMHandler(q)
	req := httptest.NewRequest(http.MethodPost, "/scim/v2/Groups", strings.NewReader(fmt.Sprintf(
		`{"displayName":"engineers","members":[{"value":%q}]}`, userID.String())))
	rec := httptest.NewRecorder()
	h.CreateGroup(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s, want 500", rec.Code, rec.Body.String())
	}
	if len(q.groups) != 0 {
		t.Fatalf("group mapping survived failed member update: %+v", q.groups)
	}
	if len(q.outbox) != 0 {
		t.Fatalf("audit row survived rolled-back group creation: %d", len(q.outbox))
	}
}

func TestSCIMGroupDeleteRollsBackMappingWhenMembershipCleanupFails(t *testing.T) {
	userID := uuid.New()
	groupID := uuid.New()
	q := &fakeSCIMQuerier{
		users: map[string]sqlc.User{
			"member@example.com": {ID: userID, Username: "member@example.com", Email: "member@example.com", IsActive: true},
		},
		groups: map[string]sqlc.IdentityGroupMapping{
			"engineers": {ID: groupID, GroupName: "engineers", Scope: "global"},
		},
		idpGroups: map[uuid.UUID]sqlc.UserIdpGroup{
			userID: {UserID: userID, Groups: json.RawMessage(`["engineers"]`)},
		},
		idpErr: errors.New("membership cleanup failed"),
	}
	h := newTestSCIMHandler(q)
	req := withChiID(httptest.NewRequest(http.MethodDelete, "/scim/v2/Groups/engineers", nil), "engineers")
	rec := httptest.NewRecorder()
	h.DeleteGroup(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s, want 500", rec.Code, rec.Body.String())
	}
	if _, ok := q.groups["engineers"]; !ok {
		t.Fatal("group mapping was deleted despite failed membership cleanup")
	}
	if got := string(q.idpGroups[userID].Groups); got != `["engineers"]` {
		t.Fatalf("membership changed after rollback: %s", got)
	}
	if len(q.outbox) != 0 {
		t.Fatalf("audit row survived rolled-back group deletion: %d", len(q.outbox))
	}
}

func TestSCIMMutationAuditIsTransactionalAndReadsDoNotWriteOutbox(t *testing.T) {
	token := "astro_scim_audit"
	q := &fakeSCIMQuerier{
		tokenHash: auth.HashSCIMToken(token),
		users:     map[string]sqlc.User{},
	}
	h := newTestSCIMHandler(q)
	create := httptest.NewRequest(http.MethodPost, "/scim/v2/Users", strings.NewReader(`{"userName":"audited@example.com"}`))
	create.Header.Set("Authorization", "Bearer "+token)
	createRec := httptest.NewRecorder()
	h.Auth(http.HandlerFunc(h.CreateUser)).ServeHTTP(createRec, create)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", createRec.Code, createRec.Body.String())
	}
	if len(q.outbox) != 1 || q.outbox[0].Action != "scim.user.create" {
		t.Fatalf("transactional audit rows=%+v", q.outbox)
	}
	var detail map[string]any
	if err := json.Unmarshal(q.outbox[0].Detail, &detail); err != nil {
		t.Fatalf("decode audit detail: %v", err)
	}
	if detail["token_id"] == "" {
		t.Fatalf("audit detail missing authenticated SCIM token id: %+v", detail)
	}

	list := httptest.NewRequest(http.MethodGet, "/scim/v2/Users", nil)
	list.Header.Set("Authorization", "Bearer "+token)
	listRec := httptest.NewRecorder()
	h.Auth(http.HandlerFunc(h.ListUsers)).ServeHTTP(listRec, list)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", listRec.Code, listRec.Body.String())
	}
	if len(q.outbox) != 1 {
		t.Fatalf("read path wrote post-response audit row: %d", len(q.outbox))
	}
}

// TestSCIMServiceProviderConfig asserts the discovery document advertises
// patch=true (the capability Azure AD/Okta gate provisioning on) and is
// served under the static-bearer Auth chain.
func TestSCIMServiceProviderConfig(t *testing.T) {
	token := "astro_scim_testtoken"
	q := &fakeSCIMQuerier{tokenHash: auth.HashSCIMToken(token), users: map[string]sqlc.User{}}
	h := newTestSCIMHandler(q)

	req := httptest.NewRequest(http.MethodGet, "/scim/v2/ServiceProviderConfig", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.Auth(http.HandlerFunc(h.ServiceProviderConfig)).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("ServiceProviderConfig: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}

	var cfg struct {
		Schemas               []string                 `json:"schemas"`
		Patch                 struct{ Supported bool } `json:"patch"`
		Filter                struct{ Supported bool } `json:"filter"`
		Bulk                  struct{ Supported bool } `json:"bulk"`
		AuthenticationSchemes []struct {
			Type string `json:"type"`
		} `json:"authenticationSchemes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !cfg.Patch.Supported {
		t.Fatalf("ServiceProviderConfig: patch not advertised as supported")
	}
	if !cfg.Filter.Supported {
		t.Fatalf("ServiceProviderConfig: filter not advertised as supported")
	}
	if cfg.Bulk.Supported {
		t.Fatalf("ServiceProviderConfig: bulk should be unsupported")
	}
	if len(cfg.AuthenticationSchemes) != 1 || cfg.AuthenticationSchemes[0].Type != "oauthbearertoken" {
		t.Fatalf("ServiceProviderConfig: want oauthbearertoken auth scheme, got %+v", cfg.AuthenticationSchemes)
	}
	if len(cfg.Schemas) != 1 || cfg.Schemas[0] != scimServiceProviderConfigSchema {
		t.Fatalf("ServiceProviderConfig: missing/incorrect schema: %+v", cfg.Schemas)
	}

	// Discovery sits under the same Auth chain — no token => 401.
	noTok := httptest.NewRequest(http.MethodGet, "/scim/v2/ServiceProviderConfig", nil)
	noRec := httptest.NewRecorder()
	h.Auth(http.HandlerFunc(h.ServiceProviderConfig)).ServeHTTP(noRec, noTok)
	if noRec.Code != http.StatusUnauthorized {
		t.Fatalf("ServiceProviderConfig without token: want 401, got %d", noRec.Code)
	}
}

// withChiID injects {id} into the chi route context so the handler's
// chi.URLParam(r, "id") resolves without standing up a full router.
func withChiID(r *http.Request, id string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}
