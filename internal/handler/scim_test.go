package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

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
}

func (f *fakeSCIMQuerier) GetSCIMTokenByHash(_ context.Context, hash string) (sqlc.ScimToken, error) {
	if hash == f.tokenHash {
		return sqlc.ScimToken{ID: uuid.New()}, nil
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
	return nil, nil
}

func (f *fakeSCIMQuerier) CountSCIMGroupNames(_ context.Context) (int64, error) { return 0, nil }

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
	h := NewSCIMHandler(q)

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
	h := NewSCIMHandler(q)

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
	h := NewSCIMHandler(q)

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
	h := NewSCIMHandler(q)
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

// TestSCIMServiceProviderConfig asserts the discovery document advertises
// patch=true (the capability Azure AD/Okta gate provisioning on) and is
// served under the static-bearer Auth chain.
func TestSCIMServiceProviderConfig(t *testing.T) {
	token := "astro_scim_testtoken"
	q := &fakeSCIMQuerier{tokenHash: auth.HashSCIMToken(token), users: map[string]sqlc.User{}}
	h := NewSCIMHandler(q)

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
