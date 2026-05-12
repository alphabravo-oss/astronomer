package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/maintenance"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

// fakeMaintenanceQuerier is the narrowest MaintenanceQuerier the test
// suite needs. It also satisfies the implicit auditWriterV1 surface so
// recordAudit inside the handler doesn't no-op.
type fakeMaintenanceQuerier struct {
	user    sqlc.User
	userErr error

	windows       []sqlc.MaintenanceWindow
	getWindowErr  error
	createdRow    sqlc.MaintenanceWindow
	createErr     error
	updatedRow    sqlc.MaintenanceWindow
	updateErr     error
	deleteErr     error
	nameExists    bool

	deferred       []sqlc.DeferredOperation
	deferredCount  int64
	getDeferredErr error
	getDeferredRow sqlc.DeferredOperation
	cancelCalled   int

	auditCalls int
}

func (f *fakeMaintenanceQuerier) GetUserByID(_ context.Context, _ uuid.UUID) (sqlc.User, error) {
	return f.user, f.userErr
}
func (f *fakeMaintenanceQuerier) ListMaintenanceWindows(_ context.Context) ([]sqlc.MaintenanceWindow, error) {
	return f.windows, nil
}
func (f *fakeMaintenanceQuerier) GetMaintenanceWindow(_ context.Context, id uuid.UUID) (sqlc.MaintenanceWindow, error) {
	if f.getWindowErr != nil {
		return sqlc.MaintenanceWindow{}, f.getWindowErr
	}
	for _, w := range f.windows {
		if w.ID == id {
			return w, nil
		}
	}
	return sqlc.MaintenanceWindow{}, pgx.ErrNoRows
}
func (f *fakeMaintenanceQuerier) GetMaintenanceWindowByName(_ context.Context, name string) (sqlc.MaintenanceWindow, error) {
	if f.nameExists {
		return sqlc.MaintenanceWindow{Name: name, ID: uuid.New()}, nil
	}
	return sqlc.MaintenanceWindow{}, pgx.ErrNoRows
}
func (f *fakeMaintenanceQuerier) CreateMaintenanceWindow(_ context.Context, _ sqlc.CreateMaintenanceWindowParams) (sqlc.MaintenanceWindow, error) {
	if f.createErr != nil {
		return sqlc.MaintenanceWindow{}, f.createErr
	}
	return f.createdRow, nil
}
func (f *fakeMaintenanceQuerier) UpdateMaintenanceWindow(_ context.Context, _ sqlc.UpdateMaintenanceWindowParams) (sqlc.MaintenanceWindow, error) {
	if f.updateErr != nil {
		return sqlc.MaintenanceWindow{}, f.updateErr
	}
	return f.updatedRow, nil
}
func (f *fakeMaintenanceQuerier) DeleteMaintenanceWindow(_ context.Context, _ uuid.UUID) error {
	return f.deleteErr
}
func (f *fakeMaintenanceQuerier) ListDeferredOperations(_ context.Context, _ sqlc.ListDeferredOperationsParams) ([]sqlc.DeferredOperation, error) {
	return f.deferred, nil
}
func (f *fakeMaintenanceQuerier) GetDeferredOperation(_ context.Context, _ uuid.UUID) (sqlc.DeferredOperation, error) {
	if f.getDeferredErr != nil {
		return sqlc.DeferredOperation{}, f.getDeferredErr
	}
	return f.getDeferredRow, nil
}
func (f *fakeMaintenanceQuerier) MarkDeferredCancelled(_ context.Context, _ sqlc.MarkDeferredCancelledParams) error {
	f.cancelCalled++
	return nil
}
func (f *fakeMaintenanceQuerier) CountDeferredOperations(_ context.Context) (int64, error) {
	return f.deferredCount, nil
}
func (f *fakeMaintenanceQuerier) CreateAuditLogV1(_ context.Context, _ sqlc.CreateAuditLogV1Params) error {
	f.auditCalls++
	return nil
}

// makeMaintenanceRequest mirrors the makeRequest helper in admin_drill_test.go
// but with a configurable method + body so the CRUD endpoints can be
// exercised in one place.
func makeMaintenanceRequest(method, target string, callerID uuid.UUID, body any) *http.Request {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, target, &buf)
	ctx := middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{
		ID:         callerID.String(),
		AuthMethod: "jwt",
	})
	return req.WithContext(ctx)
}

func TestHandler_RequiresSuperuser(t *testing.T) {
	callerID := uuid.New()
	q := &fakeMaintenanceQuerier{user: sqlc.User{ID: callerID, IsSuperuser: false}}
	h := NewMaintenanceHandler(q, nil)
	w := httptest.NewRecorder()
	req := makeMaintenanceRequest(http.MethodGet, "/api/v1/admin/maintenance-windows/", callerID, nil)
	h.List(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", w.Code, w.Body.String())
	}
}

