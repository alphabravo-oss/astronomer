// Handler tests for /api/v1/admin/gitops-sources/* — covers the
// superuser gate (non-admin -> 403, admin -> 200) and the
// auth-sentinel-on-PUT round-trip.
//
// These tests use an in-process fake GitOpsQuerier so no DB / Redis is
// required.

package handler

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type fakeGitOpsHandlerQuerier struct {
	user     sqlc.User
	sources  map[uuid.UUID]sqlc.GitopsRegistrationSource
	clusters map[uuid.UUID]sqlc.Cluster
	links    map[uuid.UUID][]sqlc.GitopsRegisteredCluster
	audits   int
	tasks    []sqlc.UpsertTaskOutboxParams
	receipts map[string]time.Time
}

func newFakeHandlerQuerier() *fakeGitOpsHandlerQuerier {
	return &fakeGitOpsHandlerQuerier{
		sources:  map[uuid.UUID]sqlc.GitopsRegistrationSource{},
		clusters: map[uuid.UUID]sqlc.Cluster{},
		links:    map[uuid.UUID][]sqlc.GitopsRegisteredCluster{},
		receipts: map[string]time.Time{},
	}
}

func (f *fakeGitOpsHandlerQuerier) GetUserByID(_ context.Context, _ uuid.UUID) (sqlc.User, error) {
	return f.user, nil
}
func (f *fakeGitOpsHandlerQuerier) ListGitOpsSources(_ context.Context) ([]sqlc.GitopsRegistrationSource, error) {
	out := []sqlc.GitopsRegistrationSource{}
	for _, s := range f.sources {
		out = append(out, s)
	}
	return out, nil
}
func (f *fakeGitOpsHandlerQuerier) GetGitOpsSource(_ context.Context, id uuid.UUID) (sqlc.GitopsRegistrationSource, error) {
	s, ok := f.sources[id]
	if !ok {
		return sqlc.GitopsRegistrationSource{}, pgx.ErrNoRows
	}
	return s, nil
}
func (f *fakeGitOpsHandlerQuerier) GetGitOpsSourceByName(_ context.Context, name string) (sqlc.GitopsRegistrationSource, error) {
	for _, s := range f.sources {
		if s.Name == name {
			return s, nil
		}
	}
	return sqlc.GitopsRegistrationSource{}, pgx.ErrNoRows
}
func (f *fakeGitOpsHandlerQuerier) CreateGitOpsSource(_ context.Context, arg sqlc.CreateGitOpsSourceParams) (sqlc.GitopsRegistrationSource, error) {
	row := sqlc.GitopsRegistrationSource{
		ID:                     uuid.New(),
		Name:                   arg.Name,
		RepoUrl:                arg.RepoUrl,
		Branch:                 arg.Branch,
		PathPrefix:             arg.PathPrefix,
		AuthMode:               arg.AuthMode,
		AuthEncrypted:          arg.AuthEncrypted,
		SyncMode:               arg.SyncMode,
		SyncIntervalSeconds:    arg.SyncIntervalSeconds,
		OnDelete:               arg.OnDelete,
		Enabled:                arg.Enabled,
		CreatedBy:              arg.CreatedBy,
		WebhookProvider:        arg.WebhookProvider,
		WebhookSecretEncrypted: arg.WebhookSecretEncrypted,
	}
	f.sources[row.ID] = row
	return row, nil
}
func (f *fakeGitOpsHandlerQuerier) UpdateGitOpsSource(_ context.Context, arg sqlc.UpdateGitOpsSourceParams) (sqlc.GitopsRegistrationSource, error) {
	row := f.sources[arg.ID]
	row.Name = arg.Name
	row.RepoUrl = arg.RepoUrl
	row.Branch = arg.Branch
	row.PathPrefix = arg.PathPrefix
	row.AuthMode = arg.AuthMode
	row.AuthEncrypted = arg.AuthEncrypted
	row.SyncMode = arg.SyncMode
	row.SyncIntervalSeconds = arg.SyncIntervalSeconds
	row.OnDelete = arg.OnDelete
	row.Enabled = arg.Enabled
	row.AllowMassDecommission = arg.AllowMassDecommission
	row.WebhookProvider = arg.WebhookProvider
	row.WebhookSecretEncrypted = arg.WebhookSecretEncrypted
	f.sources[arg.ID] = row
	return row, nil
}

