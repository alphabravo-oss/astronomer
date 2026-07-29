package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/catalog"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// --- fixtures ---------------------------------------------------------------

// sealingCatalogQuerier records the exact params the handler sends to the
// database, which is the only place the "is it plaintext at rest" question can
// be answered honestly.
type sealingCatalogQuerier struct {
	CatalogQuerier
	created  sqlc.CreateHelmRepositoryParams
	updated  sqlc.UpdateHelmRepositoryParams
	existing sqlc.HelmRepository
}

func (q *sealingCatalogQuerier) CreateHelmRepository(_ context.Context, arg sqlc.CreateHelmRepositoryParams) (sqlc.HelmRepository, error) {
	q.created = arg
	return sqlc.HelmRepository{
		ID:                  uuid.New(),
		Name:                arg.Name,
		Url:                 arg.Url,
		RepoType:            arg.RepoType,
		AuthType:            arg.AuthType,
		AuthConfig:          arg.AuthConfig,
		AuthConfigEncrypted: arg.AuthConfigEncrypted,
		Enabled:             arg.Enabled,
	}, nil
}

func (q *sealingCatalogQuerier) UpdateHelmRepository(_ context.Context, arg sqlc.UpdateHelmRepositoryParams) (sqlc.HelmRepository, error) {
	q.updated = arg
	return sqlc.HelmRepository{
		ID:                  arg.ID,
		Name:                arg.Name,
		Url:                 arg.Url,
		AuthType:            arg.AuthType,
		AuthConfig:          arg.AuthConfig,
		AuthConfigEncrypted: arg.AuthConfigEncrypted,
	}, nil
}

func (q *sealingCatalogQuerier) GetHelmRepositoryByID(context.Context, uuid.UUID) (sqlc.HelmRepository, error) {
	return q.existing, nil
}

// CreateAuditLogV1 is implemented so recordAudit's auditWriterV1 assertion
// lands on a real method instead of the nil embedded interface.
func (q *sealingCatalogQuerier) CreateAuditLogV1(context.Context, sqlc.CreateAuditLogV1Params) error {
	return nil
}

// CountChartsPerRepository backs the chart_count enrichment on the repository
// responses. Stubbed for the same reason as CreateAuditLogV1: the embedded
// CatalogQuerier is nil, so an unimplemented method is a panic, not a zero.
func (q *sealingCatalogQuerier) CountChartsPerRepository(context.Context, []uuid.UUID) ([]sqlc.CountChartsPerRepositoryRow, error) {
	return nil, nil
}

func testEncryptor(t *testing.T) *auth.Encryptor {
	t.Helper()
	key, err := auth.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	enc, err := auth.NewEncryptor(key)
	if err != nil {
		t.Fatalf("new encryptor: %v", err)
	}
	return enc
}

// sealRepo builds a stored row in the post-145 shape.
func sealRepo(t *testing.T, enc *auth.Encryptor, name, url, authType, doc string) sqlc.HelmRepository {
	t.Helper()
	ciphertext, public, err := catalog.SealAuthConfig(json.RawMessage(doc), enc)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if ciphertext == "" {
		t.Fatalf("fixture %q produced no ciphertext — it has no secret to protect", doc)
	}
	return sqlc.HelmRepository{
		ID: uuid.New(), Name: name, Url: url, RepoType: "helm",
		AuthType: authType, AuthConfig: public, AuthConfigEncrypted: ciphertext,
	}
}

// --- write path -------------------------------------------------------------