func TestWindow_CRUD(t *testing.T) {
	callerID := uuid.New()
	q := &fakeMaintenanceQuerier{
		user: sqlc.User{ID: callerID, IsSuperuser: true},
		createdRow: sqlc.MaintenanceWindow{
			ID:              uuid.New(),
			Name:            "weekday-business-hours",
			Mode:            "blackout",
			CronOpen:        "0 9 * * 1-5",
			DurationMinutes: 8 * 60,
			Timezone:        "UTC",
			OnBlock:         "refuse",
			Enabled:         true,
			ClusterSelector: []byte("{}"),
			OperationTypes:  []byte("[]"),
		},
	}
	h := NewMaintenanceHandler(q, nil)

	// Create.
	body := MaintenanceWindowRequest{
		Name:            "weekday-business-hours",
		Mode:            "blackout",
		CronOpen:        "0 9 * * 1-5",
		DurationMinutes: 480,
		Timezone:        "UTC",
		OnBlock:         "refuse",
	}
	w := httptest.NewRecorder()
	req := makeMaintenanceRequest(http.MethodPost, "/api/v1/admin/maintenance-windows/", callerID, body)
	h.Create(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("Create status = %d, want 201; body=%s", w.Code, w.Body.String())
	}
	if q.auditCalls != 1 {
		t.Fatalf("Create audit calls = %d, want 1", q.auditCalls)
	}

	// List.
	q.windows = []sqlc.MaintenanceWindow{q.createdRow}
	w = httptest.NewRecorder()
	req = makeMaintenanceRequest(http.MethodGet, "/api/v1/admin/maintenance-windows/", callerID, nil)
	h.List(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("List status = %d, want 200", w.Code)
	}

	// Get.
	w = httptest.NewRecorder()
	req = makeMaintenanceRequest(http.MethodGet, "/api/v1/admin/maintenance-windows/{id}/", callerID, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", q.createdRow.ID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	h.Get(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("Get status = %d, want 200; body=%s", w.Code, w.Body.String())
	}

	// Update.
	q.updatedRow = q.createdRow
	q.updatedRow.Description = "updated description"
	body.Description = "updated description"
	w = httptest.NewRecorder()
	req = makeMaintenanceRequest(http.MethodPut, "/api/v1/admin/maintenance-windows/{id}/", callerID, body)
	rctx = chi.NewRouteContext()
	rctx.URLParams.Add("id", q.createdRow.ID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	h.Update(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("Update status = %d, want 200; body=%s", w.Code, w.Body.String())
	}

	// Delete.
	w = httptest.NewRecorder()
	req = makeMaintenanceRequest(http.MethodDelete, "/api/v1/admin/maintenance-windows/{id}/", callerID, nil)
	rctx = chi.NewRouteContext()
	rctx.URLParams.Add("id", q.createdRow.ID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	h.Delete(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("Delete status = %d, want 204", w.Code)
	}
}

func TestHandler_Create_RejectsInvalidCron(t *testing.T) {
	callerID := uuid.New()
	q := &fakeMaintenanceQuerier{user: sqlc.User{ID: callerID, IsSuperuser: true}}
	h := NewMaintenanceHandler(q, nil)

	body := MaintenanceWindowRequest{
		Name:            "bad-cron",
		Mode:            "blackout",
		CronOpen:        "this is not a cron",
		DurationMinutes: 60,
		Timezone:        "UTC",
		OnBlock:         "refuse",
	}
	w := httptest.NewRecorder()
	req := makeMaintenanceRequest(http.MethodPost, "/api/v1/admin/maintenance-windows/", callerID, body)
	h.Create(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("Create with bad cron returned %d, want 400; body=%s", w.Code, w.Body.String())
	}
}

func TestHandler_Create_RejectsDuplicateName(t *testing.T) {
	callerID := uuid.New()
	q := &fakeMaintenanceQuerier{
		user:       sqlc.User{ID: callerID, IsSuperuser: true},
		nameExists: true,
	}
	h := NewMaintenanceHandler(q, nil)

	body := MaintenanceWindowRequest{
		Name:            "dup",
		Mode:            "blackout",
		CronOpen:        "0 9 * * 1-5",
		DurationMinutes: 60,
		Timezone:        "UTC",
		OnBlock:         "refuse",
	}
	w := httptest.NewRecorder()
	req := makeMaintenanceRequest(http.MethodPost, "/api/v1/admin/maintenance-windows/", callerID, body)
	h.Create(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("Create with duplicate name returned %d, want 409", w.Code)
	}
}

func TestHandler_CancelDeferred(t *testing.T) {
	callerID := uuid.New()
	rowID := uuid.New()
	q := &fakeMaintenanceQuerier{
		user: sqlc.User{ID: callerID, IsSuperuser: true},
		getDeferredRow: sqlc.DeferredOperation{
			ID:            rowID,
			OperationType: "cluster.delete",
			Status:        "pending",
		},
	}
	h := NewMaintenanceHandler(q, nil)
	w := httptest.NewRecorder()
	req := makeMaintenanceRequest(http.MethodPost, "/api/v1/admin/deferred-operations/{id}/cancel/", callerID, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", rowID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	h.CancelDeferred(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("CancelDeferred status = %d, want 204; body=%s", w.Code, w.Body.String())
	}
	if q.cancelCalled != 1 {
		t.Fatalf("cancel calls = %d, want 1", q.cancelCalled)
	}
}

func TestHandler_CancelDeferred_RejectsDispatched(t *testing.T) {
	callerID := uuid.New()
	rowID := uuid.New()
	q := &fakeMaintenanceQuerier{
		user: sqlc.User{ID: callerID, IsSuperuser: true},
		getDeferredRow: sqlc.DeferredOperation{
			ID:     rowID,
			Status: "dispatched",
		},
	}
	h := NewMaintenanceHandler(q, nil)
	w := httptest.NewRecorder()
	req := makeMaintenanceRequest(http.MethodPost, "/api/v1/admin/deferred-operations/{id}/cancel/", callerID, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", rowID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	h.CancelDeferred(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("CancelDeferred on dispatched op returned %d, want 409", w.Code)
	}
}

func TestHandler_CancelDeferred_NotFound(t *testing.T) {
	callerID := uuid.New()
	q := &fakeMaintenanceQuerier{
		user:           sqlc.User{ID: callerID, IsSuperuser: true},
		getDeferredErr: pgx.ErrNoRows,
	}
	h := NewMaintenanceHandler(q, nil)
	w := httptest.NewRecorder()
	req := makeMaintenanceRequest(http.MethodPost, "/api/v1/admin/deferred-operations/{id}/cancel/", callerID, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", uuid.New().String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	h.CancelDeferred(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("CancelDeferred not-found = %d, want 404", w.Code)
	}
}

// --- Gate behaviour tests --------------------------------------------

// gateMaintenanceQuerier is a small stub that also implements
// GatedOpQuerier so the gate's defer-path can insert rows.
type gateMaintenanceQuerier struct {
	createdRow sqlc.DeferredOperation
	createErr  error
	calls      int
	auditCalls int
}

func (g *gateMaintenanceQuerier) CreateDeferredOperation(_ context.Context, _ sqlc.CreateDeferredOperationParams) (sqlc.DeferredOperation, error) {
	g.calls++
	if g.createErr != nil {
		return sqlc.DeferredOperation{}, g.createErr
	}
	return g.createdRow, nil
}
func (g *gateMaintenanceQuerier) CreateAuditLogV1(_ context.Context, _ sqlc.CreateAuditLogV1Params) error {
	g.auditCalls++
	return nil
}

// gateEvalQuerier is a tiny WindowQuerier for the maintenance.Evaluator
// so the gate sees real windows during the test.
type gateEvalQuerier struct {
	rows []sqlc.MaintenanceWindow
}

func (g *gateEvalQuerier) ListEnabledMaintenanceWindows(_ context.Context) ([]sqlc.MaintenanceWindow, error) {
	return g.rows, nil
}

func TestRefuse_Returns409(t *testing.T) {
	// Window: blackout, refuse, currently active (cron 0 0 * * * + 24h)
	row := sqlc.MaintenanceWindow{
		ID:              uuid.New(),
		Name:            "always-on",
		Mode:            "blackout",
		CronOpen:        "0 0 * * *",
		DurationMinutes: 24 * 60,
		Timezone:        "UTC",
		OnBlock:         "refuse",
		Enabled:         true,
		ClusterSelector: []byte("{}"),
		OperationTypes:  []byte("[]"),
	}
	ev := maintenance.NewEvaluator(&gateEvalQuerier{rows: []sqlc.MaintenanceWindow{row}})
	q := &gateMaintenanceQuerier{}
	gate := NewMaintenanceGate(ev, q)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/clusters/abc/", nil)
	ctx := middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{
		ID: uuid.New().String(), AuthMethod: "jwt",
	})
	req = req.WithContext(ctx)
	blocked := EnforceMaintenanceWindow(w, req, gate, "cluster.delete", nil, pgtypeUUIDZero(), pgtypeUUIDZero())
	if !blocked {
		t.Fatalf("expected blocked=true inside refuse window")
	}
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", w.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	errMap, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("error map missing: %s", w.Body.String())
	}
	if errMap["code"] != "maintenance_window_active" {
		t.Fatalf("code = %v, want maintenance_window_active", errMap["code"])
	}
	if q.calls != 0 {
		t.Fatalf("expected refuse path NOT to insert deferred row, got %d", q.calls)
	}
}

