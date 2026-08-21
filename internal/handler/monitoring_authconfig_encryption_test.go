package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	imonitoring "github.com/alphabravocompany/astronomer-go/internal/monitoring"
)

// monitoring_backends.auth_config is a mixed credential/config bag: the
// Thanos/Prometheus credential shares the column with operationPolicies,
// sharedThanos, sharedAlertmanager and sharedAlertingAssets. FIVE paths do a
// read-modify-write on it to change one of those NON-secret keys, and each one
// is a chance to write the stripped projection back as if it were the whole
// document — which deletes the credential during an edit that had nothing to
// do with it.
//
// There is one test per RMW site below rather than one shared table, on
// purpose: the point is that each site is separately capable of causing this,
// and a shared helper that stopped covering one of them would fail nothing.
//
//	UpdateBackendConfig               operator edits the backend
//	updateSharedThanosMetadata        shared Thanos install/uninstall
//	updateSharedAlertmanagerMetadata  shared Alertmanager install/uninstall
//	persistSharedAlertingAssetHashes  alert rule / channel edit (alerting.go)
//	reconcileMonitoringBackend        the 30s health stamp (worker; covered in
//	                                  internal/worker/tasks)
const monitoringTestToken = "s3cr3t-thanos-token"

type monitoringAuthConfigQuerier struct {
	MonitoringQuerier
	backend sqlc.MonitoringBackend
	getErr  error
	upserts []sqlc.UpsertDefaultMonitoringBackendParams
}

func (q *monitoringAuthConfigQuerier) GetDefaultMonitoringBackend(context.Context) (sqlc.MonitoringBackend, error) {
	if q.getErr != nil {
		return sqlc.MonitoringBackend{}, q.getErr
	}
	return q.backend, nil
}

func (q *monitoringAuthConfigQuerier) UpsertDefaultMonitoringBackend(_ context.Context, arg sqlc.UpsertDefaultMonitoringBackendParams) (sqlc.MonitoringBackend, error) {
	q.upserts = append(q.upserts, arg)
	q.backend.BackendType = arg.BackendType
	q.backend.QueryUrl = arg.QueryUrl
	q.backend.AlertmanagerUrl = arg.AlertmanagerUrl
	q.backend.TenantID = arg.TenantID
	q.backend.AuthType = arg.AuthType
	q.backend.AuthConfig = arg.AuthConfig
	q.backend.AuthConfigEncrypted = arg.AuthConfigEncrypted
	return q.backend, nil
}

// alertingAuthConfigQuerier is the same fake against the alerting handler's
// narrower interface.
type alertingAuthConfigQuerier struct {
	AlertingQuerier
	upserts []sqlc.UpsertDefaultMonitoringBackendParams
}

func (q *alertingAuthConfigQuerier) UpsertDefaultMonitoringBackend(_ context.Context, arg sqlc.UpsertDefaultMonitoringBackendParams) (sqlc.MonitoringBackend, error) {
	q.upserts = append(q.upserts, arg)
	return sqlc.MonitoringBackend{AuthConfig: arg.AuthConfig, AuthConfigEncrypted: arg.AuthConfigEncrypted}, nil
}

func newMonitoringTestEncryptor(t *testing.T) *auth.Encryptor {
	t.Helper()
	key, err := auth.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	enc, err := auth.NewEncryptor(key)
	if err != nil {
		t.Fatalf("NewEncryptor: %v", err)
	}
	return enc
}

// sealedMonitoringBackend builds the post-146 row shape: the credential lives
// only in the envelope, the config bag stays in the JSONB.
func sealedMonitoringBackend(t *testing.T, enc *auth.Encryptor, doc string) sqlc.MonitoringBackend {
	t.Helper()
	ciphertext, public, err := imonitoring.SealAuthConfig(json.RawMessage(doc), enc)
	if err != nil {
		t.Fatalf("SealAuthConfig: %v", err)
	}
	if ciphertext == "" {
		t.Fatalf("test fixture %s produced no envelope; it carries nothing secret", doc)
	}
	if strings.Contains(string(public), monitoringTestToken) {
		t.Fatalf("test fixture left the credential in the plaintext column: %s", public)
	}
	return sqlc.MonitoringBackend{
		ID:                  uuid.New(),
		Name:                "default",
		BackendType:         "thanos",
		QueryUrl:            "https://thanos.example/",
		AuthType:            "bearer",
		AuthConfig:          public,
		AuthConfigEncrypted: ciphertext,
		DefaultStepSeconds:  300,
		TimeoutSeconds:      30,
	}
}

