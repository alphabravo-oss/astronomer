package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

// fakeDrillQuerier is the minimal AdminDrillQuerier the tests need.
// Each method either returns a pre-canned value or pgx.ErrNoRows when
// the test wants the "never run" branch.
type fakeDrillQuerier struct {
	user       sqlc.User
	userErr    error
	latest     sqlc.BackupDrillResult
	latestErr  error
	success    sqlc.BackupDrillResult
	successErr error
	history    []sqlc.BackupDrillResult
	historyErr error
	total      int64
	totalErr   error
	// CreateAuditLogV1 is invoked by recordAudit; satisfy the interface
	// so the audit best-effort path is a no-op in tests.
	auditCalls int
}

func (f *fakeDrillQuerier) GetUserByID(_ context.Context, _ uuid.UUID) (sqlc.User, error) {
	return f.user, f.userErr
}
func (f *fakeDrillQuerier) GetLatestBackupDrillResult(_ context.Context) (sqlc.BackupDrillResult, error) {
	return f.latest, f.latestErr
}
func (f *fakeDrillQuerier) GetLatestSuccessfulBackupDrillResult(_ context.Context) (sqlc.BackupDrillResult, error) {
	return f.success, f.successErr
}
func (f *fakeDrillQuerier) ListBackupDrillResults(_ context.Context, _ sqlc.ListBackupDrillResultsParams) ([]sqlc.BackupDrillResult, error) {
	return f.history, f.historyErr
}
func (f *fakeDrillQuerier) CountBackupDrillResults(_ context.Context) (int64, error) {
	return f.total, f.totalErr
}

// CreateAuditLogV1 makes the fake satisfy auditWriterV1 so recordAudit
// inside gate() doesn't no-op silently — we want the audit path covered
// by at least one assertion in the success test.
func (f *fakeDrillQuerier) CreateAuditLogV1(_ context.Context, _ sqlc.CreateAuditLogV1Params) error {
	f.auditCalls++
	return nil
}

// makeRequest is a tiny shorthand: builds a GET request with the given
// authenticated user injected via SetAuthenticatedUserForTest.
func makeRequest(target string, callerID uuid.UUID) *http.Request {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	ctx := middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{
		ID:         callerID.String(),
		AuthMethod: "jwt",
	})
	return req.WithContext(ctx)
}