func (f *fakeGitOpsHandlerQuerier) CreateGitOpsWebhookReceipt(_ context.Context, arg sqlc.CreateGitOpsWebhookReceiptParams) (time.Time, error) {
	key := arg.SourceID.String() + ":" + arg.ContentDigest
	if _, exists := f.receipts[key]; exists {
		return time.Time{}, pgx.ErrNoRows
	}
	now := time.Now().UTC()
	f.receipts[key] = now
	return now, nil
}

func (f *fakeGitOpsHandlerQuerier) DeleteGitOpsSource(_ context.Context, id uuid.UUID) error {
	delete(f.sources, id)
	return nil
}
func (f *fakeGitOpsHandlerQuerier) ListGitOpsRegisteredClustersBySource(_ context.Context, sourceID uuid.UUID) ([]sqlc.GitopsRegisteredCluster, error) {
	return f.links[sourceID], nil
}
func (f *fakeGitOpsHandlerQuerier) GetClusterByID(_ context.Context, id uuid.UUID) (sqlc.Cluster, error) {
	c, ok := f.clusters[id]
	if !ok {
		return sqlc.Cluster{}, pgx.ErrNoRows
	}
	return c, nil
}

// CreateAuditLogV1 makes the fake satisfy the audit writer interface so
// recordAudit (called from the handler) isn't a silent no-op.
func (f *fakeGitOpsHandlerQuerier) CreateAuditLogV1(_ context.Context, _ sqlc.CreateAuditLogV1Params) error {
	f.audits++
	return nil
}

func (f *fakeGitOpsHandlerQuerier) UpsertTaskOutbox(_ context.Context, arg sqlc.UpsertTaskOutboxParams) (sqlc.TaskOutbox, error) {
	f.tasks = append(f.tasks, arg)
	return sqlc.TaskOutbox{ID: uuid.New(), TaskType: arg.TaskType, Payload: arg.Payload}, nil
}

func (f *fakeGitOpsHandlerQuerier) UpsertAuditOutbox(_ context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	f.audits++
	return sqlc.AuditOutbox{ID: arg.ID}, nil
}

type fakeRunner struct {
	syncCalls    int
	previewCalls int
}

func (f *fakeRunner) SyncSource(_ context.Context, _ uuid.UUID) error {
	f.syncCalls++
	return nil
}
func (f *fakeRunner) PreviewSource(_ context.Context, _ uuid.UUID) (tasks.PreviewResult, error) {
	f.previewCalls++
	return tasks.PreviewResult{}, nil
}

// gitopsAuthedRequest returns an *http.Request with a fake authenticated user
// in context. Used because the handler.gate() helper reads from
// reqctx.AuthenticatedUser.
func gitopsAuthedRequest(method, target string, body []byte, callerID uuid.UUID) *http.Request {
	var buf *bytes.Buffer
	if body != nil {
		buf = bytes.NewBuffer(body)
	} else {
		buf = bytes.NewBuffer(nil)
	}
	req := httptest.NewRequest(method, target, buf)
	ctx := reqctx.WithUser(req.Context(), &reqctx.User{
		ID:         callerID.String(),
		AuthMethod: "jwt",
	})
	return req.WithContext(ctx)
}