// assertCredentialSurvived is the shared assertion, not a shared test: the
// written row must still decrypt to the credential, and must not carry it in
// the clear.
func assertCredentialSurvived(t *testing.T, enc *auth.Encryptor, params sqlc.UpsertDefaultMonitoringBackendParams) map[string]any {
	t.Helper()
	if params.AuthConfigEncrypted == "" {
		t.Fatalf("write left auth_config_encrypted empty: the credential was dropped (auth_config=%s)", params.AuthConfig)
	}
	if strings.Contains(string(params.AuthConfig), monitoringTestToken) {
		t.Fatalf("write put the credential back in the clear: %s", params.AuthConfig)
	}
	full, err := imonitoring.ResolveAuthConfig(params.AuthConfigEncrypted, params.AuthConfig, enc)
	if err != nil {
		t.Fatalf("ResolveAuthConfig on the written row: %v", err)
	}
	doc := imonitoring.DecodeAuthConfig(full)
	if doc["token"] != monitoringTestToken {
		t.Fatalf("credential did not survive the write: %v", doc)
	}
	return doc
}

// RMW SITE 1 — UpdateBackendConfig.
//
// Pre-fix this was a pure writer: req.AuthConfig became the whole stored
// document. Combined with a read that redacts the credential, the UI's own
// GET → edit → PUT round-trip posted an authConfig with no credential in it
// and the credential was gone. Changing the request timeout deleted it.
func TestUpdateBackendConfigPreservesCredentialWhenAuthConfigOmitted(t *testing.T) {
	enc := newMonitoringTestEncryptor(t)
	q := &monitoringAuthConfigQuerier{backend: sealedMonitoringBackend(t, enc,
		`{"token":"`+monitoringTestToken+`","operationPolicies":{"maxRetryAttempts":3}}`)}
	h := NewMonitoringHandlerWithQueries(q, nil)
	h.SetEncryptor(enc)

	// A body with no authConfig at all: the operator is changing the timeout.
	body := `{"queryUrl":"https://thanos.example/","authType":"bearer","timeoutSeconds":45}`
	rr := httptest.NewRecorder()
	h.UpdateBackendConfig(rr, httptest.NewRequest(http.MethodPut, "/api/v1/settings/monitoring/backend/", strings.NewReader(body)))
	if rr.Code != http.StatusOK {
		t.Fatalf("UpdateBackendConfig status = %d, body %s", rr.Code, rr.Body.String())
	}
	if len(q.upserts) != 1 {
		t.Fatalf("expected one upsert, got %d", len(q.upserts))
	}
	assertCredentialSurvived(t, enc, q.upserts[0])
	if q.upserts[0].TimeoutSeconds != 45 {
		t.Fatalf("the edit the operator actually asked for was lost: %+v", q.upserts[0])
	}
}

