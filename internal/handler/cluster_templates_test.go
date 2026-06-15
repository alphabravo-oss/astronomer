package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
)

// fakeClusterTemplateQuerier is the narrow ClusterTemplateQuerier surface
// the handler tests exercise. We keep it deliberately minimal: only the
// queries the test touches are wired with real behavior, the rest return
// zero values or pgx.ErrNoRows.
type fakeClusterTemplateQuerier struct {
	mu sync.Mutex

	templates    map[uuid.UUID]sqlc.ClusterTemplate
	byName       map[string]uuid.UUID
	applications map[uuid.UUID]sqlc.ClusterTemplateApplication
	clusters     map[uuid.UUID]sqlc.Cluster
	policies     map[uuid.UUID]sqlc.ClusterRegistrationPolicy
	audits       []sqlc.CreateAuditLogV1Params
}

type fakeAtomicClusterTemplateQuerier struct {
	*fakeClusterTemplateQuerier

	atomicApps []sqlc.UpsertClusterTemplateApplicationWithTaskOutboxParams
}

func newFakeClusterTemplateQuerier() *fakeClusterTemplateQuerier {
	return &fakeClusterTemplateQuerier{
		templates:    map[uuid.UUID]sqlc.ClusterTemplate{},
		byName:       map[string]uuid.UUID{},
		applications: map[uuid.UUID]sqlc.ClusterTemplateApplication{},
		clusters:     map[uuid.UUID]sqlc.Cluster{},
		policies:     map[uuid.UUID]sqlc.ClusterRegistrationPolicy{},
	}
}

func (f *fakeClusterTemplateQuerier) ListClusterTemplates(_ context.Context, _ sqlc.ListClusterTemplatesParams) ([]sqlc.ClusterTemplate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]sqlc.ClusterTemplate, 0, len(f.templates))
	for _, t := range f.templates {
		out = append(out, t)
	}
	return out, nil
}

func (f *fakeClusterTemplateQuerier) CountClusterTemplates(_ context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return int64(len(f.templates)), nil
}

func (f *fakeClusterTemplateQuerier) GetClusterTemplateByID(_ context.Context, id uuid.UUID) (sqlc.ClusterTemplate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.templates[id]
	if !ok {
		return sqlc.ClusterTemplate{}, pgx.ErrNoRows
	}
	return t, nil
}

func (f *fakeClusterTemplateQuerier) GetClusterTemplateByName(_ context.Context, name string) (sqlc.ClusterTemplate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := f.byName[name]
	if !ok {
		return sqlc.ClusterTemplate{}, pgx.ErrNoRows
	}
	return f.templates[id], nil
}

func (f *fakeClusterTemplateQuerier) CreateClusterTemplate(_ context.Context, arg sqlc.CreateClusterTemplateParams) (sqlc.ClusterTemplate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, exists := f.byName[arg.Name]; exists {
		return sqlc.ClusterTemplate{}, &fakePGError{code: "23505"}
	}
	id := uuid.New()
	t := sqlc.ClusterTemplate{
		ID:          id,
		Name:        arg.Name,
		Description: arg.Description,
		Spec:        arg.Spec,
		CreatedBy:   arg.CreatedBy,
	}
	f.templates[id] = t
	f.byName[arg.Name] = id
	return t, nil
}

func (f *fakeClusterTemplateQuerier) UpdateClusterTemplate(_ context.Context, arg sqlc.UpdateClusterTemplateParams) (sqlc.ClusterTemplate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	existing, ok := f.templates[arg.ID]
	if !ok {
		return sqlc.ClusterTemplate{}, pgx.ErrNoRows
	}
	// Re-key the name index when the name changed.
	if existing.Name != arg.Name {
		if _, conflict := f.byName[arg.Name]; conflict {
			return sqlc.ClusterTemplate{}, &fakePGError{code: "23505"}
		}
		delete(f.byName, existing.Name)
		f.byName[arg.Name] = arg.ID
	}
	existing.Name = arg.Name
	existing.Description = arg.Description
	existing.Spec = arg.Spec
	f.templates[arg.ID] = existing
	return existing, nil
}

