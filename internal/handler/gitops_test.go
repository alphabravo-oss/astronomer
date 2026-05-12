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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
)

type fakeGitOpsHandlerQuerier struct {
	user     sqlc.User
	sources  map[uuid.UUID]sqlc.GitopsRegistrationSource
	clusters map[uuid.UUID]sqlc.Cluster
	links    map[uuid.UUID][]sqlc.GitopsRegisteredCluster
	audits   int
}

func newFakeHandlerQuerier() *fakeGitOpsHandlerQuerier {
	return &fakeGitOpsHandlerQuerier{
		sources:  map[uuid.UUID]sqlc.GitopsRegistrationSource{},
		clusters: map[uuid.UUID]sqlc.Cluster{},
		links:    map[uuid.UUID][]sqlc.GitopsRegisteredCluster{},
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
		ID:                  uuid.New(),
		Name:                arg.Name,
		RepoUrl:             arg.RepoUrl,
		Branch:              arg.Branch,
		PathPrefix:          arg.PathPrefix,
		AuthMode:            arg.AuthMode,
		AuthEncrypted:       arg.AuthEncrypted,
		SyncMode:            arg.SyncMode,
		SyncIntervalSeconds: arg.SyncIntervalSeconds,
		OnDelete:            arg.OnDelete,
		Enabled:             arg.Enabled,
		CreatedBy:           arg.CreatedBy,
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
	f.sources[arg.ID] = row
	return row, nil
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
// middleware.GetAuthenticatedUser.
func gitopsAuthedRequest(method, target string, body []byte, callerID uuid.UUID) *http.Request {
	var buf *bytes.Buffer
	if body != nil {
		buf = bytes.NewBuffer(body)
	} else {
		buf = bytes.NewBuffer(nil)
	}
	req := httptest.NewRequest(method, target, buf)
	ctx := middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{
		ID:         callerID.String(),
		AuthMethod: "jwt",
	})
	return req.WithContext(ctx)
}

func TestHandler_RequiresSuperuser(t *testing.T) {
	callerID := uuid.New()
	q := newFakeHandlerQuerier()
	q.user = sqlc.User{ID: callerID, IsSuperuser: false}

	h := NewGitOpsHandler(q, &fakeRunner{}, nil)
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

func TestHandler_CreateGetPreviewRoundtrip(t *testing.T) {
	callerID := uuid.New()
	q := newFakeHandlerQuerier()
	q.user = sqlc.User{ID: callerID, IsSuperuser: true}
	runner := &fakeRunner{}
	h := NewGitOpsHandler(q, runner, nil)

	body := []byte(`{
		"name": "platform-gitops",
		"repo_url": "https://github.com/example/clusters",
		"branch": "main",
		"path_prefix": "clusters",
		"auth_mode": "https_token",
		"auth": "ghp_secret",
		"sync_mode": "interval",
		"sync_interval_seconds": 60,
		"on_delete": "log"
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

	// Preview should hit the runner with the row's UUID.
	id, err := uuid.Parse(created.ID)
	if err != nil {
		t.Fatalf("parse id: %v", err)
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
	h := NewGitOpsHandler(q, &fakeRunner{}, nil)
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
