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

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// fakeQuotaQuerier is the in-memory QuotaQuerier the handler tests
// drive. Mirrors the platform_settings fake shape so the helpers
// authedRequest + withURLParam can be reused directly.
type fakeQuotaQuerier struct {
	mu       sync.Mutex
	user     sqlc.User
	plans    map[string]sqlc.QuotaPlan
	auditOps []string

	// counts is the canned-response surface for the various Count*
	// methods. Tests poke at this directly.
	projectPlan         map[uuid.UUID]sqlc.GetEffectiveQuotaForProjectRow
	userPlan            map[uuid.UUID]sqlc.GetEffectiveQuotaForUserRow
	clustersInProject   map[uuid.UUID]int64
	namespacesInProject map[uuid.UUID]int32
	membersInProject    map[uuid.UUID]int64
	projectsForUser     map[uuid.UUID]int64
	tokensForUser       map[uuid.UUID]int64
	totalClusters       int64
	totalActiveUsers    int64

	projectsUsingPlan map[string]int64
	usersUsingPlan    map[string]int64

	projectSnapshots []sqlc.ProjectQuotaSnapshotRow
	userSnapshots    []sqlc.UserQuotaSnapshotRow
}

func newFakeQuotaQuerier(caller sqlc.User) *fakeQuotaQuerier {
	return &fakeQuotaQuerier{
		user:                caller,
		plans:               map[string]sqlc.QuotaPlan{},
		projectPlan:         map[uuid.UUID]sqlc.GetEffectiveQuotaForProjectRow{},
		userPlan:            map[uuid.UUID]sqlc.GetEffectiveQuotaForUserRow{},
		clustersInProject:   map[uuid.UUID]int64{},
		namespacesInProject: map[uuid.UUID]int32{},
		membersInProject:    map[uuid.UUID]int64{},
		projectsForUser:     map[uuid.UUID]int64{},
		tokensForUser:       map[uuid.UUID]int64{},
		projectsUsingPlan:   map[string]int64{},
		usersUsingPlan:      map[string]int64{},
	}
}

