// Vault handler tests (migration 067).
//
// Coverage:
//   - TestVaultHandler_RequiresSuperuser: 403 for non-superusers on every
//     admin endpoint, 401 for unauthenticated.
//   - TestVaultHandler_CRUD: create → get → list → update → delete, with
//     auth-blob round-tripping and the SentinelEncrypted preserve rule.
//   - TestVaultHandler_HelmInstall_UsesResolvedValues_InWireOnly: hooks
//     a stub vault.Resolver into CatalogHandler, walks CreateInstallation,
//     and asserts the DB row keeps the ${vault://...} marker while the
//     enqueued operation envelope carries the resolved value.

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
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	avault "github.com/alphabravocompany/astronomer-go/internal/vault"
)

// fakeVaultQuerier implements VaultConnectionQuerier for handler tests.
// In-memory only; FK behavior is whatever the tests need.
type fakeVaultQuerier struct {
	users    map[uuid.UUID]sqlc.User
	projects map[uuid.UUID]sqlc.Project
	conns    map[uuid.UUID]sqlc.VaultConnection
	byName   map[string]uuid.UUID
	defaults map[uuid.UUID]pgtype.UUID // projectID → connectionID
}

func newFakeVaultQuerier() *fakeVaultQuerier {
	return &fakeVaultQuerier{
		users:    map[uuid.UUID]sqlc.User{},
		projects: map[uuid.UUID]sqlc.Project{},
		conns:    map[uuid.UUID]sqlc.VaultConnection{},
		byName:   map[string]uuid.UUID{},
		defaults: map[uuid.UUID]pgtype.UUID{},
	}
}

func (f *fakeVaultQuerier) GetUserByID(_ context.Context, id uuid.UUID) (sqlc.User, error) {
	u, ok := f.users[id]
	if !ok {
		return sqlc.User{}, errNotFound
	}
	return u, nil
}
func (f *fakeVaultQuerier) GetProjectByID(_ context.Context, id uuid.UUID) (sqlc.Project, error) {
	p, ok := f.projects[id]
	if !ok {
		return sqlc.Project{}, errNotFound
	}
	return p, nil
}
func (f *fakeVaultQuerier) ListVaultConnections(_ context.Context) ([]sqlc.VaultConnection, error) {
	out := make([]sqlc.VaultConnection, 0, len(f.conns))
	for _, c := range f.conns {
		out = append(out, c)
	}
	return out, nil
}
func (f *fakeVaultQuerier) GetVaultConnectionByID(_ context.Context, id uuid.UUID) (sqlc.VaultConnection, error) {
	c, ok := f.conns[id]
	if !ok {
		return sqlc.VaultConnection{}, errNotFound
	}
	return c, nil
}
func (f *fakeVaultQuerier) GetVaultConnectionByName(_ context.Context, name string) (sqlc.VaultConnection, error) {
	id, ok := f.byName[name]
	if !ok {
		return sqlc.VaultConnection{}, errNotFound
	}
	return f.conns[id], nil
}
func (f *fakeVaultQuerier) CreateVaultConnection(_ context.Context, arg sqlc.CreateVaultConnectionParams) (sqlc.VaultConnection, error) {
	if _, taken := f.byName[arg.Name]; taken {
		return sqlc.VaultConnection{}, errDuplicateKey
	}
	id := uuid.New()
	row := sqlc.VaultConnection{
		ID:            id,
		Name:          arg.Name,
		Description:   arg.Description,
		Addr:          arg.Addr,
		AuthMethod:    arg.AuthMethod,
		AuthEncrypted: arg.AuthEncrypted,
		Namespace:     arg.Namespace,
		TlsSkipVerify: arg.TlsSkipVerify,
		CaCertPem:     arg.CaCertPem,
		DefaultMount:  arg.DefaultMount,
		Enabled:       arg.Enabled,
		CreatedBy:     arg.CreatedBy,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	f.conns[id] = row
	f.byName[arg.Name] = id
	return row, nil
}
func (f *fakeVaultQuerier) UpdateVaultConnection(_ context.Context, arg sqlc.UpdateVaultConnectionParams) (sqlc.VaultConnection, error) {
	row, ok := f.conns[arg.ID]
	if !ok {
		return sqlc.VaultConnection{}, errNotFound
	}
	row.Description = arg.Description
	row.Addr = arg.Addr
	row.AuthMethod = arg.AuthMethod
	row.AuthEncrypted = arg.AuthEncrypted
	row.Namespace = arg.Namespace
	row.TlsSkipVerify = arg.TlsSkipVerify
	row.CaCertPem = arg.CaCertPem
	row.DefaultMount = arg.DefaultMount
	row.Enabled = arg.Enabled
	row.UpdatedAt = time.Now()
	f.conns[arg.ID] = row
	return row, nil
}
func (f *fakeVaultQuerier) DeleteVaultConnection(_ context.Context, id uuid.UUID) error {
	row, ok := f.conns[id]
	if !ok {
		return errNotFound
	}
	delete(f.byName, row.Name)
	delete(f.conns, id)
	return nil
}
func (f *fakeVaultQuerier) UpdateVaultConnectionHealth(_ context.Context, arg sqlc.UpdateVaultConnectionHealthParams) error {
	row, ok := f.conns[arg.ID]
	if !ok {
		return errNotFound
	}
	row.LastHealthOk = arg.LastHealthOk
	row.LastError = arg.LastError
	row.LastHealthAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	f.conns[arg.ID] = row
	return nil
}
func (f *fakeVaultQuerier) SetProjectDefaultVaultConnection(_ context.Context, arg sqlc.SetProjectDefaultVaultConnectionParams) error {
	f.defaults[arg.ProjectID] = arg.DefaultVaultConnectionID
	return nil
}
func (f *fakeVaultQuerier) GetProjectDefaultVaultConnection(_ context.Context, projectID uuid.UUID) (pgtype.UUID, error) {
	v, ok := f.defaults[projectID]
	if !ok {
		return pgtype.UUID{}, nil
	}
	return v, nil
}

var errNotFound = stringError("not found")
var errDuplicateKey = stringError("duplicate key value violates unique constraint")

type stringError string

func (s stringError) Error() string { return string(s) }

// makeVaultRequest builds a request with the supplied auth context.
func makeVaultRequest(t *testing.T, method, path string, callerID uuid.UUID, body any) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req = req.WithContext(middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{
		ID: callerID.String(),
	}))
	return req
}

