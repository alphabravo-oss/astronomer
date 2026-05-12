package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/compliance"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// fakeBaselineDB is the handler-test fake. It implements both the
// narrow ComplianceBaselineReader the handler reads from AND the
// wider compliance.Querier the engine writes through — so the same
// fake serves as the runTx-passed "tx Querier" without any actual
// transaction logic.
type fakeBaselineDB struct {
	mu           sync.Mutex
	user         sqlc.User
	baselines    map[uuid.UUID]sqlc.ComplianceBaseline
	applications []sqlc.ComplianceBaselineApplication
	settings     map[string]sqlc.PlatformSetting
	quotaPlans   map[string]sqlc.QuotaPlan
	auditOps     []string
}

func newFakeBaselineDB(user sqlc.User) *fakeBaselineDB {
	return &fakeBaselineDB{
		user:       user,
		baselines:  map[uuid.UUID]sqlc.ComplianceBaseline{},
		settings:   map[string]sqlc.PlatformSetting{},
		quotaPlans: map[string]sqlc.QuotaPlan{},
	}
}

func (f *fakeBaselineDB) seedBaseline(slug, name string) uuid.UUID {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := uuid.New()
	f.baselines[id] = sqlc.ComplianceBaseline{
		ID: id, Slug: slug, Name: name, Description: name, Version: "1.0",
		Spec: json.RawMessage("{}"), Enabled: true,
	}
	return id
}

// ── ComplianceBaselineReader ──────────────────────────────────────────

func (f *fakeBaselineDB) GetUserByID(_ context.Context, _ uuid.UUID) (sqlc.User, error) {
	return f.user, nil
}

func (f *fakeBaselineDB) ListComplianceBaselines(_ context.Context) ([]sqlc.ComplianceBaseline, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]sqlc.ComplianceBaseline, 0, len(f.baselines))
	for _, b := range f.baselines {
		out = append(out, b)
	}
	return out, nil
}

func (f *fakeBaselineDB) GetComplianceBaseline(_ context.Context, id uuid.UUID) (sqlc.ComplianceBaseline, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.baselines[id]
	if !ok {
		return sqlc.ComplianceBaseline{}, pgx.ErrNoRows
	}
	return row, nil
}

func (f *fakeBaselineDB) GetComplianceBaselineApplication(_ context.Context, id uuid.UUID) (sqlc.ComplianceBaselineApplication, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, a := range f.applications {
		if a.ID == id {
			return a, nil
		}
	}
	return sqlc.ComplianceBaselineApplication{}, pgx.ErrNoRows
}

func (f *fakeBaselineDB) GetActiveComplianceBaselineApplication(_ context.Context) (sqlc.ComplianceBaselineApplication, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.applications) - 1; i >= 0; i-- {
		if f.applications[i].Status == "applied" {
			return f.applications[i], nil
		}
	}
	return sqlc.ComplianceBaselineApplication{}, pgx.ErrNoRows
}

func (f *fakeBaselineDB) ListComplianceBaselineApplications(_ context.Context, limit int32) ([]sqlc.ComplianceBaselineApplication, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]sqlc.ComplianceBaselineApplication, 0, len(f.applications))
	for i := len(f.applications) - 1; i >= 0; i-- {
		out = append(out, f.applications[i])
		if int32(len(out)) >= limit {
			break
		}
	}
	return out, nil
}

func (f *fakeBaselineDB) GetPlatformSetting(_ context.Context, key string) (sqlc.PlatformSetting, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.settings[key]
	if !ok {
		return sqlc.PlatformSetting{}, pgx.ErrNoRows
	}
	return row, nil
}

func (f *fakeBaselineDB) GetQuotaPlan(_ context.Context, name string) (sqlc.QuotaPlan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.quotaPlans[name]
	if !ok {
		return sqlc.QuotaPlan{}, pgx.ErrNoRows
	}
	return p, nil
}

// ── compliance.Querier writes ─────────────────────────────────────────

func (f *fakeBaselineDB) CreateComplianceBaselineApplication(_ context.Context, arg sqlc.CreateComplianceBaselineApplicationParams) (sqlc.ComplianceBaselineApplication, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	app := sqlc.ComplianceBaselineApplication{
		ID:            uuid.New(),
		BaselineID:    arg.BaselineID,
		PreviousState: arg.PreviousState,
		AppliedBy:     arg.AppliedBy,
		AppliedAt:     time.Now().UTC(),
		Status:        "applied",
		Notes:         arg.Notes,
	}
	f.applications = append(f.applications, app)
	return app, nil
}

func (f *fakeBaselineDB) MarkComplianceBaselineApplicationReverted(_ context.Context, arg sqlc.MarkComplianceBaselineApplicationRevertedParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.applications {
		if f.applications[i].ID == arg.ID && f.applications[i].Status == "applied" {
			f.applications[i].Status = "reverted"
			f.applications[i].RevertedBy = arg.RevertedBy
			f.applications[i].RevertedAt = pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}
			return nil
		}
	}
	return nil
}