// A PRESENT authConfig is authoritative for the credential, but must not wipe
// the config-bag keys the client never sends. Pre-fix, every backend edit also
// discarded the shared-Thanos deployment metadata stored in this column.
func TestUpdateBackendConfigReplacesCredentialButKeepsSharedStackMetadata(t *testing.T) {
	enc := newMonitoringTestEncryptor(t)
	q := &monitoringAuthConfigQuerier{backend: sealedMonitoringBackend(t, enc,
		`{"token":"`+monitoringTestToken+`","sharedThanos":{"namespace":"monitoring","releaseName":"thanos"}}`)}
	h := NewMonitoringHandlerWithQueries(q, nil)
	h.SetEncryptor(enc)

	body := `{"queryUrl":"https://thanos.example/","authType":"bearer","authConfig":{"token":"rotated-token"}}`
	rr := httptest.NewRecorder()
	h.UpdateBackendConfig(rr, httptest.NewRequest(http.MethodPut, "/api/v1/settings/monitoring/backend/", strings.NewReader(body)))
	if rr.Code != http.StatusOK {
		t.Fatalf("UpdateBackendConfig status = %d, body %s", rr.Code, rr.Body.String())
	}
	full, err := imonitoring.ResolveAuthConfig(q.upserts[0].AuthConfigEncrypted, q.upserts[0].AuthConfig, enc)
	if err != nil {
		t.Fatalf("ResolveAuthConfig: %v", err)
	}
	doc := imonitoring.DecodeAuthConfig(full)
	if doc["token"] != "rotated-token" {
		t.Fatalf("the caller's new credential was not stored: %v", doc)
	}
	shared, _ := doc["sharedThanos"].(map[string]any)
	if shared["releaseName"] != "thanos" {
		t.Fatalf("the shared-Thanos deployment metadata was discarded by an unrelated backend edit: %v", doc)
	}
}

// The WRITE RESPONSE must not carry the credential either — in BOTH the
// omitted-authConfig and the supplied-authConfig case.
//
// This is the regression that turning UpdateBackendConfig into a
// read-modify-write introduced, and it is the exact inverse of the change's
// purpose: the token comes out of the Fernet envelope and goes back on the
// wire, into browser devtools, proxies and any response logging, on an edit
// that had nothing to do with the credential. The old write response echoed
// only `req.AuthConfig`, so echoing it back genuinely leaked nothing; once the
// rendered document became "the caller's input merged over the STORED one", a
// PUT of `{"queryUrl":...,"timeoutSeconds":45}` answered 200 with the stored
// token. monitoring:update and monitoring:create are WRITE verbs — the whole
// point of GetBackendConfig's redaction is that neither of them is a way to
// READ the credential.
//
// The omitted case is the disclosure. The supplied case is here so nobody
// "fixes" this by reintroducing a conditional echo: even when the caller did
// send a credential, there is no reason for the response to repeat it, and a
// conditional is one merge away from being wrong again.
func TestUpdateBackendConfigResponseNeverCarriesTheCredential(t *testing.T) {
	enc := newMonitoringTestEncryptor(t)
	stored := `{"token":"` + monitoringTestToken + `","operationPolicies":{"maxRetryAttempts":3}}`

	t.Run("authConfig omitted", func(t *testing.T) {
		q := &monitoringAuthConfigQuerier{backend: sealedMonitoringBackend(t, enc, stored)}
		h := NewMonitoringHandlerWithQueries(q, nil)
		h.SetEncryptor(enc)

		rr := httptest.NewRecorder()
		h.UpdateBackendConfig(rr, httptest.NewRequest(http.MethodPut, "/api/v1/settings/monitoring/backend/",
			strings.NewReader(`{"queryUrl":"https://thanos.example/","authType":"bearer","timeoutSeconds":45}`)))
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
		}
		assertWriteResponseIsRedacted(t, rr.Body.String())
	})

	t.Run("authConfig supplied", func(t *testing.T) {
		q := &monitoringAuthConfigQuerier{backend: sealedMonitoringBackend(t, enc, stored)}
		h := NewMonitoringHandlerWithQueries(q, nil)
		h.SetEncryptor(enc)

		rr := httptest.NewRecorder()
		h.UpdateBackendConfig(rr, httptest.NewRequest(http.MethodPut, "/api/v1/settings/monitoring/backend/",
			strings.NewReader(`{"queryUrl":"https://thanos.example/","authType":"bearer","authConfig":{"token":"`+monitoringTestToken+`"}}`)))
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
		}
		assertWriteResponseIsRedacted(t, rr.Body.String())
	})
}