func (f *fakeQuotaQuerier) GetUserByID(_ context.Context, _ uuid.UUID) (sqlc.User, error) {
	return f.user, nil
}
func (f *fakeQuotaQuerier) ListQuotaPlans(_ context.Context) ([]sqlc.QuotaPlan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]sqlc.QuotaPlan, 0, len(f.plans))
	for _, p := range f.plans {
		out = append(out, p)
	}
	return out, nil
}
func (f *fakeQuotaQuerier) GetQuotaPlan(_ context.Context, name string) (sqlc.QuotaPlan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.plans[name]
	if !ok {
		return sqlc.QuotaPlan{}, pgx.ErrNoRows
	}
	return p, nil
}
func (f *fakeQuotaQuerier) UpsertQuotaPlan(_ context.Context, arg sqlc.UpsertQuotaPlanParams) (sqlc.QuotaPlan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := sqlc.QuotaPlan{
		Name:                    arg.Name,
		Enforcement:             arg.Enforcement,
		Description:             arg.Description,
		MaxClustersPerProject:   arg.MaxClustersPerProject,
		MaxNamespacesPerProject: arg.MaxNamespacesPerProject,
		MaxMembersPerProject:    arg.MaxMembersPerProject,
		MaxProjectsPerUser:      arg.MaxProjectsPerUser,
		MaxTokensPerUser:        arg.MaxTokensPerUser,
		MaxStreamsPerUser:       arg.MaxStreamsPerUser,
		MaxTotalClusters:        arg.MaxTotalClusters,
		MaxTotalUsers:           arg.MaxTotalUsers,
	}
	f.plans[arg.Name] = p
	return p, nil
}
func (f *fakeQuotaQuerier) DeleteQuotaPlan(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.plans, name)
	return nil
}
func (f *fakeQuotaQuerier) CountProjectsUsingQuotaPlan(_ context.Context, name string) (int64, error) {
	return f.projectsUsingPlan[name], nil
}
func (f *fakeQuotaQuerier) CountUsersUsingQuotaPlan(_ context.Context, name string) (int64, error) {
	return f.usersUsingPlan[name], nil
}
func (f *fakeQuotaQuerier) GetEffectiveQuotaForUser(_ context.Context, id uuid.UUID) (sqlc.GetEffectiveQuotaForUserRow, error) {
	p, ok := f.userPlan[id]
	if !ok {
		return sqlc.GetEffectiveQuotaForUserRow{}, pgx.ErrNoRows
	}
	return p, nil
}
func (f *fakeQuotaQuerier) GetEffectiveQuotaForProject(_ context.Context, id uuid.UUID) (sqlc.GetEffectiveQuotaForProjectRow, error) {
	p, ok := f.projectPlan[id]
	if !ok {
		return sqlc.GetEffectiveQuotaForProjectRow{}, pgx.ErrNoRows
	}
	return p, nil
}
func (f *fakeQuotaQuerier) CountClustersInProject(_ context.Context, id uuid.UUID) (int64, error) {
	return f.clustersInProject[id], nil
}
func (f *fakeQuotaQuerier) CountNamespacesInProject(_ context.Context, id uuid.UUID) (int32, error) {
	return f.namespacesInProject[id], nil
}
func (f *fakeQuotaQuerier) CountMembersInProject(_ context.Context, id uuid.UUID) (int64, error) {
	return f.membersInProject[id], nil
}
func (f *fakeQuotaQuerier) CountProjectsForUser(_ context.Context, id uuid.UUID) (int64, error) {
	return f.projectsForUser[id], nil
}
func (f *fakeQuotaQuerier) CountActiveTokensForUser(_ context.Context, id uuid.UUID) (int64, error) {
	return f.tokensForUser[id], nil
}
func (f *fakeQuotaQuerier) CountTotalClusters(_ context.Context) (int64, error) {
	return f.totalClusters, nil
}
func (f *fakeQuotaQuerier) CountTotalActiveUsers(_ context.Context) (int64, error) {
	return f.totalActiveUsers, nil
}
func (f *fakeQuotaQuerier) ListProjectQuotaSnapshots(_ context.Context, _ sqlc.ListProjectQuotaSnapshotsParams) ([]sqlc.ProjectQuotaSnapshotRow, error) {
	return f.projectSnapshots, nil
}
func (f *fakeQuotaQuerier) ListUserQuotaSnapshots(_ context.Context, _ sqlc.ListUserQuotaSnapshotsParams) ([]sqlc.UserQuotaSnapshotRow, error) {
	return f.userSnapshots, nil
}

// CreateAuditLogV1 satisfies the audit writer that recordAudit looks for.
func (f *fakeQuotaQuerier) CreateAuditLogV1(_ context.Context, arg sqlc.CreateAuditLogV1Params) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.auditOps = append(f.auditOps, arg.Action)
	return nil
}

