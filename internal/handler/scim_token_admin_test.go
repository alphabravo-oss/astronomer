package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// fakeSCIMTokenAdminQuerier is an in-memory store of scim_tokens keyed by
// hash, plus a single caller user for the superuser gate. It satisfies
// both SCIMTokenAdminQuerier (mint/list/revoke) and the subset of
// SCIMQuerier used for the cross-auth test (GetSCIMTokenByHash/Touch).
type fakeSCIMTokenAdminQuerier struct {
	caller sqlc.User
	mu     sync.Mutex
	rows   map[uuid.UUID]sqlc.ScimToken // id -> row
}

func newFakeSCIMTokenAdminQuerier(caller sqlc.User) *fakeSCIMTokenAdminQuerier {
	return &fakeSCIMTokenAdminQuerier{caller: caller, rows: map[uuid.UUID]sqlc.ScimToken{}}
}

func (f *fakeSCIMTokenAdminQuerier) GetUserByID(_ context.Context, id uuid.UUID) (sqlc.User, error) {
	if id == f.caller.ID {
		return f.caller, nil
	}
	return sqlc.User{}, pgx.ErrNoRows
}

func (f *fakeSCIMTokenAdminQuerier) CreateSCIMToken(_ context.Context, arg sqlc.CreateSCIMTokenParams) (sqlc.ScimToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row := sqlc.ScimToken{
		ID:        uuid.New(),
		Name:      arg.Name,
		TokenHash: arg.TokenHash,
		Prefix:    arg.Prefix,
	}
	f.rows[row.ID] = row
	return row, nil
}

func (f *fakeSCIMTokenAdminQuerier) ListSCIMTokens(_ context.Context) ([]sqlc.ScimToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]sqlc.ScimToken, 0, len(f.rows))
	for _, r := range f.rows {
		out = append(out, r)
	}
	return out, nil
}

func (f *fakeSCIMTokenAdminQuerier) DeleteSCIMToken(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.rows, id)
	return nil
}

func TestSCIMTokenAdmin_CreateReturnsPlaintextOnceAndListNeverLeaks(t *testing.T) {
	callerID := uuid.New()
	q := newFakeSCIMTokenAdminQuerier(sqlc.User{ID: callerID, IsSuperuser: true})
	h := NewSCIMTokenAdminHandler(q)

	// Create
	rec := httptest.NewRecorder()
	h.Create(rec, authedRequest(http.MethodPost, "/api/v1/admin/scim-tokens/", callerID, []byte(`{"name":"okta"}`)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var createResp struct {
		Data struct {
			ID    string `json:"id"`
			Name  string `json:"name"`
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	created := createResp.Data
	if !strings.HasPrefix(created.Token, auth.SCIMTokenPrefix) {
		t.Fatalf("token = %q, want %s prefix", created.Token, auth.SCIMTokenPrefix)
	}
	if created.Name != "okta" {
		t.Fatalf("name = %q, want okta", created.Name)
	}
	plaintext := created.Token

	// List must never include the plaintext or the hash.
	recList := httptest.NewRecorder()
	h.List(recList, authedRequest(http.MethodGet, "/api/v1/admin/scim-tokens/", callerID, nil))
	if recList.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body=%s", recList.Code, recList.Body.String())
	}
	listBody := recList.Body.String()
	if strings.Contains(listBody, plaintext) {
		t.Fatalf("list leaked the plaintext token: %s", listBody)
	}
	if strings.Contains(listBody, auth.HashSCIMToken(plaintext)) {
		t.Fatalf("list leaked the token hash: %s", listBody)
	}
	if !strings.Contains(listBody, "okta") {
		t.Fatalf("list missing token metadata: %s", listBody)
	}

	// The minted token authenticates against the /scim/v2 chain. Back the
	// SCIM handler with the same hash the admin handler persisted.
	scim := NewSCIMHandler(&fakeSCIMQuerier{
		tokenHash: auth.HashSCIMToken(plaintext),
		users:     map[string]sqlc.User{},
	})
	authed := scim.Auth(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	good := httptest.NewRequest(http.MethodGet, "/scim/v2/Users", nil)
	good.Header.Set("Authorization", "Bearer "+plaintext)
	recAuth := httptest.NewRecorder()
	authed.ServeHTTP(recAuth, good)
	if recAuth.Code != http.StatusOK {
		t.Fatalf("minted token auth status = %d, want 200; body=%s", recAuth.Code, recAuth.Body.String())
	}

	// A bogus token is rejected.
	bad := httptest.NewRequest(http.MethodGet, "/scim/v2/Users", nil)
	bad.Header.Set("Authorization", "Bearer astro_scim_nope")
	recBad := httptest.NewRecorder()
	authed.ServeHTTP(recBad, bad)
	if recBad.Code != http.StatusUnauthorized {
		t.Fatalf("bogus token auth status = %d, want 401", recBad.Code)
	}
}

func TestSCIMTokenAdmin_NonSuperuserForbidden(t *testing.T) {
	callerID := uuid.New()
	q := newFakeSCIMTokenAdminQuerier(sqlc.User{ID: callerID, IsSuperuser: false})
	h := NewSCIMTokenAdminHandler(q)

	rec := httptest.NewRecorder()
	h.Create(rec, authedRequest(http.MethodPost, "/api/v1/admin/scim-tokens/", callerID, []byte(`{"name":"x"}`)))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-superuser create status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
}

func TestSCIMTokenAdmin_DeleteRevokes(t *testing.T) {
	callerID := uuid.New()
	q := newFakeSCIMTokenAdminQuerier(sqlc.User{ID: callerID, IsSuperuser: true})
	h := NewSCIMTokenAdminHandler(q)

	rec := httptest.NewRecorder()
	h.Create(rec, authedRequest(http.MethodPost, "/api/v1/admin/scim-tokens/", callerID, []byte(`{"name":"azure"}`)))
	var createResp struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &createResp)
	id := createResp.Data.ID

	delReq := withURLParam(authedRequest(http.MethodDelete, "/api/v1/admin/scim-tokens/"+id+"/", callerID, nil), "id", id)
	recDel := httptest.NewRecorder()
	h.Delete(recDel, delReq)
	if recDel.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204; body=%s", recDel.Code, recDel.Body.String())
	}

	rows, _ := q.ListSCIMTokens(context.Background())
	if len(rows) != 0 {
		t.Fatalf("after delete: %d rows remain, want 0", len(rows))
	}
}
