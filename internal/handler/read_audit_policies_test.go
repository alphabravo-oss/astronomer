package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

// fakeReadAuditPolicyQuerier is the test double for the handler's DB
// surface.
type fakeReadAuditPolicyQuerier struct {
	mu        sync.Mutex
	rows      map[uuid.UUID]sqlc.ReadAuditPolicy
	user      sqlc.User
	getUserOK bool
}

func newFakeQuerier(superuser bool) *fakeReadAuditPolicyQuerier {
	return &fakeReadAuditPolicyQuerier{
		rows:      map[uuid.UUID]sqlc.ReadAuditPolicy{},
		user:      sqlc.User{ID: uuid.New(), IsSuperuser: superuser},
		getUserOK: true,
	}
}

func (f *fakeReadAuditPolicyQuerier) ListReadAuditPolicies(_ context.Context) ([]sqlc.ReadAuditPolicy, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]sqlc.ReadAuditPolicy, 0, len(f.rows))
	for _, p := range f.rows {
		out = append(out, p)
	}
	return out, nil
}

func (f *fakeReadAuditPolicyQuerier) GetReadAuditPolicy(_ context.Context, id uuid.UUID) (sqlc.ReadAuditPolicy, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.rows[id]
	if !ok {
		return sqlc.ReadAuditPolicy{}, pgx.ErrNoRows
	}
	return p, nil
}

func (f *fakeReadAuditPolicyQuerier) CreateReadAuditPolicy(_ context.Context, arg sqlc.CreateReadAuditPolicyParams) (sqlc.ReadAuditPolicy, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := sqlc.ReadAuditPolicy{
		ID:          uuid.New(),
		Name:        arg.Name,
		Description: arg.Description,
		PathPattern: arg.PathPattern,
		Verbs:       arg.Verbs,
		SampleRate:  arg.SampleRate,
		Enabled:     arg.Enabled,
		CreatedBy:   arg.CreatedBy,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	f.rows[p.ID] = p
	return p, nil
}

func (f *fakeReadAuditPolicyQuerier) UpdateReadAuditPolicy(_ context.Context, arg sqlc.UpdateReadAuditPolicyParams) (sqlc.ReadAuditPolicy, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.rows[arg.ID]
	if !ok {
		return sqlc.ReadAuditPolicy{}, pgx.ErrNoRows
	}
	p.Description = arg.Description
	p.PathPattern = arg.PathPattern
	p.Verbs = arg.Verbs
	p.SampleRate = arg.SampleRate
	p.Enabled = arg.Enabled
	p.UpdatedAt = time.Now()
	f.rows[arg.ID] = p
	return p, nil
}

func (f *fakeReadAuditPolicyQuerier) DeleteReadAuditPolicy(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.rows[id]; !ok {
		return pgx.ErrNoRows
	}
	delete(f.rows, id)
	return nil
}

func (f *fakeReadAuditPolicyQuerier) GetUserByID(_ context.Context, id uuid.UUID) (sqlc.User, error) {
	if !f.getUserOK {
		return sqlc.User{}, errors.New("user lookup disabled")
	}
	if id == f.user.ID {
		return f.user, nil
	}
	return sqlc.User{}, errors.New("not found")
}

// CreateAuditLogV1 — satisfies the audit Querier interface used by
// recordAudit; we accept all rows silently.
func (f *fakeReadAuditPolicyQuerier) CreateAuditLogV1(_ context.Context, _ sqlc.CreateAuditLogV1Params) error {
	return nil
}

// withAuth injects an AuthenticatedUser into the request context.
func withAuth(r *http.Request, userID uuid.UUID) *http.Request {
	ctx := middleware.SetAuthenticatedUserForTest(r.Context(), &middleware.AuthenticatedUser{
		ID:         userID.String(),
		Email:      "admin@example.com",
		AuthMethod: "jwt",
	})
	return r.WithContext(ctx)
}

func newPolicyRouter(h *ReadAuditPolicyHandler) *chi.Mux {
	r := chi.NewRouter()
	r.Get("/api/v1/admin/read-audit-policies/", h.List)
	r.Post("/api/v1/admin/read-audit-policies/", h.Create)
	r.Get("/api/v1/admin/read-audit-policies/{id}/", h.Get)
	r.Put("/api/v1/admin/read-audit-policies/{id}/", h.Update)
	r.Delete("/api/v1/admin/read-audit-policies/{id}/", h.Delete)
	return r
}

func TestReadAuditHandler_CRUD(t *testing.T) {
	q := newFakeQuerier(true)
	h := NewReadAuditPolicyHandler(q, nil)
	r := newPolicyRouter(h)

	// Create.
	createBody := `{"name":"new-policy","path_pattern":"/admin/foo","verbs":"GET","sample_rate":0.5,"enabled":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/read-audit-policies/", bytes.NewBufferString(createBody))
	req = withAuth(req, q.user.ID)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body=%s", w.Code, w.Body.String())
	}
	var createdW struct {
		Data readAuditPolicyResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &createdW); err != nil {
		t.Fatal(err)
	}
	created := createdW.Data
	if created.Name != "new-policy" || created.SampleRate != 0.5 {
		t.Fatalf("unexpected: %+v", created)
	}

	// List.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/admin/read-audit-policies/", nil)
	req = withAuth(req, q.user.ID)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d", w.Code)
	}
	var listW struct {
		Data struct {
			Items []readAuditPolicyResponse `json:"items"`
			Total int                       `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listW); err != nil {
		t.Fatal(err)
	}
	if listW.Data.Total != 1 || len(listW.Data.Items) != 1 {
		t.Fatalf("list got %d items, want 1", listW.Data.Total)
	}

	// Get.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/admin/read-audit-policies/"+created.ID+"/", nil)
	req = withAuth(req, q.user.ID)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get status = %d", w.Code)
	}

	// Update (toggle enabled).
	upBody := `{"enabled":false,"sample_rate":0.25}`
	req = httptest.NewRequest(http.MethodPut, "/api/v1/admin/read-audit-policies/"+created.ID+"/", bytes.NewBufferString(upBody))
	req = withAuth(req, q.user.ID)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update status = %d, body=%s", w.Code, w.Body.String())
	}
	var updatedW struct {
		Data readAuditPolicyResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &updatedW); err != nil {
		t.Fatal(err)
	}
	updated := updatedW.Data
	if updated.Enabled || updated.SampleRate != 0.25 {
		t.Fatalf("update did not apply: %+v", updated)
	}

	// Delete.
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/admin/read-audit-policies/"+created.ID+"/", nil)
	req = withAuth(req, q.user.ID)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d", w.Code)
	}
}