func (f *fakeBaselineDB) UpsertPlatformSetting(_ context.Context, arg sqlc.UpsertPlatformSettingParams) (sqlc.PlatformSetting, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row := sqlc.PlatformSetting{Key: arg.Key, Value: arg.Value, Description: arg.Description, UpdatedBy: arg.UpdatedBy, UpdatedAt: time.Now().UTC()}
	f.settings[arg.Key] = row
	return row, nil
}

func (f *fakeBaselineDB) UpsertQuotaPlan(_ context.Context, arg sqlc.UpsertQuotaPlanParams) (sqlc.QuotaPlan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	plan := sqlc.QuotaPlan{
		Name: arg.Name, Enforcement: arg.Enforcement, Description: arg.Description,
		MaxClustersPerProject:   arg.MaxClustersPerProject,
		MaxNamespacesPerProject: arg.MaxNamespacesPerProject,
		MaxMembersPerProject:    arg.MaxMembersPerProject,
		MaxProjectsPerUser:      arg.MaxProjectsPerUser,
		MaxTokensPerUser:        arg.MaxTokensPerUser,
		MaxStreamsPerUser:       arg.MaxStreamsPerUser,
		MaxTotalClusters:        arg.MaxTotalClusters,
		MaxTotalUsers:           arg.MaxTotalUsers,
	}
	f.quotaPlans[arg.Name] = plan
	return plan, nil
}

// CreateAuditLogV1 — satisfy auditWriterV1 so recordAudit doesn't no-op.
func (f *fakeBaselineDB) CreateAuditLogV1(_ context.Context, arg sqlc.CreateAuditLogV1Params) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.auditOps = append(f.auditOps, arg.Action)
	return nil
}

// runTxInline is the test's no-op tx wrapper: just call the fn with
// the same fake Querier. Engines that error roll back nothing (the
// fake doesn't model partial writes); engines that succeed commit
// nothing. This is OK for our tests because every write is idempotent.
func (f *fakeBaselineDB) runTxInline(ctx context.Context, fn func(q compliance.Querier) error) error {
	return fn(f)
}

// ── tests ─────────────────────────────────────────────────────────────

func TestComplianceHandler_RequiresSuperuser(t *testing.T) {
	callerID := uuid.New()
	// Non-superuser caller.
	q := newFakeBaselineDB(sqlc.User{ID: callerID, IsSuperuser: false})
	h := NewComplianceBaselinesHandler(q, q.runTxInline, nil)

	r := authedRequest(http.MethodGet, "/api/v1/admin/compliance-baselines/", callerID, nil)
	w := httptest.NewRecorder()
	h.List(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("List as non-superuser status = %d, want 403; body=%s", w.Code, w.Body.String())
	}

	r = authedRequest(http.MethodPost, "/api/v1/admin/compliance-baselines/abc/apply/", callerID, []byte("{}"))
	w = httptest.NewRecorder()
	h.Apply(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("Apply as non-superuser status = %d, want 403", w.Code)
	}
}

