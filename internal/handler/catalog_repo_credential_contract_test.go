package handler

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/catalog"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// Credential contract for POST/PUT /api/v1/catalog/repositories/.
//
// The bug these cover: the UI's "Authentication (optional)" fields posted
// `{"username":..,"password":..}` at the TOP LEVEL of the body, while the
// handler reads credentials from `auth_config`. encoding/json drops unknown
// fields without complaint, so an operator who filled in the credential got a
// repository saved with no credential at all — and the first symptom was a
// 401 from the registry days later, which reads as a wrong password rather
// than a dropped one.
//
// The same body also omitted `enabled` (decoding to false, which excluded the
// repository from ListEnabledHelmRepositories and therefore from the scheduled
// sweep entirely) and spelled `repoType` instead of `repo_type`.
//
// The assertions below deliberately end at catalog.ApplyIndexAuth — the
// function the sweep and the interactive sync both call — rather than at "the
// right field was set". A credential that is stored but never sent is the same
// outage as a credential that was never stored.

// uiCreateBody is the body frontend/src/lib/api.ts now sends for a private
// repository. It is written out longhand so a change on either side of the
// contract fails here.
const uiCreateBody = `{"name":"private","url":"https://charts.example.com","repo_type":"helm",` +
	`"description":"","enabled":true,"auth_type":"basic",` +
	`"auth_config":{"username":"deploy","password":"s3cret"}}`

// storedRow reconstructs the row the database would hand back for the params
// the handler wrote, so read-path assertions run against what was persisted
// rather than against what the handler happened to have in memory.
func storedRow(arg sqlc.CreateHelmRepositoryParams) sqlc.HelmRepository {
	return sqlc.HelmRepository{
		Name: arg.Name, Url: arg.Url, RepoType: arg.RepoType,
		AuthType: arg.AuthType, AuthConfig: arg.AuthConfig,
		AuthConfigEncrypted: arg.AuthConfigEncrypted, Enabled: arg.Enabled,
	}
}

// TestCreateRepoFromUIBodyStoresCredentialAndSyncPathSendsIt is the end-to-end
// assertion: what the Add Repository form posts must come back out of the
// database as an Authorization header on the index fetch.
func TestCreateRepoFromUIBodyStoresCredentialAndSyncPathSendsIt(t *testing.T) {
	enc := testEncryptor(t)
	q := &sealingCatalogQuerier{}
	h := &CatalogHandler{queries: q, log: slog.Default()}
	h.SetEncryptor(enc)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/catalog/repositories/", bytes.NewBufferString(uiCreateBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.CreateRepo(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status %d body %s", rec.Code, rec.Body.String())
	}

	// The credential survived the round trip in encrypted form...
	if q.created.AuthConfigEncrypted == "" {
		t.Fatal("nothing was sealed: the credential the operator typed was dropped")
	}
	if strings.Contains(string(q.created.AuthConfig), "s3cret") {
		t.Fatalf("password left in the plaintext column: %s", q.created.AuthConfig)
	}
	// ...the repo type the operator chose was not discarded...
	if q.created.RepoType != "helm" {
		t.Fatalf("repo_type = %q, want %q", q.created.RepoType, "helm")
	}
	// ...and the row is visible to the scheduled sweep, which only ever reads
	// enabled repositories.
	if !q.created.Enabled {
		t.Fatal("repository created disabled: the scheduled sweep will never sync it")
	}

	// End to end: the stored row must authenticate the index fetch.
	idxReq := httptest.NewRequest(http.MethodGet, "https://charts.example.com/index.yaml", nil)
	catalog.ApplyIndexAuth(idxReq, storedRow(q.created), enc, slog.Default())
	user, pass, ok := idxReq.BasicAuth()
	if !ok || user != "deploy" || pass != "s3cret" {
		t.Fatalf("sync path sent ok=%v user=%q pass=%q; want the credential the operator entered", ok, user, pass)
	}

	// The echo must not carry it back out.
	if got := rec.Body.String(); strings.Contains(got, "s3cret") || strings.Contains(got, q.created.AuthConfigEncrypted) {
		t.Fatalf("create response carries the credential or its ciphertext: %s", got)
	}
}