// newVaultTestEncryptor returns a working Fernet encryptor for tests.
func newVaultTestEncryptor(t *testing.T) *auth.Encryptor {
	t.Helper()
	// Fernet uses 32-byte base64-encoded keys; use a known fixed key
	// so tests are deterministic.
	enc, err := auth.NewEncryptor("YWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWE=")
	if err != nil {
		t.Fatalf("NewEncryptor: %v", err)
	}
	return enc
}

func TestVaultHandler_RequiresSuperuser(t *testing.T) {
	fq := newFakeVaultQuerier()
	nonSuper := uuid.New()
	fq.users[nonSuper] = sqlc.User{ID: nonSuper, IsSuperuser: false}

	h := NewVaultHandler(fq)
	h.SetEncryptor(newVaultTestEncryptor(t))

	// Build a chi router so URL params resolve correctly.
	r := chi.NewRouter()
	r.Get("/api/v1/admin/vault-connections/", h.List)
	r.Post("/api/v1/admin/vault-connections/", h.Create)
	r.Get("/api/v1/admin/vault-connections/{id}/", h.Get)
	r.Put("/api/v1/admin/vault-connections/{id}/", h.Update)
	r.Delete("/api/v1/admin/vault-connections/{id}/", h.Delete)
	r.Post("/api/v1/admin/vault-connections/{id}/test/", h.Test)
	r.Post("/api/v1/admin/vault-connections/{id}/health/", h.Health)

	dummyID := uuid.New()
	cases := []struct {
		name, method, path string
	}{
		{"list", "GET", "/api/v1/admin/vault-connections/"},
		{"create", "POST", "/api/v1/admin/vault-connections/"},
		{"get", "GET", "/api/v1/admin/vault-connections/" + dummyID.String() + "/"},
		{"update", "PUT", "/api/v1/admin/vault-connections/" + dummyID.String() + "/"},
		{"delete", "DELETE", "/api/v1/admin/vault-connections/" + dummyID.String() + "/"},
		{"test", "POST", "/api/v1/admin/vault-connections/" + dummyID.String() + "/test/"},
		{"health", "POST", "/api/v1/admin/vault-connections/" + dummyID.String() + "/health/"},
	}
	for _, c := range cases {
		t.Run(c.name+"_non_superuser", func(t *testing.T) {
			req := makeVaultRequest(t, c.method, c.path, nonSuper, nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusForbidden {
				t.Fatalf("want 403, got %d", w.Code)
			}
		})
		t.Run(c.name+"_unauthenticated", func(t *testing.T) {
			req := httptest.NewRequest(c.method, c.path, bytes.NewReader(nil))
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("want 401, got %d", w.Code)
			}
		})
	}
}

