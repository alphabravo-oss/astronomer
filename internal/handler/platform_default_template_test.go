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

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// fakePlatformDefaultTemplateQuerier is the in-memory
// PlatformDefaultTemplateQuerier used by the handler tests. The maps
// model the three FK relationships the sprint-074 endpoints care
// about: the singleton platform_configuration row, the
// cluster_templates catalog, and the cluster table — plus a slot for
// the cluster_template_applications upserts written by Reapply.
type fakePlatformDefaultTemplateQuerier struct {
	mu sync.Mutex

	// caller — superuser bit drives the gate response.
	user    sqlc.User
	userErr error

	// platform_configuration singleton state.
	config sqlc.PlatformConfiguration
	// setErr lets a test force a write failure on SetPlatformDefaultClusterTemplate.
	setErr error
	// getCfgErr lets a test force a read failure on GetPlatformConfig.
	getCfgErr error

	templates map[uuid.UUID]sqlc.ClusterTemplate
	clusters  map[uuid.UUID]sqlc.Cluster

	// upserts captures every UpsertClusterTemplateApplication call so
	// the Reapply tests can assert the binding was written.
	upserts []sqlc.UpsertClusterTemplateApplicationParams

	// auditOps captures every audit-row write so the tests can verify
	// the operator-traceable trail.
	auditOps []string
}

func newFakePlatformDefaultTemplateQuerier(user sqlc.User) *fakePlatformDefaultTemplateQuerier {
	return &fakePlatformDefaultTemplateQuerier{
		user:      user,
		templates: map[uuid.UUID]sqlc.ClusterTemplate{},
		clusters:  map[uuid.UUID]sqlc.Cluster{},
	}
}

func (f *fakePlatformDefaultTemplateQuerier) GetUserByID(_ context.Context, _ uuid.UUID) (sqlc.User, error) {
	return f.user, f.userErr
}

func (f *fakePlatformDefaultTemplateQuerier) GetPlatformConfig(_ context.Context) (sqlc.PlatformConfiguration, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getCfgErr != nil {
		return sqlc.PlatformConfiguration{}, f.getCfgErr
	}
	return f.config, nil
}

func (f *fakePlatformDefaultTemplateQuerier) SetPlatformDefaultClusterTemplate(_ context.Context, id pgtype.UUID) (sqlc.PlatformConfiguration, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.setErr != nil {
		return sqlc.PlatformConfiguration{}, f.setErr
	}
	f.config.DefaultClusterTemplateID = id
	return f.config, nil
}

func (f *fakePlatformDefaultTemplateQuerier) GetClusterTemplateByID(_ context.Context, id uuid.UUID) (sqlc.ClusterTemplate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.templates[id]
	if !ok {
		return sqlc.ClusterTemplate{}, pgx.ErrNoRows
	}
	return t, nil
}

func (f *fakePlatformDefaultTemplateQuerier) GetClusterByID(_ context.Context, id uuid.UUID) (sqlc.Cluster, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.clusters[id]
	if !ok {
		return sqlc.Cluster{}, pgx.ErrNoRows
	}
	return c, nil
}

func (f *fakePlatformDefaultTemplateQuerier) UpsertClusterTemplateApplication(_ context.Context, arg sqlc.UpsertClusterTemplateApplicationParams) (sqlc.ClusterTemplateApplication, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.upserts = append(f.upserts, arg)
	return sqlc.ClusterTemplateApplication{
		ClusterID:    arg.ClusterID,
		TemplateID:   arg.TemplateID,
		Status:       "pending",
		SpecSnapshot: arg.SpecSnapshot,
	}, nil
}

// CreateAuditLogV1 satisfies the audit-writer interface so recordAudit
// inside the handler writes through. Counts call actions only.
func (f *fakePlatformDefaultTemplateQuerier) CreateAuditLogV1(_ context.Context, arg sqlc.CreateAuditLogV1Params) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.auditOps = append(f.auditOps, arg.Action)
	return nil
}

// ──────────────────────────────────────────────────────────────────────
// GET
// ──────────────────────────────────────────────────────────────────────