func (f *fakeClusterTemplateQuerier) DeleteClusterTemplate(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	existing, ok := f.templates[id]
	if !ok {
		return pgx.ErrNoRows
	}
	delete(f.templates, id)
	delete(f.byName, existing.Name)
	return nil
}

func (f *fakeClusterTemplateQuerier) CountClusterTemplateApplicationsByTemplate(_ context.Context, templateID uuid.UUID) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int64
	for _, a := range f.applications {
		if a.TemplateID == templateID {
			n++
		}
	}
	return n, nil
}

func (f *fakeClusterTemplateQuerier) GetClusterTemplateApplication(_ context.Context, clusterID uuid.UUID) (sqlc.ClusterTemplateApplication, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.applications[clusterID]
	if !ok {
		return sqlc.ClusterTemplateApplication{}, pgx.ErrNoRows
	}
	return a, nil
}

func (f *fakeClusterTemplateQuerier) UpsertClusterTemplateApplication(_ context.Context, arg sqlc.UpsertClusterTemplateApplicationParams) (sqlc.ClusterTemplateApplication, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a := sqlc.ClusterTemplateApplication{
		ClusterID:    arg.ClusterID,
		TemplateID:   arg.TemplateID,
		Status:       "pending",
		SpecSnapshot: arg.SpecSnapshot,
	}
	f.applications[arg.ClusterID] = a
	return a, nil
}

func (f *fakeAtomicClusterTemplateQuerier) UpsertClusterTemplateApplicationWithTaskOutbox(ctx context.Context, arg sqlc.UpsertClusterTemplateApplicationWithTaskOutboxParams) (sqlc.ClusterTemplateApplication, error) {
	f.atomicApps = append(f.atomicApps, arg)
	return f.UpsertClusterTemplateApplication(ctx, sqlc.UpsertClusterTemplateApplicationParams{
		ClusterID:    arg.ClusterID,
		TemplateID:   arg.TemplateID,
		SpecSnapshot: arg.SpecSnapshot,
	})
}

func (f *fakeClusterTemplateQuerier) MarkClusterTemplateApplicationStatus(_ context.Context, arg sqlc.MarkClusterTemplateApplicationStatusParams) (sqlc.ClusterTemplateApplication, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.applications[arg.ClusterID]
	if !ok {
		return sqlc.ClusterTemplateApplication{}, pgx.ErrNoRows
	}
	a.Status = arg.Status
	a.LastError = arg.LastError
	a.AppliedAt = arg.AppliedAt
	f.applications[arg.ClusterID] = a
	return a, nil
}

func (f *fakeClusterTemplateQuerier) DeleteClusterTemplateApplication(_ context.Context, clusterID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.applications, clusterID)
	return nil
}

func (f *fakeClusterTemplateQuerier) GetClusterByID(_ context.Context, id uuid.UUID) (sqlc.Cluster, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.clusters[id]
	if !ok {
		return sqlc.Cluster{}, pgx.ErrNoRows
	}
	return c, nil
}

func (f *fakeClusterTemplateQuerier) DeleteClusterRegistrationPolicy(_ context.Context, clusterID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.policies, clusterID)
	return nil
}

func (f *fakeClusterTemplateQuerier) CreateAuditLogV1(_ context.Context, arg sqlc.CreateAuditLogV1Params) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.audits = append(f.audits, arg)
	return nil
}

func (f *fakeClusterTemplateQuerier) auditRowAt(t *testing.T, idx int) sqlc.CreateAuditLogV1Params {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.audits) <= idx {
		t.Fatalf("audit rows=%d, want index %d", len(f.audits), idx)
	}
	return f.audits[idx]
}

// fakePGError mirrors the surface CreateClusterTemplate returns on a
// duplicate name. The handler reads pgconn.PgError.Code via errors.As,
// so an unrelated error type satisfies "the fake returned an error" but
// does NOT trip the 409-classification branch. That's fine for the unit
// tests we care about (CRUD happy path, in-use blocking). The live pg
// path is covered by the integration suite.
type fakePGError struct {
	code string
}