// assertWriteResponseIsRedacted pins the write response to the same shape the
// read has: no credential, no ciphertext — but still the operationPolicies
// block and the credential KEY NAMES, so a successful save does not read to
// the UI as a dropped credential. That last part is why the response renders
// the document the request produced rather than re-reading the row: the stored
// JSONB is the stripped projection, so a re-read would answer
// `authConfigKeys: []`.
func assertWriteResponseIsRedacted(t *testing.T, body string) {
	t.Helper()
	if strings.Contains(body, monitoringTestToken) {
		t.Fatalf("the write response echoed the credential back on the wire: %s", body)
	}
	var envelope struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("unmarshal write response: %v", err)
	}
	data := envelope.Data
	if data == nil {
		// Some Respond helpers render the object at the top level.
		if err := json.Unmarshal([]byte(body), &data); err != nil {
			t.Fatalf("unmarshal write response: %v", err)
		}
	}
	authConfig, _ := data["authConfig"].(map[string]any)
	if _, ok := authConfig["operationPolicies"]; !ok {
		t.Fatalf("the write response dropped the non-secret operationPolicies block: %v", data["authConfig"])
	}
	if len(authConfig) != 1 {
		t.Fatalf("the write response authConfig carries more than operationPolicies: %v", authConfig)
	}
	keys, _ := data["authConfigKeys"].([]any)
	if len(keys) != 1 || keys[0] != "token" {
		t.Fatalf("authConfigKeys = %v, want [token]: a successful save must not read as a dropped credential", keys)
	}
}

// RMW SITE 2 — updateSharedThanosMetadata.
//
// Runs on every shared-Thanos install / replace / uninstall. Pre-fix it did
// decodeJSONMap(backend.AuthConfig) → mutate → marshal, so on a sealed row it
// would have written back the projection: deploying the stack would have
// deleted the credential the stack then needs.
func TestUpdateSharedThanosMetadataPreservesCredential(t *testing.T) {
	enc := newMonitoringTestEncryptor(t)
	backend := sealedMonitoringBackend(t, enc, `{"token":"`+monitoringTestToken+`"}`)
	q := &monitoringAuthConfigQuerier{backend: backend}
	h := NewMonitoringHandlerWithQueries(q, nil)
	h.SetEncryptor(enc)

	if err := h.updateSharedThanosMetadata(context.Background(), backend, SharedThanosStackRequest{
		ManagementClusterID: uuid.New().String(),
		Namespace:           "monitoring",
		ReleaseName:         "thanos",
	}, "installing"); err != nil {
		t.Fatalf("updateSharedThanosMetadata: %v", err)
	}
	doc := assertCredentialSurvived(t, enc, q.upserts[0])
	shared, _ := doc["sharedThanos"].(map[string]any)
	if shared["status"] != "installing" {
		t.Fatalf("the metadata stamp this call exists to make was lost: %v", doc)
	}
}

// RMW SITE 3 — updateSharedAlertmanagerMetadata.
func TestUpdateSharedAlertmanagerMetadataPreservesCredential(t *testing.T) {
	enc := newMonitoringTestEncryptor(t)
	backend := sealedMonitoringBackend(t, enc, `{"token":"`+monitoringTestToken+`"}`)
	q := &monitoringAuthConfigQuerier{backend: backend}
	h := NewMonitoringHandlerWithQueries(q, nil)
	h.SetEncryptor(enc)

	if err := h.updateSharedAlertmanagerMetadata(context.Background(), backend, SharedAlertmanagerRequest{
		ManagementClusterID: uuid.New().String(),
		Namespace:           "monitoring",
		ReleaseName:         "astronomer-alertmanager",
	}, "installing"); err != nil {
		t.Fatalf("updateSharedAlertmanagerMetadata: %v", err)
	}
	doc := assertCredentialSurvived(t, enc, q.upserts[0])
	shared, _ := doc["sharedAlertmanager"].(map[string]any)
	if shared["status"] != "installing" {
		t.Fatalf("the metadata stamp this call exists to make was lost: %v", doc)
	}
}