func TestDefer_Returns202AndInserts(t *testing.T) {
	// Window: blackout, defer, currently active.
	row := sqlc.MaintenanceWindow{
		ID:              uuid.New(),
		Name:            "always-defer",
		Mode:            "blackout",
		CronOpen:        "0 0 * * *",
		DurationMinutes: 24 * 60,
		Timezone:        "UTC",
		OnBlock:         "defer",
		Enabled:         true,
		ClusterSelector: []byte("{}"),
		OperationTypes:  []byte("[]"),
	}
	ev := maintenance.NewEvaluator(&gateEvalQuerier{rows: []sqlc.MaintenanceWindow{row}})
	q := &gateMaintenanceQuerier{
		createdRow: sqlc.DeferredOperation{ID: uuid.New(), Status: "pending"},
	}
	gate := NewMaintenanceGate(ev, q)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/clusters/abc/", nil)
	ctx := middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{
		ID: uuid.New().String(), AuthMethod: "jwt",
	})
	req = req.WithContext(ctx)
	blocked := EnforceMaintenanceWindow(w, req, gate, "cluster.delete", nil, pgtypeUUIDZero(), pgtypeUUIDZero())
	if !blocked {
		t.Fatalf("expected blocked=true (deferred)")
	}
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", w.Code, w.Body.String())
	}
	if q.calls != 1 {
		t.Fatalf("expected one CreateDeferredOperation call, got %d", q.calls)
	}
}