// TestCreateRepoSealsCredentialAtRest is the round-trip: what the operator
// posts must not be what lands in the JSONB column.
//
// Pre-fix CreateRepo wrote req.AuthConfig straight into helm_repositories.
// auth_config, so the registry password sat in the clear in the database, in
// pg_dump output and in every backup — the finding this migration exists for.
func TestCreateRepoSealsCredentialAtRest(t *testing.T) {
	enc := testEncryptor(t)
	q := &sealingCatalogQuerier{}
	h := &CatalogHandler{queries: q, log: slog.Default()}
	h.SetEncryptor(enc)

	body := `{"name":"private","url":"https://charts.example.com","auth_type":"basic",` +
		`"auth_config":{"username":"u","password":"s3cret","charts":["app"]},"enabled":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/catalog/repositories/", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.CreateRepo(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status %d body %s", rec.Code, rec.Body.String())
	}

	// 1. The plaintext column must not carry the secret in any form.
	stored := string(q.created.AuthConfig)
	if strings.Contains(stored, "s3cret") {
		t.Fatalf("password stored as readable plaintext in auth_config: %s", stored)
	}
	var stripped map[string]any
	if err := json.Unmarshal(q.created.AuthConfig, &stripped); err != nil {
		t.Fatalf("stored auth_config is not valid JSON: %v", err)
	}
	if _, present := stripped["password"]; present {
		t.Fatalf("password key survives in the plaintext projection: %s", stored)
	}
	// Non-secret fields stay queryable — the catalog UI reads them.
	if stripped["username"] != "u" {
		t.Fatalf("username should stay in the plaintext projection: %s", stored)
	}
	if _, ok := stripped["charts"]; !ok {
		t.Fatalf("charts list should stay in the plaintext projection: %s", stored)
	}

	// 2. The envelope must decrypt back to the complete document.
	if q.created.AuthConfigEncrypted == "" {
		t.Fatal("auth_config_encrypted is empty: nothing was sealed")
	}
	plain, err := enc.Decrypt(q.created.AuthConfigEncrypted)
	if err != nil {
		t.Fatalf("stored ciphertext does not decrypt: %v", err)
	}
	var full map[string]any
	if err := json.Unmarshal([]byte(plain), &full); err != nil {
		t.Fatalf("decrypted document is not JSON: %v", err)
	}
	if full["password"] != "s3cret" {
		t.Fatalf("decrypted document lost the password: %v", full)
	}

	// 3. The API echo must leak neither the secret nor the ciphertext.
	if got := rec.Body.String(); strings.Contains(got, "s3cret") || strings.Contains(got, q.created.AuthConfigEncrypted) {
		t.Fatalf("create response carries the credential or its ciphertext: %s", got)
	}
}

// TestUpdateRepoSentinelMergesAgainstDecryptedDocument — the "leave the
// password alone" sentinel has to resolve against the DECRYPTED document.
//
// Pre-fix (had encryption been added without touching this path) the merge
// base was the stored JSONB, which after sealing no longer contains the
// password: every PUT that echoed the sentinel would silently delete the
// credential the operator meant to preserve.
func TestUpdateRepoSentinelMergesAgainstDecryptedDocument(t *testing.T) {
	enc := testEncryptor(t)
	existing := sealRepo(t, enc, "private", "https://charts.example.com", "basic",
		`{"username":"u","password":"s3cret"}`)
	q := &sealingCatalogQuerier{existing: existing}
	h := &CatalogHandler{queries: q, log: slog.Default()}
	h.SetEncryptor(enc)

	body := `{"name":"private","url":"https://charts.example.com","auth_type":"basic",` +
		`"auth_config":{"username":"u","password":"` + SecretSentinel + `"},"enabled":true}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/catalog/repositories/"+existing.ID.String()+"/",
		bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.UpdateRepo(rec, withURLParam(req, "id", existing.ID.String()))

	if rec.Code != http.StatusOK {
		t.Fatalf("update: status %d body %s", rec.Code, rec.Body.String())
	}
	plain, err := enc.Decrypt(q.updated.AuthConfigEncrypted)
	if err != nil {
		t.Fatalf("updated ciphertext does not decrypt: %v", err)
	}
	var full map[string]any
	_ = json.Unmarshal([]byte(plain), &full)
	if full["password"] != "s3cret" {
		t.Fatalf("sentinel echo lost the stored password: %v", full)
	}
}

// --- read paths -------------------------------------------------------------

// TestHandlerReadPathRecoversSealedCredential covers the interactive reader.
func TestHandlerReadPathRecoversSealedCredential(t *testing.T) {
	enc := testEncryptor(t)
	h := &CatalogHandler{log: slog.Default()}
	h.SetEncryptor(enc)

	basic := sealRepo(t, enc, "basic", "https://charts.example.com", "basic",
		`{"username":"u","password":"p"}`)
	req := httptest.NewRequest(http.MethodGet, "https://charts.example.com/index.yaml", nil)
	h.applyRepoIndexAuth(req, basic)
	user, pass, ok := req.BasicAuth()
	if !ok || user != "u" || pass != "p" {
		t.Fatalf("sealed basic credential not recovered: ok=%v user=%q pass=%q", ok, user, pass)
	}

	bearer := sealRepo(t, enc, "bearer", "https://charts.example.com", "bearer", `{"token":"tok"}`)
	req2 := httptest.NewRequest(http.MethodGet, "https://charts.example.com/index.yaml", nil)
	h.applyRepoIndexAuth(req2, bearer)
	if got := req2.Header.Get("Authorization"); got != "Bearer tok" {
		t.Fatalf("sealed bearer credential not recovered: %q", got)
	}

	oci := sealRepo(t, enc, "oci", "oci://registry.example.com/charts", "basic",
		`{"username":"u","password":"p","charts":["app"]}`)
	cfg, err := h.resolveOCIAuthConfig(oci)
	if err != nil {
		t.Fatalf("resolve OCI: %v", err)
	}
	if cfg.Username != "u" || cfg.Password != "p" || len(cfg.Charts) != 1 {
		t.Fatalf("sealed OCI credential not recovered: %+v", cfg)
	}
}