func TestReadAuditHandler_RequiresSuperuser(t *testing.T) {
	q := newFakeQuerier(false) // not superuser
	h := NewReadAuditPolicyHandler(q, nil)
	r := newPolicyRouter(h)

	// List requires superuser.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/read-audit-policies/", nil)
	req = withAuth(req, q.user.ID)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("list as non-superuser: status = %d, want 403", w.Code)
	}

	// Create requires superuser.
	req = httptest.NewRequest(http.MethodPost, "/api/v1/admin/read-audit-policies/", bytes.NewBufferString(`{"name":"x","path_pattern":"/x"}`))
	req = withAuth(req, q.user.ID)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("create as non-superuser: status = %d, want 403", w.Code)
	}

	// Update requires superuser.
	id := uuid.New()
	req = httptest.NewRequest(http.MethodPut, "/api/v1/admin/read-audit-policies/"+id.String()+"/", bytes.NewBufferString(`{}`))
	req = withAuth(req, q.user.ID)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("update as non-superuser: status = %d, want 403", w.Code)
	}

	// Delete requires superuser.
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/admin/read-audit-policies/"+id.String()+"/", nil)
	req = withAuth(req, q.user.ID)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("delete as non-superuser: status = %d, want 403", w.Code)
	}
}

// fakeAuditReader is a narrow implementation of auditReaderV1 that lets
// us drive the action_class filter test path without touching a real
// DB. Only the action_class-related methods need real behavior; the
// rest panic if called by mistake.
type fakeAuditReader struct {
	byClass map[string][]sqlc.AuditLog
}

func (f *fakeAuditReader) GetAuditLogV1ByID(_ context.Context, _ uuid.UUID) (sqlc.AuditLog, error) {
	return sqlc.AuditLog{}, errors.New("not implemented")
}
func (f *fakeAuditReader) ListAuditLogV1(_ context.Context, _ sqlc.ListAuditLogsParams) ([]sqlc.AuditLog, error) {
	return nil, nil
}
func (f *fakeAuditReader) ListAuditLogV1ByUser(_ context.Context, _ sqlc.ListAuditLogsByUserParams) ([]sqlc.AuditLog, error) {
	return nil, nil
}
func (f *fakeAuditReader) ListAuditLogV1ByResourceType(_ context.Context, _ sqlc.ListAuditLogsByResourceTypeParams) ([]sqlc.AuditLog, error) {
	return nil, nil
}
func (f *fakeAuditReader) ListAuditLogV1ByAction(_ context.Context, _ sqlc.ListAuditLogsByActionParams) ([]sqlc.AuditLog, error) {
	return nil, nil
}
func (f *fakeAuditReader) ListAuditLogV1ByActionClass(_ context.Context, arg sqlc.ListAuditLogsByActionClassParams) ([]sqlc.AuditLog, error) {
	return f.byClass[arg.ActionClass], nil
}
func (f *fakeAuditReader) ListAuditLogV1Since(_ context.Context, _ sqlc.ListAuditLogsSinceParams) ([]sqlc.AuditLog, error) {
	return nil, nil
}
func (f *fakeAuditReader) CountAuditLogV1(_ context.Context) (int64, error) { return 0, nil }
func (f *fakeAuditReader) CountAuditLogV1ByUser(_ context.Context, _ pgtype.UUID) (int64, error) {
	return 0, nil
}
func (f *fakeAuditReader) CountAuditLogV1ByActionClass(_ context.Context, ac string) (int64, error) {
	return int64(len(f.byClass[ac])), nil
}

func TestAuditLog_FilterByActionClass(t *testing.T) {
	reader := &fakeAuditReader{
		byClass: map[string][]sqlc.AuditLog{
			"read": {
				{ID: uuid.New(), Action: "read.projects.cloud_credentials", ActionClass: "read"},
				{ID: uuid.New(), Action: "read.admin.sso", ActionClass: "read"},
			},
		},
	}
	h := NewAuditHandler(reader)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit/?action_class=read", nil)
	w := httptest.NewRecorder()
	h.List(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Data  []AuditLogResponse `json:"data"`
		Count int64              `json:"count"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Count != 2 || len(resp.Data) != 2 {
		t.Fatalf("count = %d, items = %d", resp.Count, len(resp.Data))
	}
	for _, it := range resp.Data {
		if it.ActionClass != "read" {
			t.Errorf("ActionClass = %q want read", it.ActionClass)
		}
	}
}