func TestGate_NilSafe(t *testing.T) {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	if blocked := EnforceMaintenanceWindow(w, req, nil, "cluster.delete", nil, pgtypeUUIDZero(), pgtypeUUIDZero()); blocked {
		t.Fatalf("nil gate should return blocked=false")
	}
}

func TestGate_DeferDegrade_ToRefuseWhenQuerierMissing(t *testing.T) {
	row := sqlc.MaintenanceWindow{
		ID:              uuid.New(),
		Name:            "defer-no-queries",
		Mode:            "blackout",
		CronOpen:        "0 0 * * *",
		DurationMinutes: 24 * 60,
		Timezone:        "UTC",
		OnBlock:         "defer",
		Enabled:         true,
		ClusterSelector: []byte("{}"),
		OperationTypes:  []byte("[]"),
	}
	ev := maintenance.NewEvaluator(&gateEvalQuerier{rows: []sqlc.MaintenanceWindow{row}})
	gate := NewMaintenanceGate(ev, nil) // intentionally nil queries
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	ctx := middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{
		ID: uuid.New().String(), AuthMethod: "jwt",
	})
	req = req.WithContext(ctx)
	if blocked := EnforceMaintenanceWindow(w, req, gate, "cluster.delete", nil, pgtypeUUIDZero(), pgtypeUUIDZero()); !blocked {
		t.Fatalf("expected blocked=true")
	}
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 (degraded refuse), got %d", w.Code)
	}
}

func TestGate_DeferInsertFailure_FallsBackTo409(t *testing.T) {
	row := sqlc.MaintenanceWindow{
		ID:              uuid.New(),
		Name:            "broken-defer",
		Mode:            "blackout",
		CronOpen:        "0 0 * * *",
		DurationMinutes: 24 * 60,
		Timezone:        "UTC",
		OnBlock:         "defer",
		Enabled:         true,
		ClusterSelector: []byte("{}"),
		OperationTypes:  []byte("[]"),
	}
	ev := maintenance.NewEvaluator(&gateEvalQuerier{rows: []sqlc.MaintenanceWindow{row}})
	q := &gateMaintenanceQuerier{createErr: errors.New("disk full")}
	gate := NewMaintenanceGate(ev, q)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	ctx := middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{
		ID: uuid.New().String(), AuthMethod: "jwt",
	})
	req = req.WithContext(ctx)
	if blocked := EnforceMaintenanceWindow(w, req, gate, "cluster.delete", nil, pgtypeUUIDZero(), pgtypeUUIDZero()); !blocked {
		t.Fatalf("expected blocked=true")
	}
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 fallback, got %d", w.Code)
	}
}

// pgtypeUUIDZero builds an empty pgtype.UUID for tests that don't need
// a specific value.
func pgtypeUUIDZero() pgtype.UUID {
	return pgtype.UUID{}
}

var _ = time.Now // keep time imported for any extension that needs it