// TestCreateRepoRejectsTopLevelCredentials pins the pre-fix body as an error
// rather than a silent no-op. Dropping a credential the client clearly meant
// to set is never the right answer; 400 is.
func TestCreateRepoRejectsTopLevelCredentials(t *testing.T) {
	q := &sealingCatalogQuerier{}
	h := &CatalogHandler{queries: q, log: slog.Default()}
	h.SetEncryptor(testEncryptor(t))

	// Exactly what the UI used to post.
	body := `{"name":"private","url":"https://charts.example.com","repoType":"helm",` +
		`"username":"deploy","password":"s3cret"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/catalog/repositories/", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.CreateRepo(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400 for top-level credentials: %s", rec.Code, rec.Body.String())
	}
	if q.created.Name != "" {
		t.Fatal("a repository was created from a body whose credential could not be honoured")
	}
	if !strings.Contains(rec.Body.String(), "auth_config") {
		t.Fatalf("the error must name the field that works: %s", rec.Body.String())
	}
}

// TestCreateRepoDefaultsToEnabledWhenOmitted — a client that does not mention
// `enabled` wants a working repository, not a dormant one.
func TestCreateRepoDefaultsToEnabledWhenOmitted(t *testing.T) {
	q := &sealingCatalogQuerier{}
	h := &CatalogHandler{queries: q, log: slog.Default()}
	h.SetEncryptor(testEncryptor(t))

	body := `{"name":"public","url":"https://charts.example.com"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/catalog/repositories/", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.CreateRepo(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status %d body %s", rec.Code, rec.Body.String())
	}
	if !q.created.Enabled {
		t.Fatal("enabled defaulted to false: the repository would never be swept")
	}

	// ...and an explicit false is still honoured.
	q2 := &sealingCatalogQuerier{}
	h2 := &CatalogHandler{queries: q2, log: slog.Default()}
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/catalog/repositories/",
		bytes.NewBufferString(`{"name":"paused","url":"https://charts.example.com","enabled":false}`))
	req2.Header.Set("Content-Type", "application/json")
	h2.CreateRepo(httptest.NewRecorder(), req2)
	if q2.created.Enabled {
		t.Fatal("explicit enabled:false was overridden by the default")
	}
}