func TestComplianceHandler_ListJoinsRegistry(t *testing.T) {
	callerID := uuid.New()
	q := newFakeBaselineDB(sqlc.User{ID: callerID, IsSuperuser: true})
	q.seedBaseline("pci_dss_4_0", "PCI-DSS 4.0")
	q.seedBaseline("soc2", "SOC 2")
	h := NewComplianceBaselinesHandler(q, q.runTxInline, nil)

	r := authedRequest(http.MethodGet, "/api/v1/admin/compliance-baselines/", callerID, nil)
	w := httptest.NewRecorder()
	h.List(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("List status = %d, body=%s", w.Code, w.Body.String())
	}
	// Response is wrapped in {"data": [...]}.
	var wrapper map[string]json.RawMessage
	json.Unmarshal(w.Body.Bytes(), &wrapper)
	var entries []baselineResponse
	if err := json.Unmarshal(wrapper["data"], &entries); err != nil {
		t.Fatalf("decode entries: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
	// Each entry should have the registry's Spec joined in (not empty).
	for _, e := range entries {
		if e.Slug == "pci_dss_4_0" && e.Spec.AuditRetentionDays != 365 {
			t.Errorf("PCI entry spec.audit_retention_days = %d, want 365 (registry join)", e.Spec.AuditRetentionDays)
		}
		if e.Slug == "soc2" && e.Spec.PSSProfile != "baseline" {
			t.Errorf("SOC2 entry spec.pss_profile = %q, want baseline", e.Spec.PSSProfile)
		}
	}
}

func TestComplianceHandler_ApplyEndToEnd(t *testing.T) {
	callerID := uuid.New()
	q := newFakeBaselineDB(sqlc.User{ID: callerID, IsSuperuser: true})
	pciID := q.seedBaseline("pci_dss_4_0", "PCI-DSS 4.0")
	h := NewComplianceBaselinesHandler(q, q.runTxInline, nil)

	r := withURLParam(
		authedRequest(http.MethodPost, "/api/v1/admin/compliance-baselines/"+pciID.String()+"/apply/", callerID, []byte(`{"notes":"Q4 audit"}`)),
		"id", pciID.String(),
	)
	w := httptest.NewRecorder()
	h.Apply(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("Apply status = %d, body=%s", w.Code, w.Body.String())
	}
	if got := q.settings["audit.retention_days"]; len(got.Value) == 0 {
		t.Error("audit.retention_days not written by Apply")
	}
	// Active endpoint should now report pci_dss_4_0.
	w2 := httptest.NewRecorder()
	h.Active(w2, authedRequest(http.MethodGet, "/api/v1/admin/compliance-baselines/active/", callerID, nil))
	if w2.Code != http.StatusOK {
		t.Fatalf("Active status = %d, body=%s", w2.Code, w2.Body.String())
	}
	body := w2.Body.String()
	if !bytes.Contains([]byte(body), []byte("pci_dss_4_0")) {
		t.Errorf("Active response should reference pci_dss_4_0; got %s", body)
	}
}

func TestComplianceHandler_DiffPreview(t *testing.T) {
	callerID := uuid.New()
	q := newFakeBaselineDB(sqlc.User{ID: callerID, IsSuperuser: true})
	pciID := q.seedBaseline("pci_dss_4_0", "PCI-DSS 4.0")
	h := NewComplianceBaselinesHandler(q, q.runTxInline, nil)

	r := withURLParam(
		authedRequest(http.MethodGet, "/api/v1/admin/compliance-baselines/"+pciID.String()+"/diff/", callerID, nil),
		"id", pciID.String(),
	)
	w := httptest.NewRecorder()
	h.Diff(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("Diff status = %d, body=%s", w.Code, w.Body.String())
	}
	var wrapper map[string]json.RawMessage
	json.Unmarshal(w.Body.Bytes(), &wrapper)
	var res compliance.DiffResult
	if err := json.Unmarshal(wrapper["data"], &res); err != nil {
		t.Fatalf("decode diff: %v", err)
	}
	if res.BaselineSlug != "pci_dss_4_0" {
		t.Errorf("Diff slug = %q, want pci_dss_4_0", res.BaselineSlug)
	}
	if len(res.Changes) == 0 {
		t.Error("Diff should report changes on a fresh DB")
	}
}

func TestComplianceHandler_RevertRefusesIfNewer(t *testing.T) {
	callerID := uuid.New()
	q := newFakeBaselineDB(sqlc.User{ID: callerID, IsSuperuser: true})
	pciID := q.seedBaseline("pci_dss_4_0", "PCI-DSS 4.0")
	soc2ID := q.seedBaseline("soc2", "SOC 2")
	h := NewComplianceBaselinesHandler(q, q.runTxInline, nil)

	// Apply PCI then SOC2.
	r1 := withURLParam(
		authedRequest(http.MethodPost, "/api/v1/admin/compliance-baselines/"+pciID.String()+"/apply/", callerID, []byte(`{}`)),
		"id", pciID.String(),
	)
	h.Apply(httptest.NewRecorder(), r1)
	r2 := withURLParam(
		authedRequest(http.MethodPost, "/api/v1/admin/compliance-baselines/"+soc2ID.String()+"/apply/", callerID, []byte(`{}`)),
		"id", soc2ID.String(),
	)
	h.Apply(httptest.NewRecorder(), r2)

	// The first application is no longer the latest — revert should refuse.
	if len(q.applications) != 2 {
		t.Fatalf("applications=%d, want 2", len(q.applications))
	}
	firstID := q.applications[0].ID
	w := httptest.NewRecorder()
	rev := withURLParam(
		authedRequest(http.MethodPost, "/api/v1/admin/compliance-baseline-applications/"+firstID.String()+"/revert/", callerID, nil),
		"id", firstID.String(),
	)
	h.Revert(w, rev)
	if w.Code != http.StatusConflict {
		t.Errorf("Revert-of-older status = %d, want 409; body=%s", w.Code, w.Body.String())
	}
}

func TestComplianceHandler_History(t *testing.T) {
	callerID := uuid.New()
	q := newFakeBaselineDB(sqlc.User{ID: callerID, IsSuperuser: true})
	id := q.seedBaseline("soc2", "SOC 2")
	h := NewComplianceBaselinesHandler(q, q.runTxInline, nil)

	r := withURLParam(
		authedRequest(http.MethodPost, "/api/v1/admin/compliance-baselines/"+id.String()+"/apply/", callerID, []byte(`{}`)),
		"id", id.String(),
	)
	h.Apply(httptest.NewRecorder(), r)

	w := httptest.NewRecorder()
	h.History(w, authedRequest(http.MethodGet, "/api/v1/admin/compliance-baseline-applications/", callerID, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("History status = %d, body=%s", w.Code, w.Body.String())
	}
	var wrapper map[string]json.RawMessage
	json.Unmarshal(w.Body.Bytes(), &wrapper)
	var apps []applicationResponse
	json.Unmarshal(wrapper["data"], &apps)
	if len(apps) != 1 {
		t.Errorf("history entries = %d, want 1", len(apps))
	}
	if apps[0].BaselineSlug != "soc2" {
		t.Errorf("history entry slug = %q, want soc2", apps[0].BaselineSlug)
	}
}