func TestBackupDrillHandler_GetLatest(t *testing.T) {
	callerID := uuid.New()
	rowID := uuid.New()
	startedAt := time.Date(2026, 5, 5, 4, 0, 0, 0, time.UTC)
	finishedAt := startedAt.Add(2 * time.Minute)

	q := &fakeDrillQuerier{
		user: sqlc.User{ID: callerID, IsSuperuser: true},
		latest: sqlc.BackupDrillResult{
			ID:            rowID,
			StartedAt:     startedAt,
			FinishedAt:    pgtype.Timestamptz{Time: finishedAt, Valid: true},
			Status:        "success",
			BackupKey:     "astronomer-pg/release/daily/2026-05-05T03-00-00Z.pgcustom",
			SchemaVersion: pgtype.Int4{Int32: 39, Valid: true},
			CreatedAt:     finishedAt,
		},
		success: sqlc.BackupDrillResult{
			ID:            rowID,
			StartedAt:     startedAt,
			FinishedAt:    pgtype.Timestamptz{Time: finishedAt, Valid: true},
			Status:        "success",
			BackupKey:     "astronomer-pg/release/daily/2026-05-05T03-00-00Z.pgcustom",
			SchemaVersion: pgtype.Int4{Int32: 39, Valid: true},
			CreatedAt:     finishedAt,
		},
	}

	h := NewAdminDrillHandler(q)
	w := httptest.NewRecorder()
	req := makeRequest("/api/v1/admin/backup-drill/", callerID)

	h.GetLatest(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Body is wrapped in {"data": ...} by RespondJSON.
	dataRaw, ok := got["data"]
	if !ok {
		t.Fatalf("missing data: %s", w.Body.String())
	}
	var payload BackupDrillLatestResponse
	if err := json.Unmarshal(dataRaw, &payload); err != nil {
		t.Fatalf("decode data: %v", err)
	}
	if payload.Latest == nil {
		t.Fatalf("latest = nil, want populated")
	}
	if payload.Latest.Status != "success" {
		t.Fatalf("status = %q, want success", payload.Latest.Status)
	}
	if payload.Latest.SchemaVersion == nil || *payload.Latest.SchemaVersion != 39 {
		t.Fatalf("schema_version = %v, want 39", payload.Latest.SchemaVersion)
	}
	if payload.LatestSuccessAgeSeconds == nil {
		t.Fatalf("latest_success_age_seconds = nil, want populated")
	}
	if *payload.LatestSuccessAgeSeconds <= 0 {
		t.Fatalf("latest_success_age_seconds = %f, want > 0", *payload.LatestSuccessAgeSeconds)
	}
	if q.auditCalls != 1 {
		t.Fatalf("audit calls = %d, want 1", q.auditCalls)
	}
}

func TestBackupDrillHandler_GetLatest_NoRows(t *testing.T) {
	callerID := uuid.New()
	q := &fakeDrillQuerier{
		user:       sqlc.User{ID: callerID, IsSuperuser: true},
		latestErr:  pgx.ErrNoRows,
		successErr: pgx.ErrNoRows,
	}
	h := NewAdminDrillHandler(q)
	w := httptest.NewRecorder()
	req := makeRequest("/api/v1/admin/backup-drill/", callerID)

	h.GetLatest(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (no-rows is a valid state)", w.Code)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var payload BackupDrillLatestResponse
	if err := json.Unmarshal(got["data"], &payload); err != nil {
		t.Fatalf("decode data: %v", err)
	}
	if payload.Latest != nil {
		t.Fatalf("latest = %+v, want nil", payload.Latest)
	}
	if payload.LatestSuccessAgeSeconds != nil {
		t.Fatalf("age = %v, want nil", payload.LatestSuccessAgeSeconds)
	}
}

func TestBackupDrillHandler_ListHistory(t *testing.T) {
	callerID := uuid.New()
	a := sqlc.BackupDrillResult{
		ID:        uuid.New(),
		StartedAt: time.Now().Add(-7 * 24 * time.Hour),
		Status:    "success",
		BackupKey: "astronomer-pg/release/daily/old.pgcustom",
	}
	b := sqlc.BackupDrillResult{
		ID:           uuid.New(),
		StartedAt:    time.Now().Add(-24 * time.Hour),
		Status:       "failure",
		BackupKey:    "astronomer-pg/release/daily/new.pgcustom",
		ErrorMessage: "psql: connection refused",
	}
	q := &fakeDrillQuerier{
		user:    sqlc.User{ID: callerID, IsSuperuser: true},
		history: []sqlc.BackupDrillResult{b, a},
		total:   2,
	}
	h := NewAdminDrillHandler(q)
	w := httptest.NewRecorder()
	req := makeRequest("/api/v1/admin/backup-drill/history/?limit=10", callerID)

	h.ListHistory(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var payload struct {
		Data  []BackupDrillResult `json:"data"`
		Count int64               `json:"count"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Count != 2 {
		t.Fatalf("count = %d, want 2", payload.Count)
	}
	if len(payload.Data) != 2 {
		t.Fatalf("len(data) = %d, want 2", len(payload.Data))
	}
	if payload.Data[0].Status != "failure" {
		t.Fatalf("data[0].status = %q, want failure (newest first)", payload.Data[0].Status)
	}
}

func TestBackupDrillHandler_RequiresSuperuser(t *testing.T) {
	callerID := uuid.New()
	q := &fakeDrillQuerier{
		user: sqlc.User{ID: callerID, IsSuperuser: false},
	}
	h := NewAdminDrillHandler(q)

	// Both endpoints must reject non-superusers with 403.
	cases := []struct {
		name string
		path string
		call func(w http.ResponseWriter, r *http.Request)
	}{
		{"GetLatest", "/api/v1/admin/backup-drill/", h.GetLatest},
		{"ListHistory", "/api/v1/admin/backup-drill/history/", h.ListHistory},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := makeRequest(tc.path, callerID)
			tc.call(w, req)
			if w.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403; body=%s", w.Code, w.Body.String())
			}
		})
	}

	// And anon callers (no context user) must hit 401, not 403 — that's
	// the contract the rest of the admin endpoints share.
	t.Run("Anonymous", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/backup-drill/", nil)
		h.GetLatest(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", w.Code)
		}
	})
}

func TestBackupDrillHandler_DBError(t *testing.T) {
	callerID := uuid.New()
	q := &fakeDrillQuerier{
		user:      sqlc.User{ID: callerID, IsSuperuser: true},
		latestErr: errors.New("connection refused"),
	}
	h := NewAdminDrillHandler(q)
	w := httptest.NewRecorder()
	req := makeRequest("/api/v1/admin/backup-drill/", callerID)

	h.GetLatest(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
}