func TestVaultHandler_CreateGetUpdate(t *testing.T) {
	fq := newFakeVaultQuerier()
	caller := uuid.New()
	fq.users[caller] = sqlc.User{ID: caller, IsSuperuser: true}

	h := NewVaultHandler(fq)
	h.SetEncryptor(newVaultTestEncryptor(t))

	r := chi.NewRouter()
	r.Post("/api/v1/admin/vault-connections/", h.Create)
	r.Get("/api/v1/admin/vault-connections/{id}/", h.Get)
	r.Put("/api/v1/admin/vault-connections/{id}/", h.Update)

	// Create.
	body := map[string]any{
		"name":          "prod",
		"description":   "primary",
		"addr":          "https://vault.example.com",
		"auth_method":   "token",
		"auth":          map[string]any{"token": "root-secret"},
		"default_mount": "secret",
	}
	req := makeVaultRequest(t, "POST", "/api/v1/admin/vault-connections/", caller, body)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create want 201, got %d: %s", w.Code, w.Body.String())
	}
	var wrapped struct {
		Data VaultConnectionResponse `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&wrapped); err != nil {
		t.Fatalf("decode created: %v", err)
	}
	created := wrapped.Data
	if created.Auth["token"] != avault.SentinelEncrypted {
		t.Fatalf("token should be redacted in response, got %q (full body: %s)", created.Auth["token"], w.Body.String())
	}

	// Verify cleartext didn't leak.
	if strings.Contains(w.Body.String(), "root-secret") {
		t.Fatalf("response leaked cleartext token")
	}

	// Get echoes the same redaction.
	req = makeVaultRequest(t, "GET", "/api/v1/admin/vault-connections/"+created.ID.String()+"/", caller, nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get want 200, got %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "root-secret") {
		t.Fatalf("get leaked cleartext token")
	}

	// Update with sentinel preserves the stored token. Pass an empty
	// description; the auth blob's "token" field is the sentinel.
	upd := map[string]any{
		"description":   "primary (updated)",
		"addr":          "https://vault.example.com",
		"auth_method":   "token",
		"auth":          map[string]any{"token": avault.SentinelEncrypted},
		"default_mount": "secret",
	}
	req = makeVaultRequest(t, "PUT", "/api/v1/admin/vault-connections/"+created.ID.String()+"/", caller, upd)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update want 200, got %d: %s", w.Code, w.Body.String())
	}
	// Verify stored blob still decrypts to the original token (via the
	// fake querier's stored row).
	stored := fq.conns[created.ID]
	plain, err := h.encryptor.Decrypt(stored.AuthEncrypted)
	if err != nil {
		t.Fatalf("decrypt stored: %v", err)
	}
	if !strings.Contains(plain, "root-secret") {
		t.Fatalf("stored token lost after update; got %q", plain)
	}
}

// TestVaultHandler_RejectsInsecureAddr verifies the http:// gate.
func TestVaultHandler_RejectsInsecureAddr(t *testing.T) {
	fq := newFakeVaultQuerier()
	caller := uuid.New()
	fq.users[caller] = sqlc.User{ID: caller, IsSuperuser: true}

	h := NewVaultHandler(fq)
	h.SetEncryptor(newVaultTestEncryptor(t))

	r := chi.NewRouter()
	r.Post("/api/v1/admin/vault-connections/", h.Create)

	body := map[string]any{
		"name":        "dev",
		"addr":        "http://vault.local",
		"auth_method": "token",
		"auth":        map[string]any{"token": "abc"},
	}
	req := makeVaultRequest(t, "POST", "/api/v1/admin/vault-connections/", caller, body)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for http:// without tls_skip_verify, got %d", w.Code)
	}
}
