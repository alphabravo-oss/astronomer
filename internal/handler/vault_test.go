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
	audits   []sqlc.CreateAuditLogV1Params
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
	f.defaults[arg.ID] = arg.DefaultVaultConnectionID
	return nil
}
func (f *fakeVaultQuerier) GetProjectDefaultVaultConnection(_ context.Context, projectID uuid.UUID) (pgtype.UUID, error) {
	v, ok := f.defaults[projectID]
	if !ok {
		return pgtype.UUID{}, nil
	}
	return v, nil
}

func (f *fakeVaultQuerier) CreateAuditLogV1(_ context.Context, arg sqlc.CreateAuditLogV1Params) error {
	f.audits = append(f.audits, arg)
	return nil
}

func (f *fakeVaultQuerier) auditRowAt(t *testing.T, idx int) sqlc.CreateAuditLogV1Params {
	t.Helper()
	if len(f.audits) <= idx {
		t.Fatalf("audit rows=%d, want index %d", len(f.audits), idx)
	}
	return f.audits[idx]
}

func assertVaultAudit(t *testing.T, row sqlc.CreateAuditLogV1Params, action, resourceID, resourceName string) {
	t.Helper()
	if row.Action != action {
		t.Fatalf("audit action=%q want %q; row=%+v", row.Action, action, row)
	}
	if row.ResourceType != "vault_connection" {
		t.Fatalf("audit resource_type=%q want vault_connection; row=%+v", row.ResourceType, row)
	}
	if row.ResourceID != resourceID || row.ResourceName != resourceName {
		t.Fatalf("audit target=(%q,%q), want (%q,%q)", row.ResourceID, row.ResourceName, resourceID, resourceName)
	}
}

var errNotFound = stringError("not found")
var errDuplicateKey = stringError("duplicate key value violates unique constraint")

type stringError string

func (s stringError) Error() string { return string(s) }

type fakeVaultProbe struct {
	result TestResult
	err    error
}

func (f fakeVaultProbe) Health(context.Context, sqlc.VaultConnection, string) error {
	return f.err
}

func (f fakeVaultProbe) Test(context.Context, sqlc.VaultConnection, string, string) (TestResult, error) {
	return f.result, f.err
}

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
	createAudit := fq.auditRowAt(t, 0)
	assertVaultAudit(t, createAudit, "admin.vault_connection.created", created.ID.String(), "prod")
	assertAuditDetail(t, createAudit.Detail, "addr", "https://vault.example.com")
	assertAuditDetail(t, createAudit.Detail, "auth_method", "token")
	assertAuditDetailOmit(t, createAudit.Detail, "token")
	assertAuditDetailOmit(t, createAudit.Detail, "auth")

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
	updateAudit := fq.auditRowAt(t, 1)
	assertVaultAudit(t, updateAudit, "admin.vault_connection.updated", created.ID.String(), "prod")
	assertAuditDetail(t, updateAudit.Detail, "addr", "https://vault.example.com")
	assertAuditDetail(t, updateAudit.Detail, "auth_method", "token")
	assertAuditDetailOmit(t, updateAudit.Detail, "token")
	assertAuditDetailOmit(t, updateAudit.Detail, "auth")
}

func TestVaultHandler_DeleteAuditsConnection(t *testing.T) {
	fq := newFakeVaultQuerier()
	caller := uuid.New()
	fq.users[caller] = sqlc.User{ID: caller, IsSuperuser: true}
	connID := uuid.New()
	fq.conns[connID] = sqlc.VaultConnection{
		ID:         connID,
		Name:       "prod",
		Addr:       "https://vault.example.com",
		AuthMethod: "token",
		Enabled:    true,
	}
	fq.byName["prod"] = connID

	h := NewVaultHandler(fq)
	r := chi.NewRouter()
	r.Delete("/api/v1/admin/vault-connections/{id}/", h.Delete)

	req := makeVaultRequest(t, "DELETE", "/api/v1/admin/vault-connections/"+connID.String()+"/", caller, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete want 204, got %d: %s", w.Code, w.Body.String())
	}
	deleteAudit := fq.auditRowAt(t, 0)
	assertVaultAudit(t, deleteAudit, "admin.vault_connection.deleted", connID.String(), "prod")
	assertAuditDetailOmit(t, deleteAudit.Detail, "token")
	assertAuditDetailOmit(t, deleteAudit.Detail, "auth")
}