func (e *fakePGError) Error() string { return "pg error " + e.code }

// helper that wraps every test with a chi route ctx + URL params.
func withChiParams(req *http.Request, params map[string]string) *http.Request {
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

// ────────────────────────────────────────────────────────────────────────
// Tests
// ────────────────────────────────────────────────────────────────────────

// TestClusterTemplate_CRUD exercises the basic create-list-get-update-
// delete happy path on the template endpoints.
func TestClusterTemplate_CRUD(t *testing.T) {
	q := newFakeClusterTemplateQuerier()
	h := NewClusterTemplateHandler(q)

	// Create a template with a valid spec.
	body := mustJSON(t, map[string]any{
		"name":        "production-web",
		"description": "Production web app preset",
		"spec": map[string]any{
			"environment": "production",
			"labels":      map[string]string{"tier": "prod"},
			"tools": []map[string]any{
				{"slug": "argocd", "preset": "ha"},
			},
			"default_project": map[string]any{
				"name":                 "platform",
				"pod_security_profile": "baseline",
			},
			"registration_policy": map[string]any{
				"token_rotation_days": 90,
			},
		},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/cluster-templates/", bytes.NewReader(body))
	h.Create(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var createResp struct {
		Data ClusterTemplateResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if createResp.Data.Name != "production-web" {
		t.Errorf("create returned name=%q", createResp.Data.Name)
	}
	id := createResp.Data.ID

	// List: should return one row.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/cluster-templates/", nil)
	h.List(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status=%d", rec.Code)
	}
	var listResp struct {
		Data  []ClusterTemplateResponse `json:"data"`
		Count int64                     `json:"count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if listResp.Count != 1 || len(listResp.Data) != 1 {
		t.Errorf("list returned count=%d items=%d", listResp.Count, len(listResp.Data))
	}

	// Get.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/cluster-templates/"+id+"/", nil)
	req = withChiParams(req, map[string]string{"id": id})
	h.Get(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// Update — change description.
	body = mustJSON(t, map[string]any{
		"name":        "production-web",
		"description": "v2",
		"spec":        map[string]any{},
	})
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/v1/cluster-templates/"+id+"/", bytes.NewReader(body))
	req = withChiParams(req, map[string]string{"id": id})
	h.Update(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// Delete — should succeed since no cluster references it.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/cluster-templates/"+id+"/", nil)
	req = withChiParams(req, map[string]string{"id": id})
	h.Delete(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// TestClusterTemplate_Create_RejectsInvalidSpec runs the spec validator
// against several malformed bodies.
func TestClusterTemplate_Create_RejectsInvalidSpec(t *testing.T) {
	q := newFakeClusterTemplateQuerier()
	h := NewClusterTemplateHandler(q)

	cases := []struct {
		name string
		spec map[string]any
		want string
	}{
		{"unknown key", map[string]any{"unknown_field": 1}, "unknown spec key"},
		{"bad env", map[string]any{"environment": "prod"}, "environment must be"},
		{"bad pss", map[string]any{"default_project": map[string]any{"name": "p", "pod_security_profile": "loose"}}, "pod_security_profile"},
		{"missing project name", map[string]any{"default_project": map[string]any{}}, "default_project.name is required"},
		{"missing tool slug", map[string]any{"tools": []map[string]any{{"preset": "ha"}}}, "tools[0].slug is required"},
		{"negative rotation", map[string]any{"registration_policy": map[string]any{"token_rotation_days": -1}}, "registration_policy.token_rotation_days"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := mustJSON(t, map[string]any{"name": tc.name, "spec": tc.spec})
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
			h.Create(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.want) {
				t.Errorf("want body to mention %q, got %s", tc.want, rec.Body.String())
			}
		})
	}
}

// TestClusterTemplate_DeleteRejectedWhenInUse verifies the count-first
// 409 path.
func TestClusterTemplate_DeleteRejectedWhenInUse(t *testing.T) {
	q := newFakeClusterTemplateQuerier()
	h := NewClusterTemplateHandler(q)

	tmplID := uuid.New()
	q.templates[tmplID] = sqlc.ClusterTemplate{ID: tmplID, Name: "x", Spec: json.RawMessage(`{}`)}
	q.byName["x"] = tmplID
	clusterID := uuid.New()
	q.applications[clusterID] = sqlc.ClusterTemplateApplication{ClusterID: clusterID, TemplateID: tmplID}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/cluster-templates/"+tmplID.String()+"/", nil)
	req = withChiParams(req, map[string]string{"id": tmplID.String()})
	h.Delete(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "template_in_use") {
		t.Errorf("expected template_in_use code, got %s", rec.Body.String())
	}
	// Template still exists.
	if _, ok := q.templates[tmplID]; !ok {
		t.Errorf("template was deleted despite 409")
	}
}

// TestClusterTemplate_ApplyAndStatus exercises the per-cluster bind +
// status endpoints, including the queue handoff.
func TestClusterTemplate_ApplyAndStatus(t *testing.T) {
	q := newFakeClusterTemplateQuerier()
	clusterID := uuid.New()
	q.clusters[clusterID] = sqlc.Cluster{ID: clusterID, Name: "demo", Environment: "development", Labels: json.RawMessage(`{}`), Annotations: json.RawMessage(`{}`)}

	tmplID := uuid.New()
	spec := json.RawMessage(`{"environment":"production","labels":{"tier":"prod"}}`)
	q.templates[tmplID] = sqlc.ClusterTemplate{ID: tmplID, Name: "production-web", Spec: spec}
	q.byName["production-web"] = tmplID

	cap := &captureEnqueuer{}
	h := NewClusterTemplateHandler(q)
	h.SetQueue(cap)

	body := mustJSON(t, map[string]string{"template_id": tmplID.String()})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/"+clusterID.String()+"/template/", bytes.NewReader(body))
	req = withChiParams(req, map[string]string{"cluster_id": clusterID.String()})
	h.Apply(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("apply: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if cap.count == 0 {
		t.Errorf("apply did not enqueue a task")
	}
	applyAudit := q.auditRowAt(t, 0)
	if applyAudit.Action != "cluster.template_applied" || applyAudit.ResourceType != "cluster" || applyAudit.ResourceID != clusterID.String() || applyAudit.ResourceName != "demo" {
		t.Fatalf("apply audit row=%+v, want cluster.template_applied on cluster %s", applyAudit, clusterID)
	}
	assertAuditDetail(t, applyAudit.Detail, "template_id", tmplID.String())

	// Status endpoint should return pending.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/clusters/"+clusterID.String()+"/template/", nil)
	req = withChiParams(req, map[string]string{"cluster_id": clusterID.String()})
	h.GetApplication(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get application: status=%d", rec.Code)
	}
	var statusResp struct {
		Data ClusterTemplateApplicationResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &statusResp); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if statusResp.Data.Status != "pending" {
		t.Errorf("status=%s, want pending", statusResp.Data.Status)
	}
	if statusResp.Data.TemplateName != "production-web" {
		t.Errorf("template_name=%s", statusResp.Data.TemplateName)
	}

	// Reapply.
	cap.count = 0
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/", nil)
	req = withChiParams(req, map[string]string{"cluster_id": clusterID.String()})
	h.Reapply(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("reapply: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if cap.count == 0 {
		t.Errorf("reapply did not enqueue a task")
	}

	// Detach.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/", nil)
	req = withChiParams(req, map[string]string{"cluster_id": clusterID.String()})
	h.Detach(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("detach: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if _, ok := q.applications[clusterID]; ok {
		t.Errorf("application row not deleted")
	}
	detachAudit := q.auditRowAt(t, 2)
	if detachAudit.Action != "cluster.template_detached" || detachAudit.ResourceType != "cluster" || detachAudit.ResourceID != clusterID.String() || detachAudit.ResourceName != "demo" {
		t.Fatalf("detach audit row=%+v, want cluster.template_detached on cluster %s", detachAudit, clusterID)
	}
}

func TestClusterTemplateApplyWritesTaskOutbox(t *testing.T) {
	q := newFakeClusterTemplateQuerier()
	clusterID := uuid.New()
	q.clusters[clusterID] = sqlc.Cluster{ID: clusterID, Name: "demo", Environment: "development", Labels: json.RawMessage(`{}`), Annotations: json.RawMessage(`{}`)}
	tmplID := uuid.New()
	q.templates[tmplID] = sqlc.ClusterTemplate{ID: tmplID, Name: "production-web", Spec: json.RawMessage(`{"tools":[]}`)}
	outbox := &fakeRegistrationTaskOutbox{}
	cap := &captureEnqueuer{}
	h := NewClusterTemplateHandler(q)
	h.SetQueue(cap)
	h.SetTaskOutbox(outbox)

	body := mustJSON(t, map[string]string{"template_id": tmplID.String()})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/"+clusterID.String()+"/template/", bytes.NewReader(body))
	req = withChiParams(req, map[string]string{"cluster_id": clusterID.String()})
	h.Apply(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("apply: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if cap.count != 0 {
		t.Fatalf("direct enqueues = %d, want 0 when outbox succeeds", cap.count)
	}
	args := outbox.all()
	if len(args) != 1 {
		t.Fatalf("outbox writes = %d, want 1", len(args))
	}
	arg := args[0]
	if arg.TaskType != tasks.ClusterTemplateApplyType {
		t.Fatalf("task type = %q, want %q", arg.TaskType, tasks.ClusterTemplateApplyType)
	}
	if !arg.DedupeKey.Valid || arg.DedupeKey.String != "cluster_template_apply:"+clusterID.String() {
		t.Fatalf("dedupe key = %+v", arg.DedupeKey)
	}
	if arg.QueueName != tasks.ClusterTemplateApplyQueueName || arg.MaxRetry != 3 || arg.MaxDeliveryAttempts != 20 {
		t.Fatalf("outbox options queue/max_retry/max_delivery = %s/%d/%d", arg.QueueName, arg.MaxRetry, arg.MaxDeliveryAttempts)
	}
	var payload tasks.ClusterTemplateApplyPayload
	if err := json.Unmarshal(arg.Payload, &payload); err != nil {
		t.Fatalf("payload JSON: %v", err)
	}
	if payload.ClusterID != clusterID.String() {
		t.Fatalf("payload cluster_id = %q, want %s", payload.ClusterID, clusterID)
	}
}

func TestClusterTemplateApplyWritesApplicationAndTaskOutboxAtomically(t *testing.T) {
	base := newFakeClusterTemplateQuerier()
	clusterID := uuid.New()
	base.clusters[clusterID] = sqlc.Cluster{ID: clusterID, Name: "demo", Environment: "development", Labels: json.RawMessage(`{}`), Annotations: json.RawMessage(`{}`)}
	tmplID := uuid.New()
	base.templates[tmplID] = sqlc.ClusterTemplate{ID: tmplID, Name: "production-web", Spec: json.RawMessage(`{"tools":[]}`)}
	q := &fakeAtomicClusterTemplateQuerier{fakeClusterTemplateQuerier: base}
	outbox := &fakeRegistrationTaskOutbox{}
	cap := &captureEnqueuer{}
	h := NewClusterTemplateHandler(q)
	h.SetQueue(cap)
	h.SetTaskOutbox(outbox)

	body := mustJSON(t, map[string]string{"template_id": tmplID.String()})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/"+clusterID.String()+"/template/", bytes.NewReader(body))
	req = withChiParams(req, map[string]string{"cluster_id": clusterID.String()})
	h.Apply(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("apply: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(q.atomicApps) != 1 {
		t.Fatalf("atomic app+outbox writes = %d, want 1", len(q.atomicApps))
	}
	if len(outbox.all()) != 0 {
		t.Fatalf("separate outbox writes = %d, want 0", len(outbox.all()))
	}
	if cap.count != 0 {
		t.Fatalf("direct enqueues = %d, want 0", cap.count)
	}
	arg := q.atomicApps[0]
	if !arg.DedupeKey.Valid || arg.DedupeKey.String != "cluster_template_apply:"+clusterID.String() {
		t.Fatalf("dedupe key = %+v", arg.DedupeKey)
	}
	if arg.TaskType != tasks.ClusterTemplateApplyType || arg.QueueName != tasks.ClusterTemplateApplyQueueName || arg.MaxRetry != 3 || arg.MaxDeliveryAttempts != 20 {
		t.Fatalf("task metadata = %s/%s/%d/%d", arg.TaskType, arg.QueueName, arg.MaxRetry, arg.MaxDeliveryAttempts)
	}
	var payload tasks.ClusterTemplateApplyPayload
	if err := json.Unmarshal(arg.Payload, &payload); err != nil {
		t.Fatalf("payload JSON: %v", err)
	}
	if payload.ClusterID != clusterID.String() {
		t.Fatalf("payload cluster_id = %q, want %s", payload.ClusterID, clusterID)
	}
}

// TestClusterTemplate_Apply_RejectsUnknownTemplate is the validation
// path for a bind with a non-existent template_id.
func TestClusterTemplate_Apply_RejectsUnknownTemplate(t *testing.T) {
	q := newFakeClusterTemplateQuerier()
	clusterID := uuid.New()
	q.clusters[clusterID] = sqlc.Cluster{ID: clusterID, Name: "demo"}
	h := NewClusterTemplateHandler(q)

	missingID := uuid.New()
	body := mustJSON(t, map[string]string{"template_id": missingID.String()})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req = withChiParams(req, map[string]string{"cluster_id": clusterID.String()})
	h.Apply(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// TestClusterTemplate_Reapply_NoExistingBinding 404s rather than
// silently creating a fresh row.
func TestClusterTemplate_Reapply_NoExistingBinding(t *testing.T) {
	q := newFakeClusterTemplateQuerier()
	clusterID := uuid.New()
	q.clusters[clusterID] = sqlc.Cluster{ID: clusterID, Name: "demo"}
	h := NewClusterTemplateHandler(q)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req = withChiParams(req, map[string]string{"cluster_id": clusterID.String()})
	h.Reapply(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// TestValidateTemplateSpec_AcceptsEmpty is the explicit no-op case —
// an empty spec is a valid template body (template with just a name and
// no policy is useful for grouping/discovery).
func TestValidateTemplateSpec_AcceptsEmpty(t *testing.T) {
	if err := validateTemplateSpec(nil); err != nil {
		t.Errorf("nil spec: %v", err)
	}
	if err := validateTemplateSpec(json.RawMessage(`{}`)); err != nil {
		t.Errorf("empty spec: %v", err)
	}
}

// TestIsUniqueViolation guards the pg-error classification helper from
// drift. We can't easily construct a real *pgconn.PgError without
// dragging the dep into every test, so we settle for the nil-error case
// here; the integration suite covers the live pg path.
func TestIsUniqueViolation_NilSafe(t *testing.T) {
	if isUniqueViolation(nil) {
		t.Errorf("nil error should not be a unique violation")
	}
	if isUniqueViolation(errors.New("not a pg error")) {
		t.Errorf("plain error should not be a unique violation")
	}
	if isFKRestrictViolation(nil) {
		t.Errorf("nil error should not be a fk restrict violation")
	}
}

// ────────────────────────────────────────────────────────────────────────
// Test helpers
// ────────────────────────────────────────────────────────────────────────

// mustJSON is defined in argocd_test.go; reuse it.

// captureEnqueuer is a minimal ClusterTemplateEnqueuer that counts
// successful Enqueue calls. Real asynq.Client isn't needed because the
// handler interfaces against the narrower ClusterTemplateEnqueuer
// surface (Enqueue only).
type captureEnqueuer struct {
	count int
}

func (c *captureEnqueuer) Enqueue(_ *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	c.count++
	return &asynq.TaskInfo{}, nil
}