func TestGitOpsHandler_RequiresSuperuser(t *testing.T) {
	callerID := uuid.New()
	q := newFakeHandlerQuerier()
	q.user = sqlc.User{ID: callerID, IsSuperuser: false}

	h := wireGitOpsMutationFixture(NewGitOpsHandler(q, &fakeRunner{}, nil), q)
	w := httptest.NewRecorder()
	req := gitopsAuthedRequest(http.MethodGet, "/api/v1/admin/gitops-sources/", nil, callerID)

	h.List(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("non-superuser must get 403; got %d body=%s", w.Code, w.Body.String())
	}

	// Promote and retry.
	q.user.IsSuperuser = true
	w2 := httptest.NewRecorder()
	req2 := gitopsAuthedRequest(http.MethodGet, "/api/v1/admin/gitops-sources/", nil, callerID)
	h.List(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("superuser must get 200; got %d body=%s", w2.Code, w2.Body.String())
	}
}

func TestGitOpsHandler_ListPaginationContract(t *testing.T) {
	callerID := uuid.New()
	q := newFakeHandlerQuerier()
	q.user = sqlc.User{ID: callerID, IsSuperuser: true}
	for i := 0; i < 3; i++ {
		id := uuid.New()
		q.sources[id] = sqlc.GitopsRegistrationSource{
			ID: id, Name: fmt.Sprintf("source-%d", i), RepoUrl: "https://example.test/repo.git",
			Branch: "main", AuthMode: "none", SyncMode: "manual", OnDelete: "log",
			CreatedAt: time.Now(), UpdatedAt: time.Now(),
		}
	}
	h := wireGitOpsMutationFixture(NewGitOpsHandler(q, &fakeRunner{}, nil), q)
	w := httptest.NewRecorder()
	req := gitopsAuthedRequest(http.MethodGet, "/api/v1/admin/gitops-sources/?limit=1&offset=1", nil, callerID)
	h.List(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", w.Code, w.Body.String())
	}
	var page struct {
		Data       []gitopsSourceResponse `json:"data"`
		Pagination paging.Metadata        `json:"pagination"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode page: %v", err)
	}
	if len(page.Data) != 1 || page.Pagination.Limit != 1 || page.Pagination.Offset != 1 || page.Pagination.Total == nil || *page.Pagination.Total != 3 {
		t.Fatalf("unexpected page: %+v", page)
	}
}

func TestGitOpsHandlerRejectsEscapingPathPrefix(t *testing.T) {
	callerID := uuid.New()
	q := newFakeHandlerQuerier()
	q.user = sqlc.User{ID: callerID, IsSuperuser: true}
	h := wireGitOpsMutationFixture(NewGitOpsHandler(q, &fakeRunner{}, nil), q)

	body := []byte(`{"name":"escape","repo_url":"https://example.test/repo.git","path_prefix":"../../etc"}`)
	w := httptest.NewRecorder()
	req := gitopsAuthedRequest(http.MethodPost, "/api/v1/admin/gitops-sources/", body, callerID)
	h.Create(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	if len(q.sources) != 0 {
		t.Fatalf("created %d sources for invalid path_prefix", len(q.sources))
	}
}

func TestHandler_CreateGetPreviewRoundtrip(t *testing.T) {
	callerID := uuid.New()
	q := newFakeHandlerQuerier()
	q.user = sqlc.User{ID: callerID, IsSuperuser: true}
	runner := &fakeRunner{}
	h := wireGitOpsMutationFixture(NewGitOpsHandler(q, runner, nil), q)
	h.SetEncryptor(testEncryptor(t))

	body := []byte(`{
		"name": "platform-gitops",
		"repo_url": "https://github.com/example/clusters",
		"branch": "main",
		"path_prefix": "clusters",
		"auth_mode": "https_token",
		"auth": "ghp_secret",
		"sync_mode": "interval",
		"sync_interval_seconds": 60,
		"on_delete": "log",
		"webhook_provider": "github",
		"webhook_secret": "github-webhook-secret-32-characters-minimum"
	}`)
	w := httptest.NewRecorder()
	req := gitopsAuthedRequest(http.MethodPost, "/api/v1/admin/gitops-sources/", body, callerID)
	h.Create(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", w.Code, w.Body.String())
	}
	var resp map[string]json.RawMessage
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	// RespondJSON wraps in {"data": ...}
	dataRaw, ok := resp["data"]
	if !ok {
		t.Fatalf("missing data wrapper")
	}
	var created gitopsSourceResponse
	if err := json.Unmarshal(dataRaw, &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.Auth != GitOpsAuthSentinel {
		t.Fatalf("auth must be sentinel, not raw; got %q", created.Auth)
	}
	if !created.AuthConfigured {
		t.Fatalf("auth_configured must be true after non-empty auth")
	}
	if !created.WebhookConfigured || created.WebhookProvider != "github" {
		t.Fatalf("webhook response configured/provider=%v/%q", created.WebhookConfigured, created.WebhookProvider)
	}

	// Preview should hit the runner with the row's UUID.
	id, err := uuid.Parse(created.ID)
	if err != nil {
		t.Fatalf("parse id: %v", err)
	}
	if stored := q.sources[id].WebhookSecretEncrypted; stored == "" || strings.Contains(stored, "github-webhook-secret") {
		t.Fatalf("webhook secret was not encrypted at rest: %q", stored)
	}
	if strings.Contains(w.Body.String(), "github-webhook-secret") {
		t.Fatal("webhook plaintext leaked in create response")
	}
	r := chi.NewRouter()
	r.Get("/api/v1/admin/gitops-sources/{id}/preview/", h.Preview)
	w2 := httptest.NewRecorder()
	req2 := gitopsAuthedRequest(http.MethodGet, "/api/v1/admin/gitops-sources/"+id.String()+"/preview/", nil, callerID)
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("preview status = %d, want 200; body=%s", w2.Code, w2.Body.String())
	}
	if runner.previewCalls != 1 {
		t.Fatalf("expected 1 preview call, got %d", runner.previewCalls)
	}
}

func TestGitOpsSyncAndWebhookQueueDurableIntent(t *testing.T) {
	callerID := uuid.New()
	q := newFakeHandlerQuerier()
	q.user = sqlc.User{ID: callerID, IsSuperuser: true}
	sourceID := uuid.New()
	encryptor := testEncryptor(t)
	webhookSecret := "github-webhook-secret-32-characters-minimum"
	sealedSecret, err := encryptor.Encrypt(webhookSecret)
	if err != nil {
		t.Fatal(err)
	}
	q.sources[sourceID] = sqlc.GitopsRegistrationSource{
		ID: sourceID, Name: "platform", RepoUrl: "https://github.com/example/platform", Enabled: true,
		WebhookProvider: "github", WebhookSecretEncrypted: sealedSecret,
	}
	runner := &fakeRunner{}
	h := NewGitOpsHandler(q, runner, nil)
	h.SetTaskOutbox(q)
	h.SetEncryptor(encryptor)

	router := chi.NewRouter()
	router.Post("/api/v1/admin/gitops-sources/{id}/sync/", h.Sync)
	router.Post("/api/v1/gitops/sources/{id}/webhook/", h.Webhook)

	manual := httptest.NewRecorder()
	manualRequest := gitopsAuthedRequest(http.MethodPost, "/api/v1/admin/gitops-sources/"+sourceID.String()+"/sync/", nil, callerID)
	manualRequest.Header.Set("Idempotency-Key", "gitops-manual-sync")
	router.ServeHTTP(manual, manualRequest)
	if manual.Code != http.StatusServiceUnavailable {
		t.Fatalf("manual sync status=%d body=%s", manual.Code, manual.Body.String())
	}
	h.SetRunTx(func(_ context.Context, fn func(GitOpsMutationTx) error) error { return fn(q) })
	payload := []byte(`{"ref":"refs/heads/main"}`)
	mac := hmac.New(sha256.New, []byte(webhookSecret))
	_, _ = mac.Write(payload)
	webhookRequest := httptest.NewRequest(http.MethodPost, "/api/v1/gitops/sources/"+sourceID.String()+"/webhook/", bytes.NewReader(payload))
	webhookRequest.Header.Set("X-GitHub-Event", "push")
	webhookRequest.Header.Set("X-GitHub-Delivery", "delivery-1")
	webhookRequest.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	webhook := httptest.NewRecorder()
	router.ServeHTTP(webhook, webhookRequest)
	if webhook.Code != http.StatusAccepted {
		t.Fatalf("webhook sync status=%d body=%s", webhook.Code, webhook.Body.String())
	}
	if runner.syncCalls != 0 {
		t.Fatalf("request path performed %d inline sync(s), want zero", runner.syncCalls)
	}
	if len(q.tasks) != 1 {
		t.Fatalf("durable task intents=%d, want webhook task only", len(q.tasks))
	}
	for _, task := range q.tasks {
		if task.TaskType != tasks.GitOpsSyncType || !strings.Contains(string(task.Payload), sourceID.String()) || strings.Contains(string(task.Payload), "git.example") {
			t.Fatalf("unsafe or invalid task intent: type=%q payload=%s", task.TaskType, task.Payload)
		}
	}
	replay := httptest.NewRecorder()
	replayRequest := httptest.NewRequest(http.MethodPost, "/api/v1/gitops/sources/"+sourceID.String()+"/webhook/", bytes.NewReader(payload))
	replayRequest.Header = webhookRequest.Header.Clone()
	router.ServeHTTP(replay, replayRequest)
	if replay.Code != http.StatusConflict || len(q.tasks) != 1 {
		t.Fatalf("replay status/tasks=%d/%d, want 409/1: %s", replay.Code, len(q.tasks), replay.Body.String())
	}
	if q.audits != 2 {
		t.Fatalf("accepted plus replay audit rows=%d, want 2", q.audits)
	}
	// The delivery header is not covered by GitHub's signature. Neither a
	// changed ID nor age may allow the captured signed payload to run again.
	for key := range q.receipts {
		q.receipts[key] = time.Now().Add(-365 * 24 * time.Hour)
	}
	changedID := httptest.NewRequest(http.MethodPost, webhookRequest.URL.String(), bytes.NewReader(payload))
	changedID.Header = webhookRequest.Header.Clone()
	changedID.Header.Set("X-GitHub-Delivery", "attacker-replaced-delivery-id")
	changedReply := httptest.NewRecorder()
	router.ServeHTTP(changedReply, changedID)
	if changedReply.Code != http.StatusConflict || len(q.tasks) != 1 {
		t.Fatalf("changed-ID old replay status/tasks=%d/%d, want 409/1: %s", changedReply.Code, len(q.tasks), changedReply.Body.String())
	}
}

func TestGitOpsWebhookRejectsMissingOrInvalidNativeSignature(t *testing.T) {
	q := newFakeHandlerQuerier()
	sourceID := uuid.New()
	encryptor := testEncryptor(t)
	sealed, err := encryptor.Encrypt("github-webhook-secret-32-characters-minimum")
	if err != nil {
		t.Fatal(err)
	}
	q.sources[sourceID] = sqlc.GitopsRegistrationSource{
		ID: sourceID, Name: "platform", RepoUrl: "https://github.com/example/platform",
		WebhookProvider: "github", WebhookSecretEncrypted: sealed, Enabled: true,
	}
	h := wireGitOpsMutationFixture(NewGitOpsHandler(q, &fakeRunner{}, nil), q)
	h.SetEncryptor(encryptor)
	h.SetRunTx(func(_ context.Context, fn func(GitOpsMutationTx) error) error { return fn(q) })

	router := chi.NewRouter()
	router.Post("/api/v1/gitops/sources/{id}/webhook/", h.Webhook)
	for _, signature := range []string{"", "sha256=not-hex", "sha256=" + strings.Repeat("00", sha256.Size)} {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/gitops/sources/"+sourceID.String()+"/webhook/", strings.NewReader(`{"ref":"refs/heads/main"}`))
		req.Header.Set("X-GitHub-Event", "push")
		req.Header.Set("X-GitHub-Delivery", uuid.NewString())
		if signature != "" {
			req.Header.Set("X-Hub-Signature-256", signature)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("signature %q status=%d, want 401: %s", signature, rec.Code, rec.Body.String())
		}
	}
	if len(q.tasks) != 0 || len(q.receipts) != 0 {
		t.Fatalf("rejected signatures persisted tasks/receipts=%d/%d", len(q.tasks), len(q.receipts))
	}
	if q.audits != 3 {
		t.Fatalf("rejected signature audit rows=%d, want 3", q.audits)
	}
}

func TestGitOpsCredentialWritesFailClosedWithoutEncryptor(t *testing.T) {
	callerID := uuid.New()
	q := newFakeHandlerQuerier()
	q.user = sqlc.User{ID: callerID, IsSuperuser: true}
	h := wireGitOpsMutationFixture(NewGitOpsHandler(q, &fakeRunner{}, nil), q)
	body := []byte(`{"name":"private","repo_url":"https://git.example/private","auth_mode":"https_token","auth":"plaintext-token","sync_mode":"manual","on_delete":"log"}`)
	w := httptest.NewRecorder()
	h.Create(w, gitopsAuthedRequest(http.MethodPost, "/api/v1/admin/gitops-sources/", body, callerID))
	if w.Code != http.StatusServiceUnavailable || len(q.sources) != 0 {
		t.Fatalf("credentialed create status/sources=%d/%d body=%s", w.Code, len(q.sources), w.Body.String())
	}
}

func TestGitOpsRepositoryURLRejectsEmbeddedCredentials(t *testing.T) {
	for _, raw := range []string{
		"https://user:secret@git.example/repo",
		"https://git.example/repo?token=secret",
		"file:///etc/passwd",
		"ssh://deploy:secret@git.example/repo",
	} {
		if _, err := validateGitOpsRepositoryURL(raw); err == nil {
			t.Fatalf("validateGitOpsRepositoryURL(%q) = nil, want error", raw)
		}
	}
}

func TestHandler_UpdatePreservesAuthOnSentinel(t *testing.T) {
	callerID := uuid.New()
	q := newFakeHandlerQuerier()
	q.user = sqlc.User{ID: callerID, IsSuperuser: true}
	id := uuid.New()
	q.sources[id] = sqlc.GitopsRegistrationSource{
		ID:                  id,
		Name:                "demo",
		RepoUrl:             "https://example/demo",
		Branch:              "main",
		AuthMode:            "https_token",
		AuthEncrypted:       "original-secret",
		SyncMode:            "interval",
		SyncIntervalSeconds: 60,
		OnDelete:            "log",
		Enabled:             true,
	}
	h := wireGitOpsMutationFixture(NewGitOpsHandler(q, &fakeRunner{}, nil), q)
	body := []byte(`{
		"name": "demo",
		"repo_url": "https://example/demo",
		"auth_mode": "https_token",
		"auth": "<encrypted>",
		"sync_mode": "interval",
		"sync_interval_seconds": 60,
		"on_delete": "tombstone"
	}`)
	r := chi.NewRouter()
	r.Put("/api/v1/admin/gitops-sources/{id}/", h.Update)
	w := httptest.NewRecorder()
	req := gitopsAuthedRequest(http.MethodPut, "/api/v1/admin/gitops-sources/"+id.String()+"/", body, callerID)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update status = %d, body=%s", w.Code, w.Body.String())
	}
	if q.sources[id].AuthEncrypted != "original-secret" {
		t.Fatalf("auth must be preserved when sentinel echoed; got %q", q.sources[id].AuthEncrypted)
	}
	if q.sources[id].OnDelete != "tombstone" {
		t.Fatalf("on_delete update lost; got %q", q.sources[id].OnDelete)
	}
}