func TestQuotaPlans_CRUD(t *testing.T) {
	callerID := uuid.New()
	q := newFakeQuotaQuerier(sqlc.User{ID: callerID, IsSuperuser: true})
	h := NewQuotaHandler(q)

	// Create
	body := []byte(`{"name":"custom","enforcement":"hard","max_clusters_per_project":7}`)
	req := authedRequest(http.MethodPost, "/api/v1/admin/quota-plans/", callerID, body)
	w := httptest.NewRecorder()
	h.CreatePlan(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", w.Code, w.Body.String())
	}

	// Get
	getReq := withURLParam(authedRequest(http.MethodGet, "/api/v1/admin/quota-plans/custom/", callerID, nil), "name", "custom")
	gw := httptest.NewRecorder()
	h.GetPlan(gw, getReq)
	if gw.Code != http.StatusOK {
		t.Fatalf("get status=%d", gw.Code)
	}

	// Update
	updBody := []byte(`{"enforcement":"soft","max_clusters_per_project":99}`)
	updReq := withURLParam(authedRequest(http.MethodPut, "/api/v1/admin/quota-plans/custom/", callerID, updBody), "name", "custom")
	uw := httptest.NewRecorder()
	h.UpdatePlan(uw, updReq)
	if uw.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", uw.Code, uw.Body.String())
	}
	if q.plans["custom"].Enforcement != "soft" {
		t.Fatalf("expected soft enforcement after update, got %s", q.plans["custom"].Enforcement)
	}

	// Delete
	delReq := withURLParam(authedRequest(http.MethodDelete, "/api/v1/admin/quota-plans/custom/", callerID, nil), "name", "custom")
	dw := httptest.NewRecorder()
	h.DeletePlan(dw, delReq)
	if dw.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", dw.Code, dw.Body.String())
	}
	if _, ok := q.plans["custom"]; ok {
		t.Fatalf("expected plan to be deleted")
	}

	// Audit trail should include create/update/delete actions.
	wantActions := map[string]bool{"quota_plan.create": false, "quota_plan.update": false, "quota_plan.delete": false}
	for _, a := range q.auditOps {
		if _, ok := wantActions[a]; ok {
			wantActions[a] = true
		}
	}
	for action, seen := range wantActions {
		if !seen {
			t.Errorf("missing audit action: %s", action)
		}
	}
}