// TestLegacyPlaintextRowStillAuthenticatesAfterUpgrade — an install that
// upgrades onto migration 145 has rows with an empty envelope and the whole
// document still in the JSONB. They must keep working until the sealing sweep
// converts them; encrypting on write without this fallback is how a security
// fix becomes an outage for every authenticated repository.
func TestLegacyPlaintextRowStillAuthenticatesAfterUpgrade(t *testing.T) {
	enc := testEncryptor(t)
	h := &CatalogHandler{log: slog.Default()}
	h.SetEncryptor(enc)

	legacy := sqlc.HelmRepository{
		Name: "pre-145", Url: "https://charts.example.com", RepoType: "helm",
		AuthType:   "basic",
		AuthConfig: json.RawMessage(`{"username":"u","password":"p"}`),
		// AuthConfigEncrypted deliberately empty: this is the pre-upgrade shape.
	}
	req := httptest.NewRequest(http.MethodGet, "https://charts.example.com/index.yaml", nil)
	h.applyRepoIndexAuth(req, legacy)
	user, pass, ok := req.BasicAuth()
	if !ok || user != "u" || pass != "p" {
		t.Fatalf("pre-145 plaintext row stopped authenticating: ok=%v user=%q pass=%q", ok, user, pass)
	}
}

// TestUndecryptableCredentialSendsNoAuthorizationHeader is the anti-regression
// for the decryptGitAuth shape (internal/worker/tasks/gitops_sync.go), which
// returns the CIPHERTEXT when Decrypt fails. Sending a Fernet token as a
// password produces an upstream 401 and sends the operator hunting a
// credential problem when the actual fault is the encryption key.
func TestUndecryptableCredentialSendsNoAuthorizationHeader(t *testing.T) {
	sealedUnderOldKey := sealRepo(t, testEncryptor(t), "private", "https://charts.example.com",
		"basic", `{"username":"u","password":"p"}`)

	// A different key — the "rotation dropped the old key too early" case.
	h := &CatalogHandler{log: slog.Default()}
	h.SetEncryptor(testEncryptor(t))

	req := httptest.NewRequest(http.MethodGet, "https://charts.example.com/index.yaml", nil)
	h.applyRepoIndexAuth(req, sealedUnderOldKey)
	if got := req.Header.Get("Authorization"); got != "" {
		t.Fatalf("request must carry NO Authorization header when the credential cannot be decrypted, got %q", got)
	}
	if strings.Contains(req.Header.Get("Authorization"), sealedUnderOldKey.AuthConfigEncrypted) {
		t.Fatal("ciphertext was sent as a credential")
	}

	// Same for the "no key configured at all" case.
	h2 := &CatalogHandler{log: slog.Default()}
	req2 := httptest.NewRequest(http.MethodGet, "https://charts.example.com/index.yaml", nil)
	h2.applyRepoIndexAuth(req2, sealedUnderOldKey)
	if got := req2.Header.Get("Authorization"); got != "" {
		t.Fatalf("no-encryptor path must not authenticate, got %q", got)
	}

	// The OCI reader reports the failure rather than pulling anonymously.
	if _, err := h.resolveOCIAuthConfig(sealedUnderOldKey); err == nil {
		t.Fatal("OCI resolve must fail loudly instead of degrading to an anonymous pull")
	}
}

// --- API response -----------------------------------------------------------