func TestPlatformDefaultTemplate_GetReturnsTemplate(t *testing.T) {
	callerID := uuid.New()
	templateID := uuid.New()
	tmpl := sqlc.ClusterTemplate{
		ID:          templateID,
		Name:        "Platform baseline",
		Description: "auto-baseline",
		Spec:        json.RawMessage(`{"tools":[{"slug":"trivy-operator"}]}`),
	}

	q := newFakePlatformDefaultTemplateQuerier(sqlc.User{ID: callerID, IsSuperuser: true})
	q.templates[templateID] = tmpl
	q.config = sqlc.PlatformConfiguration{
		ID:                       1,
		DefaultClusterTemplateID: pgtype.UUID{Bytes: templateID, Valid: true},
	}

	h := NewPlatformDefaultTemplateHandler(q)
	w := httptest.NewRecorder()
	req := authedRequest(http.MethodGet, "/api/v1/admin/platform-settings/default-cluster-template/", callerID, nil)
	h.Get(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET status = %d, body=%s", w.Code, w.Body.String())
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	var payload defaultTemplateResponse
	if err := json.Unmarshal(envelope["data"], &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.TemplateID == nil || *payload.TemplateID != templateID.String() {
		t.Errorf("template_id = %v, want %s", payload.TemplateID, templateID)
	}
	if payload.Template == nil || payload.Template.Name != "Platform baseline" {
		t.Errorf("template embed missing or wrong name: %+v", payload.Template)
	}
}

func TestPlatformDefaultTemplate_GetReturnsNullWhenUnset(t *testing.T) {
	callerID := uuid.New()
	q := newFakePlatformDefaultTemplateQuerier(sqlc.User{ID: callerID, IsSuperuser: true})
	q.config = sqlc.PlatformConfiguration{ID: 1} // default UUID is Valid:false

	h := NewPlatformDefaultTemplateHandler(q)
	w := httptest.NewRecorder()
	req := authedRequest(http.MethodGet, "/api/v1/admin/platform-settings/default-cluster-template/", callerID, nil)
	h.Get(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET status = %d", w.Code)
	}
	var envelope map[string]json.RawMessage
	_ = json.Unmarshal(w.Body.Bytes(), &envelope)
	var payload defaultTemplateResponse
	_ = json.Unmarshal(envelope["data"], &payload)
	if payload.TemplateID != nil {
		t.Errorf("template_id = %v, want nil (unset)", payload.TemplateID)
	}
	if payload.Template != nil {
		t.Errorf("template embed = %+v, want nil", payload.Template)
	}
}

// ──────────────────────────────────────────────────────────────────────
// PUT
// ──────────────────────────────────────────────────────────────────────

func TestPlatformDefaultTemplate_PUTSetsAndUnsets(t *testing.T) {
	callerID := uuid.New()
	templateID := uuid.New()

	q := newFakePlatformDefaultTemplateQuerier(sqlc.User{ID: callerID, IsSuperuser: true})
	q.templates[templateID] = sqlc.ClusterTemplate{ID: templateID, Name: "Platform baseline"}
	q.config = sqlc.PlatformConfiguration{ID: 1}

	h := NewPlatformDefaultTemplateHandler(q)

	// Set.
	body, _ := json.Marshal(map[string]any{"template_id": templateID.String()})
	w := httptest.NewRecorder()
	req := authedRequest(http.MethodPut, "/api/v1/admin/platform-settings/default-cluster-template/", callerID, body)
	h.Update(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT-set status = %d, body=%s", w.Code, w.Body.String())
	}
	if !q.config.DefaultClusterTemplateID.Valid || uuid.UUID(q.config.DefaultClusterTemplateID.Bytes) != templateID {
		t.Errorf("post-set DefaultClusterTemplateID = %+v, want valid %s", q.config.DefaultClusterTemplateID, templateID)
	}

	// Unset.
	body2, _ := json.Marshal(map[string]any{"template_id": nil})
	w2 := httptest.NewRecorder()
	req2 := authedRequest(http.MethodPut, "/api/v1/admin/platform-settings/default-cluster-template/", callerID, body2)
	h.Update(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("PUT-unset status = %d, body=%s", w2.Code, w2.Body.String())
	}
	if q.config.DefaultClusterTemplateID.Valid {
		t.Errorf("post-unset DefaultClusterTemplateID = %+v, want invalid", q.config.DefaultClusterTemplateID)
	}

	// Audit: one set + one unset.
	if len(q.auditOps) != 2 {
		t.Errorf("audit ops = %v, want 2", q.auditOps)
	}
	for _, a := range q.auditOps {
		if a != "admin.platform_default_template.updated" {
			t.Errorf("audit op = %q, want admin.platform_default_template.updated", a)
		}
	}
}

func TestPlatformDefaultTemplate_PUTRejectsBadTemplateID(t *testing.T) {
	callerID := uuid.New()
	q := newFakePlatformDefaultTemplateQuerier(sqlc.User{ID: callerID, IsSuperuser: true})
	// templates map is empty — every UUID is "not found".
	q.config = sqlc.PlatformConfiguration{ID: 1}

	h := NewPlatformDefaultTemplateHandler(q)

	// Garbage string → 400.
	body, _ := json.Marshal(map[string]any{"template_id": "not-a-uuid"})
	w := httptest.NewRecorder()
	req := authedRequest(http.MethodPut, "/api/v1/admin/platform-settings/default-cluster-template/", callerID, body)
	h.Update(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("PUT garbage status = %d, want 400; body=%s", w.Code, w.Body.String())
	}

	// Valid UUID but not in catalog → 400 (validation_error from our pre-check).
	body2, _ := json.Marshal(map[string]any{"template_id": uuid.New().String()})
	w2 := httptest.NewRecorder()
	req2 := authedRequest(http.MethodPut, "/api/v1/admin/platform-settings/default-cluster-template/", callerID, body2)
	h.Update(w2, req2)
	if w2.Code != http.StatusBadRequest {
		t.Errorf("PUT stale uuid status = %d, want 400; body=%s", w2.Code, w2.Body.String())
	}

	// Both attempts should have left the config unchanged.
	if q.config.DefaultClusterTemplateID.Valid {
		t.Errorf("config was mutated despite validation rejections: %+v", q.config.DefaultClusterTemplateID)
	}
}

// ──────────────────────────────────────────────────────────────────────
// POST /reapply
// ──────────────────────────────────────────────────────────────────────

func TestPlatformDefaultTemplate_ReapplyCreatesApplication(t *testing.T) {
	callerID := uuid.New()
	templateID := uuid.New()
	clusterID := uuid.New()

	q := newFakePlatformDefaultTemplateQuerier(sqlc.User{ID: callerID, IsSuperuser: true})
	q.templates[templateID] = sqlc.ClusterTemplate{
		ID:   templateID,
		Name: "Platform baseline",
		Spec: json.RawMessage(`{"tools":[{"slug":"trivy-operator"}]}`),
	}
	q.clusters[clusterID] = sqlc.Cluster{ID: clusterID, Name: "prod-1"}
	q.config = sqlc.PlatformConfiguration{
		ID:                       1,
		DefaultClusterTemplateID: pgtype.UUID{Bytes: templateID, Valid: true},
	}

	h := NewPlatformDefaultTemplateHandler(q)
	w := httptest.NewRecorder()
	req := withURLParam(
		authedRequest(http.MethodPost,
			"/api/v1/admin/platform-settings/default-cluster-template/reapply/"+clusterID.String()+"/",
			callerID, []byte("{}")),
		"cluster_id", clusterID.String(),
	)
	h.Reapply(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("reapply status = %d, body=%s", w.Code, w.Body.String())
	}
	if len(q.upserts) != 1 {
		t.Fatalf("upserts = %d, want 1", len(q.upserts))
	}
	up := q.upserts[0]
	if up.ClusterID != clusterID {
		t.Errorf("upsert ClusterID = %s, want %s", up.ClusterID, clusterID)
	}
	if up.TemplateID != templateID {
		t.Errorf("upsert TemplateID = %s, want %s", up.TemplateID, templateID)
	}
}

func TestPlatformDefaultTemplate_ReapplyReturns409WhenNoDefaultSet(t *testing.T) {
	callerID := uuid.New()
	clusterID := uuid.New()
	q := newFakePlatformDefaultTemplateQuerier(sqlc.User{ID: callerID, IsSuperuser: true})
	q.clusters[clusterID] = sqlc.Cluster{ID: clusterID, Name: "prod-1"}
	q.config = sqlc.PlatformConfiguration{ID: 1} // no default

	h := NewPlatformDefaultTemplateHandler(q)
	w := httptest.NewRecorder()
	req := withURLParam(
		authedRequest(http.MethodPost,
			"/api/v1/admin/platform-settings/default-cluster-template/reapply/"+clusterID.String()+"/",
			callerID, []byte("{}")),
		"cluster_id", clusterID.String(),
	)
	h.Reapply(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("reapply status = %d, want 409 (no default configured); body=%s", w.Code, w.Body.String())
	}
	if len(q.upserts) != 0 {
		t.Errorf("upserts written despite no default: %d", len(q.upserts))
	}
}

// ──────────────────────────────────────────────────────────────────────
// Superuser gate
// ──────────────────────────────────────────────────────────────────────

func TestPlatformDefaultTemplate_RequiresSuperuser(t *testing.T) {
	callerID := uuid.New()
	q := newFakePlatformDefaultTemplateQuerier(sqlc.User{ID: callerID, IsSuperuser: false})
	q.config = sqlc.PlatformConfiguration{ID: 1}

	h := NewPlatformDefaultTemplateHandler(q)

	// GET.
	w := httptest.NewRecorder()
	req := authedRequest(http.MethodGet, "/api/v1/admin/platform-settings/default-cluster-template/", callerID, nil)
	h.Get(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("GET non-superuser status = %d, want 403; body=%s", w.Code, w.Body.String())
	}

	// PUT.
	body, _ := json.Marshal(map[string]any{"template_id": nil})
	w2 := httptest.NewRecorder()
	req2 := authedRequest(http.MethodPut, "/api/v1/admin/platform-settings/default-cluster-template/", callerID, body)
	h.Update(w2, req2)
	if w2.Code != http.StatusForbidden {
		t.Errorf("PUT non-superuser status = %d, want 403; body=%s", w2.Code, w2.Body.String())
	}

	// Reapply.
	clusterID := uuid.New()
	w3 := httptest.NewRecorder()
	req3 := withURLParam(
		authedRequest(http.MethodPost, "/api/v1/admin/platform-settings/default-cluster-template/reapply/"+clusterID.String()+"/", callerID, []byte("{}")),
		"cluster_id", clusterID.String(),
	)
	h.Reapply(w3, req3)
	if w3.Code != http.StatusForbidden {
		t.Errorf("Reapply non-superuser status = %d, want 403; body=%s", w3.Code, w3.Body.String())
	}

	// Auditor: zero ops — gate rejects before any write.
	if len(q.auditOps) != 0 {
		t.Errorf("audit ops on rejected calls = %v, want 0", q.auditOps)
	}
}

// Guard against silently dropping the cfg fetch error in GET — a 500
// shouldn't leak the empty struct as "no default configured".
func TestPlatformDefaultTemplate_GetSurfaceDBErrorsAs500(t *testing.T) {
	callerID := uuid.New()
	q := newFakePlatformDefaultTemplateQuerier(sqlc.User{ID: callerID, IsSuperuser: true})
	q.getCfgErr = errors.New("boom")

	h := NewPlatformDefaultTemplateHandler(q)
	w := httptest.NewRecorder()
	req := authedRequest(http.MethodGet, "/api/v1/admin/platform-settings/default-cluster-template/", callerID, nil)
	h.Get(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("GET on db_error status = %d, want 500", w.Code)
	}
}

// authedRequest + withURLParam are shared with platform_settings_test.go.
// We rely on them living in the same package — guard against accidental
// removal so test compilation is the canary.
var _ = bytes.NewReader