// RMW SITE 4 — persistSharedAlertingAssetHashes (internal/handler/alerting.go).
//
// The least obvious of the five: it runs as a side effect of an alert-rule or
// notification-channel edit. Pre-fix, on a sealed row, adding a Slack channel
// would have deleted the Thanos credential.
func TestPersistSharedAlertingAssetHashesPreservesCredential(t *testing.T) {
	enc := newMonitoringTestEncryptor(t)
	backend := sealedMonitoringBackend(t, enc, `{"token":"`+monitoringTestToken+`"}`)
	q := &alertingAuthConfigQuerier{}
	h := NewAlertingHandler(q)
	h.SetEncryptor(enc)

	if err := h.persistSharedAlertingAssetHashes(context.Background(), backend, map[string]any{
		"rulerRules": "abc123",
	}); err != nil {
		t.Fatalf("persistSharedAlertingAssetHashes: %v", err)
	}
	if len(q.upserts) != 1 {
		t.Fatalf("expected one upsert, got %d", len(q.upserts))
	}
	doc := assertCredentialSurvived(t, enc, q.upserts[0])
	assets, _ := doc["sharedAlertingAssets"].(map[string]any)
	hashes, _ := assets["hashes"].(map[string]any)
	if hashes["rulerRules"] != "abc123" {
		t.Fatalf("the asset hashes this call exists to persist were lost: %v", doc)
	}
}

func TestUpdateSharedGrafanaMetadataPreservesCredential(t *testing.T) {
	enc := newMonitoringTestEncryptor(t)
	backend := sealedMonitoringBackend(t, enc, `{"token":"`+monitoringTestToken+`"}`)
	q := &monitoringAuthConfigQuerier{backend: backend}
	h := NewMonitoringHandlerWithQueries(q, nil)
	h.SetEncryptor(enc)

	if err := h.updateSharedGrafanaMetadata(context.Background(), backend, SharedGrafanaRequest{
		ManagementClusterID: uuid.New().String(),
		Namespace:           "monitoring",
		ReleaseName:         sharedGrafanaDefaultRelease,
		ChartVersion:        sharedGrafanaDefaultChart,
	}, "installing"); err != nil {
		t.Fatalf("updateSharedGrafanaMetadata: %v", err)
	}
	doc := assertCredentialSurvived(t, enc, q.upserts[0])
	shared, _ := doc["sharedGrafana"].(map[string]any)
	if shared["status"] != "installing" {
		t.Fatalf("the metadata stamp this call exists to make was lost: %v", doc)
	}
	if shared["authMode"] != sharedGrafanaAuthModeProxy {
		t.Fatalf("authMode = %v", shared["authMode"])
	}
}

// A write that cannot read the existing credential must ABORT, not proceed.
// Proceeding would stamp the metadata and delete the credential, converting a
// recoverable key-management problem (wrong ASTRONOMER_ENCRYPTION_KEY, a
// rotation that dropped a key too early) into a permanent data loss.
func TestUpdateSharedLokiMetadataPreservesCredential(t *testing.T) {
	enc := newMonitoringTestEncryptor(t)
	backend := sealedMonitoringBackend(t, enc, `{"token":"`+monitoringTestToken+`"}`)
	q := &monitoringAuthConfigQuerier{backend: backend}
	h := NewMonitoringHandlerWithQueries(q, nil)
	h.SetEncryptor(enc)

	if err := h.updateSharedLokiMetadata(context.Background(), backend, SharedLokiRequest{
		ManagementClusterID: uuid.New().String(),
		Namespace:           "monitoring",
		ReleaseName:         sharedLokiDefaultRelease,
		ChartVersion:        sharedLokiDefaultChart,
		IngestHostname:      "loki-ingest.example.com",
	}, "installing"); err != nil {
		t.Fatalf("updateSharedLokiMetadata: %v", err)
	}
	doc := assertCredentialSurvived(t, enc, q.upserts[0])
	shared, _ := doc["sharedLoki"].(map[string]any)
	if shared["status"] != "installing" {
		t.Fatalf("the metadata stamp this call exists to make was lost: %v", doc)
	}
	if shared["ingestPublic"] != false {
		t.Fatalf("ingestPublic = %v, want false", shared["ingestPublic"])
	}
}