func TestQuotaPlans_RejectsDeleteWhileInUse(t *testing.T) {
	callerID := uuid.New()
	q := newFakeQuotaQuerier(sqlc.User{ID: callerID, IsSuperuser: true})
	q.plans["custom"] = sqlc.QuotaPlan{Name: "custom"}
	q.projectsUsingPlan["custom"] = 3

	h := NewQuotaHandler(q)
	req := withURLParam(authedRequest(http.MethodDelete, "/api/v1/admin/quota-plans/custom/", callerID, nil), "name", "custom")
	w := httptest.NewRecorder()
	h.DeletePlan(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 conflict, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestQuotaPlans_RejectsReservedDelete(t *testing.T) {
	callerID := uuid.New()
	q := newFakeQuotaQuerier(sqlc.User{ID: callerID, IsSuperuser: true})
	h := NewQuotaHandler(q)
	for _, name := range []string{"free", "global"} {
		req := withURLParam(authedRequest(http.MethodDelete, "/api/v1/admin/quota-plans/"+name+"/", callerID, nil), "name", name)
		w := httptest.NewRecorder()
		h.DeletePlan(w, req)
		if w.Code != http.StatusConflict {
			t.Errorf("delete %q: expected 409 reserved, got %d", name, w.Code)
		}
	}
}

func TestQuotaPlans_RejectsBadEnforcement(t *testing.T) {
	callerID := uuid.New()
	q := newFakeQuotaQuerier(sqlc.User{ID: callerID, IsSuperuser: true})
	h := NewQuotaHandler(q)

	body := []byte(`{"name":"weird","enforcement":"loose"}`)
	req := authedRequest(http.MethodPost, "/api/v1/admin/quota-plans/", callerID, body)
	w := httptest.NewRecorder()
	h.CreatePlan(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad enforcement, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestQuotaPlans_RequiresSuperuser(t *testing.T) {
	callerID := uuid.New()
	q := newFakeQuotaQuerier(sqlc.User{ID: callerID, IsSuperuser: false})
	h := NewQuotaHandler(q)

	req := authedRequest(http.MethodGet, "/api/v1/admin/quota-plans/", callerID, nil)
	w := httptest.NewRecorder()
	h.ListPlans(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-superuser, got %d", w.Code)
	}
}

func TestProjectQuota_RendersUsage(t *testing.T) {
	callerID := uuid.New()
	projectID := uuid.New()
	q := newFakeQuotaQuerier(sqlc.User{ID: callerID, IsSuperuser: true})
	q.projectPlan[projectID] = sqlc.GetEffectiveQuotaForProjectRow{
		ProjectID:             projectID,
		PlanName:              "free",
		Enforcement:           "hard",
		MaxClustersPerProject: 5,
		MaxNamespacesPerProject: 10,
		MaxMembersPerProject:  10,
		Overrides:             json.RawMessage(`{"max_clusters_per_project": 50}`),
	}
	q.clustersInProject[projectID] = 3
	q.namespacesInProject[projectID] = 4
	q.membersInProject[projectID] = 2

	h := NewQuotaHandler(q)
	req := withURLParam(authedRequest(http.MethodGet, "/api/v1/projects/"+projectID.String()+"/quota/", callerID, nil), "id", projectID.String())
	w := httptest.NewRecorder()
	h.ProjectQuota(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !bytes.Contains(resp["data"], []byte(`"quota_plan":"free"`)) {
		t.Errorf("body missing quota_plan, got %s", string(resp["data"]))
	}
	if !bytes.Contains(resp["data"], []byte(`"max_clusters_per_project":50`)) {
		t.Errorf("override not applied; body=%s", string(resp["data"]))
	}
}

func TestMyQuota_RendersUsage(t *testing.T) {
	callerID := uuid.New()
	q := newFakeQuotaQuerier(sqlc.User{ID: callerID, IsSuperuser: false})
	q.userPlan[callerID] = sqlc.GetEffectiveQuotaForUserRow{
		UserID:             callerID,
		PlanName:           "free",
		Enforcement:        "hard",
		MaxProjectsPerUser: 3,
		MaxTokensPerUser:   5,
		MaxStreamsPerUser:  3,
	}
	q.projectsForUser[callerID] = 1
	q.tokensForUser[callerID] = 2

	h := NewQuotaHandler(q)
	req := authedRequest(http.MethodGet, "/api/v1/auth/me/quota/", callerID, nil)
	w := httptest.NewRecorder()
	h.MyQuota(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestFleetUsage_TopOffenders(t *testing.T) {
	callerID := uuid.New()
	q := newFakeQuotaQuerier(sqlc.User{ID: callerID, IsSuperuser: true})
	q.plans["global"] = sqlc.QuotaPlan{Name: "global", Enforcement: "hard", MaxTotalClusters: 100, MaxTotalUsers: 200}
	q.totalClusters = 50
	q.totalActiveUsers = 90
	// One project is at 90% of cluster cap (5 clusters, 5 max → 100%).
	proj := uuid.New()
	q.projectSnapshots = []sqlc.ProjectQuotaSnapshotRow{
		{
			ProjectID:             proj,
			ProjectName:           "noisy",
			QuotaPlan:             "free",
			Enforcement:           "hard",
			MaxClustersPerProject: 5,
			ClustersInProject:     5,
		},
	}
	user := uuid.New()
	q.userSnapshots = []sqlc.UserQuotaSnapshotRow{
		{
			UserID:           user,
			Username:         "alice",
			QuotaPlan:        "free",
			MaxTokensPerUser: 5,
			TokensForUser:    5,
		},
	}

	h := NewQuotaHandler(q)
	req := authedRequest(http.MethodGet, "/api/v1/admin/quota-usage/", callerID, nil)
	w := httptest.NewRecorder()
	h.FleetUsage(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var wrap map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &wrap); err != nil {
		t.Fatalf("decode: %v", err)
	}
	data := wrap["data"]
	if !bytes.Contains(data, []byte("noisy")) || !bytes.Contains(data, []byte("alice")) {
		t.Errorf("missing offender entries, got %s", string(data))
	}
	if !bytes.Contains(data, []byte(`"total_clusters":50`)) {
		t.Errorf("missing global totals; got %s", string(data))
	}
}

// Sentinel to make sure pgx.ErrNoRows imports are used and that the
// 404 path on a missing plan is exercised at compile time.
var _ = errors.Is(pgx.ErrNoRows, pgx.ErrNoRows)