// TestRedactedRepositoryResponseCarriesNeitherSecretNorCiphertext.
//
// GET/POST/PUT /catalog/repositories serialise sqlc.HelmRepository wholesale.
// Before this change the model gained an auth_config_encrypted field that
// would have been marshalled straight onto the wire: every catalog reader
// would receive an offline-crackable copy of the credential.
func TestRedactedRepositoryResponseCarriesNeitherSecretNorCiphertext(t *testing.T) {
	enc := testEncryptor(t)
	h := &CatalogHandler{log: slog.Default()}
	h.SetEncryptor(enc)

	repo := sealRepo(t, enc, "private", "https://charts.example.com", "basic",
		`{"username":"u","password":"s3cret","token":"b34r3r-t0k3n"}`)
	out := h.redactHelmRepository(repo)

	if out.AuthConfigEncrypted != "" {
		t.Fatalf("API response carries the ciphertext: %q", out.AuthConfigEncrypted)
	}
	body, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "s3cret") || strings.Contains(string(body), "b34r3r-t0k3n") {
		t.Fatalf("API response carries the credential: %s", body)
	}
	if strings.Contains(string(body), repo.AuthConfigEncrypted) {
		t.Fatalf("API response carries the ciphertext: %s", body)
	}
	// Shape preserved: the client can still see that a secret is configured
	// and echo the sentinel back on PUT.
	var m map[string]any
	_ = json.Unmarshal(out.AuthConfig, &m)
	if m["password"] != SecretSentinel || m["token"] != SecretSentinel {
		t.Fatalf("sentinel not reconstructed from the sealed document: %v", m)
	}
	if m["username"] != "u" {
		t.Fatalf("username should survive redaction: %v", m)
	}
}

// TestRedactionFailsClosedWhenCredentialIsUndecryptable — a key problem must
// not turn the redactor into a ciphertext leak.
func TestRedactionFailsClosedWhenCredentialIsUndecryptable(t *testing.T) {
	repo := sealRepo(t, testEncryptor(t), "private", "https://charts.example.com", "basic",
		`{"username":"u","password":"s3cret"}`)
	h := &CatalogHandler{log: slog.Default()}
	h.SetEncryptor(testEncryptor(t)) // wrong key

	out := h.redactHelmRepository(repo)
	body, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), repo.AuthConfigEncrypted) {
		t.Fatalf("undecryptable credential leaked as ciphertext: %s", body)
	}
	var m map[string]any
	_ = json.Unmarshal(out.AuthConfig, &m)
	if _, present := m["password"]; present {
		t.Fatalf("password key must be absent when it cannot be read: %v", m)
	}
}

// TestTestRepoConnectionReportsUndecryptableCredential covers the HTTP
// (index.yaml) branch of the test-connection endpoint.
//
// Everywhere else, an unreadable credential correctly degrades to an
// unauthenticated request — the upstream 401 is a real, if unhelpful, error.
// Test-connection is the exception: it is the one endpoint where the operator
// is explicitly asking "does this credential work", so reporting the upstream's
// 401 would answer "your password is wrong" when the actual fault is a wrong or
// rotated ASTRONOMER_ENCRYPTION_KEY. That is the misdiagnosis
// catalog.ErrAuthConfigUnavailable exists to prevent. The OCI branch already
// handled it; only this branch was left going through the log-and-continue
// helper.
func TestTestRepoConnectionReportsUndecryptableCredential(t *testing.T) {
	// Sealed under one key, read with another (rotation dropped the old key).
	// A literal public IP keeps httpclient.GuardPublicHost happy without DNS;
	// the handler must answer before any connection is attempted.
	sealedUnderOldKey := sealRepo(t, testEncryptor(t), "private", "https://93.184.216.34",
		"basic", `{"username":"u","password":"p"}`)

	h := &CatalogHandler{
		log:     slog.Default(),
		queries: &sealingCatalogQuerier{existing: sealedUnderOldKey},
	}
	h.SetEncryptor(testEncryptor(t))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/catalog/repositories/"+sealedUnderOldKey.ID.String()+"/test-connection/", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", sealedUnderOldKey.ID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	h.TestRepoConnection(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (the probe result is the body, not the status): %s", rec.Code, rec.Body.String())
	}
	var envelope struct {
		Data struct {
			Success bool   `json:"success"`
			Message string `json:"message"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	body := envelope.Data
	if body.Success {
		t.Fatalf("an undecryptable credential must not report a successful connection: %s", rec.Body.String())
	}
	if !strings.Contains(strings.ToLower(body.Message), "decrypt") {
		t.Fatalf("message must name the key-management fault, not an auth failure: %q", body.Message)
	}
	if strings.Contains(rec.Body.String(), sealedUnderOldKey.AuthConfigEncrypted) {
		t.Fatal("ciphertext leaked into the response body")
	}
}