func TestVaultHandler_TestEndpointAuditsProbeResult(t *testing.T) {
	fq := newFakeVaultQuerier()
	caller := uuid.New()
	fq.users[caller] = sqlc.User{ID: caller, IsSuperuser: true}
	enc := newVaultTestEncryptor(t)
	authBlob, err := avault.EncodeAuthBlob("token", map[string]string{"token": "root-secret"})
	if err != nil {
		t.Fatalf("EncodeAuthBlob: %v", err)
	}
	encrypted, err := enc.Encrypt(authBlob)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	connID := uuid.New()
	fq.conns[connID] = sqlc.VaultConnection{
		ID:            connID,
		Name:          "prod",
		Addr:          "https://vault.example.com",
		AuthMethod:    "token",
		AuthEncrypted: encrypted,
		DefaultMount:  "secret",
		Enabled:       true,
	}

	h := NewVaultHandler(fq)
	h.SetEncryptor(enc)
	h.SetProbe(fakeVaultProbe{result: TestResult{
		OK:        true,
		Reachable: true,
		AuthOK:    true,
		LatencyMS: 12,
		Message:   "ok",
	}})
	r := chi.NewRouter()
	r.Post("/api/v1/admin/vault-connections/{id}/test/", h.Test)

	req := makeVaultRequest(t, "POST", "/api/v1/admin/vault-connections/"+connID.String()+"/test/", caller, map[string]any{
		"probe_path": "secret/data/health",
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("test want 200, got %d: %s", w.Code, w.Body.String())
	}
	testAudit := fq.auditRowAt(t, 0)
	assertVaultAudit(t, testAudit, "admin.vault_connection.tested", connID.String(), "prod")
	assertAuditDetail(t, testAudit.Detail, "probe_path", "secret/data/health")
	assertAuditDetailOmit(t, testAudit.Detail, "token")
	assertAuditDetailOmit(t, testAudit.Detail, "auth")
}

func TestVaultHandler_ProjectDefaultVaultAssignmentIsAudited(t *testing.T) {
	fq := newFakeVaultQuerier()
	caller := uuid.New()
	projectID := uuid.New()
	connID := uuid.New()
	fq.projects[projectID] = sqlc.Project{ID: projectID, Name: "team-a"}
	fq.conns[connID] = sqlc.VaultConnection{ID: connID, Name: "prod", Addr: "https://vault.example.com", AuthMethod: "token"}

	h := NewVaultHandler(fq)
	r := chi.NewRouter()
	r.Put("/api/v1/projects/{id}/default-vault-connection/", h.PutProjectDefault)

	req := makeVaultRequest(t, "PUT", "/api/v1/projects/"+projectID.String()+"/default-vault-connection/", caller, map[string]any{
		"connection_id": connID.String(),
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("project default vault assignment want 200, got %d: %s", w.Code, w.Body.String())
	}
	auditRow := fq.auditRowAt(t, 0)
	if auditRow.Action != "project.default_vault_connection.set" {
		t.Fatalf("audit action=%q want project.default_vault_connection.set; row=%+v", auditRow.Action, auditRow)
	}
	if auditRow.ResourceType != "project" || auditRow.ResourceID != projectID.String() {
		t.Fatalf("audit target=(%q,%q), want project %s", auditRow.ResourceType, auditRow.ResourceID, projectID)
	}
	assertAuditDetail(t, auditRow.Detail, "connection_id", connID.String())
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