// TestCreateRepoInfersAuthTypeFromCredential — auth_type and auth_config are
// two halves of one credential. A document with a username and password but no
// auth_type used to be stored intact and then never sent, because
// ApplyIndexAuth returns early on an empty auth_type.
func TestCreateRepoInfersAuthTypeFromCredential(t *testing.T) {
	enc := testEncryptor(t)
	q := &sealingCatalogQuerier{}
	h := &CatalogHandler{queries: q, log: slog.Default()}
	h.SetEncryptor(enc)

	body := `{"name":"private","url":"https://charts.example.com",` +
		`"auth_config":{"username":"deploy","password":"s3cret"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/catalog/repositories/", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.CreateRepo(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status %d body %s", rec.Code, rec.Body.String())
	}
	if q.created.AuthType != "basic" {
		t.Fatalf("auth_type = %q, want %q inferred from the credential", q.created.AuthType, "basic")
	}
	idxReq := httptest.NewRequest(http.MethodGet, "https://charts.example.com/index.yaml", nil)
	catalog.ApplyIndexAuth(idxReq, storedRow(q.created), enc, slog.Default())
	if _, _, ok := idxReq.BasicAuth(); !ok {
		t.Fatal("credential stored but never sent: auth_type was not derived")
	}
}

// --- update -----------------------------------------------------------------

// TestUpdateRepoWithoutAuthConfigKeepsStoredCredential is the worst of the
// family and the reason UpdateRepoRequest is all pointers.
//
// Pre-fix, an absent auth_config was normalised to `{}` before the sentinel
// merge ran, which made the merge's own "empty incoming means leave it alone"
// guard dead code. A PUT that renamed a repository — or changed nothing at all
// — wiped the credential AND the envelope, and the operator found out when the
// next sync 401'd.
func TestUpdateRepoWithoutAuthConfigKeepsStoredCredential(t *testing.T) {
	enc := testEncryptor(t)
	existing := sealRepo(t, enc, "private", "https://charts.example.com", "basic",
		`{"username":"deploy","password":"s3cret"}`)
	existing.Enabled = true
	q := &sealingCatalogQuerier{existing: existing}
	h := &CatalogHandler{queries: q, log: slog.Default()}
	h.SetEncryptor(enc)

	// A rename. Nothing about the credential is mentioned.
	body := `{"name":"private-renamed"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/catalog/repositories/"+existing.ID.String()+"/",
		bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.UpdateRepo(rec, withURLParam(req, "id", existing.ID.String()))

	if rec.Code != http.StatusOK {
		t.Fatalf("update: status %d body %s", rec.Code, rec.Body.String())
	}
	if q.updated.Name != "private-renamed" {
		t.Fatalf("the rename did not take: %q", q.updated.Name)
	}
	if q.updated.AuthConfigEncrypted == "" {
		t.Fatal("the rename cleared the credential envelope")
	}
	// Everything else the body did not mention survived too.
	if q.updated.Url != existing.Url || !q.updated.Enabled || q.updated.AuthType != "basic" {
		t.Fatalf("omitted fields were overwritten: url=%q enabled=%v auth_type=%q",
			q.updated.Url, q.updated.Enabled, q.updated.AuthType)
	}

	// End to end: the surviving credential is still the working one.
	idxReq := httptest.NewRequest(http.MethodGet, "https://charts.example.com/index.yaml", nil)
	catalog.ApplyIndexAuth(idxReq, sqlc.HelmRepository{
		Name: q.updated.Name, Url: q.updated.Url, AuthType: q.updated.AuthType,
		AuthConfig: q.updated.AuthConfig, AuthConfigEncrypted: q.updated.AuthConfigEncrypted,
	}, enc, slog.Default())
	user, pass, ok := idxReq.BasicAuth()
	if !ok || user != "deploy" || pass != "s3cret" {
		t.Fatalf("credential did not survive the rename: ok=%v user=%q pass=%q", ok, user, pass)
	}
}

// TestUpdateRepoWithNewCredentialReplacesStored — the other direction. A
// rotation has to actually rotate.
func TestUpdateRepoWithNewCredentialReplacesStored(t *testing.T) {
	enc := testEncryptor(t)
	existing := sealRepo(t, enc, "private", "https://charts.example.com", "basic",
		`{"username":"deploy","password":"old-secret"}`)
	q := &sealingCatalogQuerier{existing: existing}
	h := &CatalogHandler{queries: q, log: slog.Default()}
	h.SetEncryptor(enc)

	body := `{"auth_config":{"username":"deploy","password":"new-secret"}}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/catalog/repositories/"+existing.ID.String()+"/",
		bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.UpdateRepo(rec, withURLParam(req, "id", existing.ID.String()))

	if rec.Code != http.StatusOK {
		t.Fatalf("update: status %d body %s", rec.Code, rec.Body.String())
	}
	idxReq := httptest.NewRequest(http.MethodGet, "https://charts.example.com/index.yaml", nil)
	catalog.ApplyIndexAuth(idxReq, sqlc.HelmRepository{
		Name: "private", Url: existing.Url, AuthType: q.updated.AuthType,
		AuthConfig: q.updated.AuthConfig, AuthConfigEncrypted: q.updated.AuthConfigEncrypted,
	}, enc, slog.Default())
	_, pass, _ := idxReq.BasicAuth()
	if pass != "new-secret" {
		t.Fatalf("rotation did not take: sync path still sends %q", pass)
	}
}

// TestUpdateRepoWithEmptyAuthConfigClearsCredential — the deliberate removal
// has to stay possible, or "preserve on absent" becomes "can never unset".
func TestUpdateRepoWithEmptyAuthConfigClearsCredential(t *testing.T) {
	enc := testEncryptor(t)
	existing := sealRepo(t, enc, "private", "https://charts.example.com", "basic",
		`{"username":"deploy","password":"s3cret"}`)
	q := &sealingCatalogQuerier{existing: existing}
	h := &CatalogHandler{queries: q, log: slog.Default()}
	h.SetEncryptor(enc)

	body := `{"auth_type":"none","auth_config":{}}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/catalog/repositories/"+existing.ID.String()+"/",
		bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.UpdateRepo(rec, withURLParam(req, "id", existing.ID.String()))

	if rec.Code != http.StatusOK {
		t.Fatalf("update: status %d body %s", rec.Code, rec.Body.String())
	}
	if q.updated.AuthConfigEncrypted != "" {
		t.Fatal("explicit auth_config:{} left an envelope behind")
	}
	if string(q.updated.AuthConfig) != "{}" {
		t.Fatalf("explicit auth_config:{} left %s behind", q.updated.AuthConfig)
	}
}

// TestUpdateRepoRejectsTopLevelCredentials — same guard as create. A rotation
// posted in the wrong shape must not report success while changing nothing.
func TestUpdateRepoRejectsTopLevelCredentials(t *testing.T) {
	enc := testEncryptor(t)
	existing := sealRepo(t, enc, "private", "https://charts.example.com", "basic",
		`{"username":"deploy","password":"s3cret"}`)
	q := &sealingCatalogQuerier{existing: existing}
	h := &CatalogHandler{queries: q, log: slog.Default()}
	h.SetEncryptor(enc)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/catalog/repositories/"+existing.ID.String()+"/",
		bytes.NewBufferString(`{"name":"private","password":"rotated"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.UpdateRepo(rec, withURLParam(req, "id", existing.ID.String()))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if q.updated.Name != "" {
		t.Fatal("the update was applied despite the credential being unusable")
	}
}

// TestRepoWriteResponsesNeverEchoCredential covers both writes at once: no
// response body on this endpoint family may contain the secret the client just
// sent, the stored secret, or the ciphertext.
func TestRepoWriteResponsesNeverEchoCredential(t *testing.T) {
	enc := testEncryptor(t)
	existing := sealRepo(t, enc, "private", "https://charts.example.com", "basic",
		`{"username":"deploy","password":"s3cret"}`)
	q := &sealingCatalogQuerier{existing: existing}
	h := &CatalogHandler{queries: q, log: slog.Default()}
	h.SetEncryptor(enc)

	createRec := httptest.NewRecorder()
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/catalog/repositories/",
		bytes.NewBufferString(uiCreateBody))
	createReq.Header.Set("Content-Type", "application/json")
	h.CreateRepo(createRec, createReq)

	updateRec := httptest.NewRecorder()
	updateReq := httptest.NewRequest(http.MethodPut, "/api/v1/catalog/repositories/"+existing.ID.String()+"/",
		bytes.NewBufferString(`{"auth_config":{"username":"deploy","password":"rotated"}}`))
	updateReq.Header.Set("Content-Type", "application/json")
	h.UpdateRepo(updateRec, withURLParam(updateReq, "id", existing.ID.String()))

	for name, rec := range map[string]*httptest.ResponseRecorder{"create": createRec, "update": updateRec} {
		body := rec.Body.String()
		for _, forbidden := range []string{"s3cret", "rotated", existing.AuthConfigEncrypted, q.updated.AuthConfigEncrypted} {
			if forbidden != "" && strings.Contains(body, forbidden) {
				t.Fatalf("%s response leaked %q: %s", name, forbidden, body)
			}
		}
		// The username is not a secret and stays readable, so the UI can show
		// which account is configured.
		var env struct {
			Data struct {
				AuthConfig map[string]any `json:"auth_config"`
			} `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			t.Fatalf("%s response is not JSON: %v", name, err)
		}
		if env.Data.AuthConfig["username"] != "deploy" {
			t.Fatalf("%s response dropped the non-secret username: %v", name, env.Data.AuthConfig)
		}
		if env.Data.AuthConfig["password"] != SecretSentinel {
			t.Fatalf("%s response should mark the password as configured-but-hidden: %v", name, env.Data.AuthConfig)
		}
	}
}