func TestMonitoringWritesAbortWhenTheCredentialCannotBeDecrypted(t *testing.T) {
	sealingEnc := newMonitoringTestEncryptor(t)
	backend := sealedMonitoringBackend(t, sealingEnc, `{"token":"`+monitoringTestToken+`"}`)
	// A DIFFERENT key — the rotated-too-early case.
	wrongEnc := newMonitoringTestEncryptor(t)

	t.Run("updateSharedThanosMetadata", func(t *testing.T) {
		q := &monitoringAuthConfigQuerier{backend: backend}
		h := NewMonitoringHandlerWithQueries(q, nil)
		h.SetEncryptor(wrongEnc)
		if err := h.updateSharedThanosMetadata(context.Background(), backend, SharedThanosStackRequest{}, "installing"); err == nil {
			t.Fatal("expected an error, not a write that drops the credential")
		}
		if len(q.upserts) != 0 {
			t.Fatalf("wrote %d row(s) despite being unable to read the credential", len(q.upserts))
		}
	})

	t.Run("updateSharedAlertmanagerMetadata", func(t *testing.T) {
		q := &monitoringAuthConfigQuerier{backend: backend}
		h := NewMonitoringHandlerWithQueries(q, nil)
		h.SetEncryptor(wrongEnc)
		if err := h.updateSharedAlertmanagerMetadata(context.Background(), backend, SharedAlertmanagerRequest{}, "installing"); err == nil {
			t.Fatal("expected an error, not a write that drops the credential")
		}
		if len(q.upserts) != 0 {
			t.Fatalf("wrote %d row(s) despite being unable to read the credential", len(q.upserts))
		}
	})

	t.Run("updateSharedGrafanaMetadata", func(t *testing.T) {
		q := &monitoringAuthConfigQuerier{backend: backend}
		h := NewMonitoringHandlerWithQueries(q, nil)
		h.SetEncryptor(wrongEnc)
		if err := h.updateSharedGrafanaMetadata(context.Background(), backend, SharedGrafanaRequest{}, "installing"); err == nil {
			t.Fatal("expected an error, not a write that drops the credential")
		}
		if len(q.upserts) != 0 {
			t.Fatalf("wrote %d row(s) despite being unable to read the credential", len(q.upserts))
		}
	})

	t.Run("persistSharedAlertingAssetHashes", func(t *testing.T) {
		q := &alertingAuthConfigQuerier{}
		h := NewAlertingHandler(q)
		h.SetEncryptor(wrongEnc)
		if err := h.persistSharedAlertingAssetHashes(context.Background(), backend, map[string]any{"rulerRules": "x"}); err == nil {
			t.Fatal("expected an error, not a write that drops the credential")
		}
		if len(q.upserts) != 0 {
			t.Fatalf("wrote %d row(s) despite being unable to read the credential", len(q.upserts))
		}
	})

	t.Run("UpdateBackendConfig", func(t *testing.T) {
		q := &monitoringAuthConfigQuerier{backend: backend}
		h := NewMonitoringHandlerWithQueries(q, nil)
		h.SetEncryptor(wrongEnc)
		rr := httptest.NewRecorder()
		h.UpdateBackendConfig(rr, httptest.NewRequest(http.MethodPut, "/api/v1/settings/monitoring/backend/",
			strings.NewReader(`{"queryUrl":"https://thanos.example/"}`)))
		if rr.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500 rather than a silent credential drop", rr.Code)
		}
		if len(q.upserts) != 0 {
			t.Fatalf("wrote %d row(s) despite being unable to read the credential", len(q.upserts))
		}
	})
}

// A first save has nothing to preserve and must not 500 on the missing row.
func TestUpdateBackendConfigCreatesTheFirstBackend(t *testing.T) {
	enc := newMonitoringTestEncryptor(t)
	q := &monitoringAuthConfigQuerier{getErr: pgx.ErrNoRows}
	h := NewMonitoringHandlerWithQueries(q, nil)
	h.SetEncryptor(enc)

	rr := httptest.NewRecorder()
	h.UpdateBackendConfig(rr, httptest.NewRequest(http.MethodPut, "/api/v1/settings/monitoring/backend/",
		strings.NewReader(`{"queryUrl":"https://thanos.example/","authType":"bearer","authConfig":{"token":"`+monitoringTestToken+`"}}`)))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	assertCredentialSurvived(t, enc, q.upserts[0])
}

// The read surface must never carry the credential OR the ciphertext. The
// ciphertext is not a secret, but shipping it would invite a client to treat
// it as one — and the response DTO is the only thing standing between the sqlc
// row and the wire.
func TestGetBackendConfigNeverReturnsCredentialOrCiphertext(t *testing.T) {
	enc := newMonitoringTestEncryptor(t)
	backend := sealedMonitoringBackend(t, enc,
		`{"token":"`+monitoringTestToken+`","operationPolicies":{"maxRetryAttempts":3}}`)
	q := &monitoringAuthConfigQuerier{backend: backend}
	h := NewMonitoringHandlerWithQueries(q, nil)
	h.SetEncryptor(enc)

	rr := httptest.NewRecorder()
	h.GetBackendConfig(rr, httptest.NewRequest(http.MethodGet, "/api/v1/settings/monitoring/backend/", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if strings.Contains(body, monitoringTestToken) {
		t.Fatalf("read response leaked the credential: %s", body)
	}
	if strings.Contains(body, backend.AuthConfigEncrypted) {
		t.Fatalf("read response shipped the ciphertext column: %s", body)
	}
	if strings.Contains(body, "authConfigEncrypted") || strings.Contains(body, "auth_config_encrypted") {
		t.Fatalf("read response exposes the envelope column by name: %s", body)
	}

	// "configured, not disclosed" survives sealing: the key NAME is still
	// reported even though its value now lives only in the envelope. Deriving
	// this from the stored projection would report an empty list and tell an
	// operator no credential is configured when one is.
	var envelope struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	keys, _ := envelope.Data["authConfigKeys"].([]any)
	if len(keys) != 1 || keys[0] != "token" {
		t.Fatalf("authConfigKeys = %v, want [token]", keys)
	}
}

// A pre-146 row (empty envelope, credential inline) must keep working across
// the upgrade, and the API must still redact it.
func TestGetBackendConfigRedactsAPreMigrationRow(t *testing.T) {
	q := &monitoringAuthConfigQuerier{backend: sqlc.MonitoringBackend{
		ID:         uuid.New(),
		AuthType:   "bearer",
		AuthConfig: json.RawMessage(`{"token":"` + monitoringTestToken + `"}`),
	}}
	h := NewMonitoringHandlerWithQueries(q, nil)
	h.SetEncryptor(newMonitoringTestEncryptor(t))

	rr := httptest.NewRecorder()
	h.GetBackendConfig(rr, httptest.NewRequest(http.MethodGet, "/api/v1/settings/monitoring/backend/", nil))
	if strings.Contains(rr.Body.String(), monitoringTestToken) {
		t.Fatalf("read response leaked the pre-146 plaintext credential: %s", rr.Body.String())
	}
}

// The API redaction allow-list must stay a SUBSET of the at-rest allow-list.
// Narrower is always safe; wider would render a key the envelope had sealed,
// which is a leak the redaction test above would not catch on its own because
// it only exercises the keys it happens to name.
func TestRedactedMonitoringAuthConfigIsSubsetOfNonSecret(t *testing.T) {
	doc := map[string]any{}
	for _, key := range imonitoring.NonSecretAuthConfigKeys {
		doc[key] = map[string]any{"probe": true}
	}
	doc["token"] = monitoringTestToken
	for key := range redactedMonitoringAuthConfig(doc) {
		if !slices.Contains(imonitoring.NonSecretAuthConfigKeys, key) {
			t.Fatalf("API redaction exposes %q, which the at-rest split seals into the envelope", key)
		}
	}
}
