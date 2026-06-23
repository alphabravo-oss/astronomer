package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

type fakeAdminTaskOutboxQuerier struct {
	users   map[uuid.UUID]sqlc.User
	rows    []sqlc.TaskOutbox
	retried []sqlc.RetryTaskOutboxParams
	audits  int
}

func (f *fakeAdminTaskOutboxQuerier) GetUserByID(_ context.Context, id uuid.UUID) (sqlc.User, error) {
	if f.users == nil {
		return sqlc.User{}, pgx.ErrNoRows
	}
	user, ok := f.users[id]
	if !ok {
		return sqlc.User{}, pgx.ErrNoRows
	}
	return user, nil
}

func (f *fakeAdminTaskOutboxQuerier) CreateAuditLogV1(context.Context, sqlc.CreateAuditLogV1Params) error {
	f.audits++
	return nil
}

func (f *fakeAdminTaskOutboxQuerier) ListTaskOutbox(_ context.Context, arg sqlc.ListTaskOutboxParams) ([]sqlc.TaskOutbox, error) {
	out := []sqlc.TaskOutbox{}
	for _, row := range f.rows {
		if arg.Status == "" || row.Status == arg.Status {
			out = append(out, row)
		}
	}
	return out, nil
}

func (f *fakeAdminTaskOutboxQuerier) CountTaskOutbox(_ context.Context, status string) (int64, error) {
	var count int64
	for _, row := range f.rows {
		if status == "" || row.Status == status {
			count++
		}
	}
	return count, nil
}

func (f *fakeAdminTaskOutboxQuerier) GetTaskOutbox(_ context.Context, id uuid.UUID) (sqlc.TaskOutbox, error) {
	for _, row := range f.rows {
		if row.ID == id {
			return row, nil
		}
	}
	return sqlc.TaskOutbox{}, pgx.ErrNoRows
}

func (f *fakeAdminTaskOutboxQuerier) RetryTaskOutbox(_ context.Context, arg sqlc.RetryTaskOutboxParams) (sqlc.TaskOutbox, error) {
	f.retried = append(f.retried, arg)
	for i, row := range f.rows {
		if row.ID == arg.ID {
			f.rows[i].Status = "pending"
			if arg.NextAttemptAt.Valid {
				f.rows[i].NextAttemptAt = arg.NextAttemptAt.Time
			}
			f.rows[i].LastError = ""
			return f.rows[i], nil
		}
	}
	return sqlc.TaskOutbox{}, pgx.ErrNoRows
}

func makeAdminTaskOutboxRequest(method, path string, callerID uuid.UUID) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	ctx := middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{
		ID:    callerID.String(),
		Email: "admin@example.com",
	})
	return req.WithContext(ctx)
}

func adminTaskOutboxRouter(h *AdminTaskOutboxHandler) *chi.Mux {
	r := chi.NewRouter()
	r.Get("/api/v1/admin/task-outbox/", h.List)
	r.Get("/api/v1/admin/task-outbox/dead/", h.ListDead)
	r.Post("/api/v1/admin/task-outbox/{id}/retry/", h.Retry)
	return r
}

func makeTaskOutboxRow(status string) sqlc.TaskOutbox {
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	return sqlc.TaskOutbox{
		ID:                  uuid.New(),
		TaskType:            "argocd:auto_register_cluster",
		Payload:             []byte(`{"cluster_id":"one"}`),
		QueueName:           "default",
		MaxRetry:            5,
		MaxDeliveryAttempts: 20,
		Status:              status,
		AttemptCount:        3,
		NextAttemptAt:       now,
		LastError:           "redis down",
		CreatedAt:           now.Add(-time.Minute),
		UpdatedAt:           now,
	}
}

func TestAdminTaskOutboxListFiltersAndHidesPayload(t *testing.T) {
	adminID := uuid.New()
	q := &fakeAdminTaskOutboxQuerier{
		users: map[uuid.UUID]sqlc.User{adminID: {ID: adminID, IsSuperuser: true}},
		rows:  []sqlc.TaskOutbox{makeTaskOutboxRow("dead"), makeTaskOutboxRow("pending")},
	}
	h := NewAdminTaskOutboxHandler(q)
	router := adminTaskOutboxRouter(h)

	req := makeAdminTaskOutboxRequest(http.MethodGet, "/api/v1/admin/task-outbox/?status=dead", adminID)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var body struct {
		Data  []map[string]any `json:"data"`
		Count int64            `json:"count"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Count != 1 || len(body.Data) != 1 {
		t.Fatalf("count/data = %d/%d, want 1/1", body.Count, len(body.Data))
	}
	if _, ok := body.Data[0]["payload"]; ok {
		t.Fatalf("task outbox list must not expose raw payload")
	}
	if got := body.Data[0]["payload_size"].(float64); got == 0 {
		t.Fatalf("payload_size should be set")
	}
}

func TestAdminTaskOutboxListDeadReturnsOnlyDead(t *testing.T) {
	adminID := uuid.New()
	q := &fakeAdminTaskOutboxQuerier{
		users: map[uuid.UUID]sqlc.User{adminID: {ID: adminID, IsSuperuser: true}},
		rows:  []sqlc.TaskOutbox{makeTaskOutboxRow("dead"), makeTaskOutboxRow("pending"), makeTaskOutboxRow("failed")},
	}
	h := NewAdminTaskOutboxHandler(q)
	router := adminTaskOutboxRouter(h)

	req := makeAdminTaskOutboxRequest(http.MethodGet, "/api/v1/admin/task-outbox/dead/", adminID)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var body struct {
		Data  []map[string]any `json:"data"`
		Count int64            `json:"count"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Count != 1 || len(body.Data) != 1 {
		t.Fatalf("count/data = %d/%d, want 1/1", body.Count, len(body.Data))
	}
	if got := body.Data[0]["status"].(string); got != "dead" {
		t.Fatalf("status = %q, want dead", got)
	}
}

func TestAdminTaskOutboxRejectsNonSuperuser(t *testing.T) {
	userID := uuid.New()
	q := &fakeAdminTaskOutboxQuerier{
		users: map[uuid.UUID]sqlc.User{userID: {ID: userID, IsSuperuser: false}},
	}
	h := NewAdminTaskOutboxHandler(q)
	router := adminTaskOutboxRouter(h)

	req := makeAdminTaskOutboxRequest(http.MethodGet, "/api/v1/admin/task-outbox/", userID)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
}

func TestAdminTaskOutboxRetryMovesRowPending(t *testing.T) {
	adminID := uuid.New()
	row := makeTaskOutboxRow("dead")
	q := &fakeAdminTaskOutboxQuerier{
		users: map[uuid.UUID]sqlc.User{adminID: {ID: adminID, IsSuperuser: true}},
		rows:  []sqlc.TaskOutbox{row},
	}
	h := NewAdminTaskOutboxHandler(q)
	h.now = func() time.Time { return time.Date(2026, 6, 12, 12, 5, 0, 0, time.UTC) }
	router := adminTaskOutboxRouter(h)

	req := makeAdminTaskOutboxRequest(http.MethodPost, "/api/v1/admin/task-outbox/"+row.ID.String()+"/retry/", adminID)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	if len(q.retried) != 1 || q.retried[0].ID != row.ID {
		t.Fatalf("retried = %+v", q.retried)
	}
	if q.audits != 1 {
		t.Fatalf("audits = %d, want 1", q.audits)
	}
}

func TestAdminTaskOutboxRetryRejectsDelivered(t *testing.T) {
	adminID := uuid.New()
	row := makeTaskOutboxRow("delivered")
	q := &fakeAdminTaskOutboxQuerier{
		users: map[uuid.UUID]sqlc.User{adminID: {ID: adminID, IsSuperuser: true}},
		rows:  []sqlc.TaskOutbox{row},
	}
	h := NewAdminTaskOutboxHandler(q)
	router := adminTaskOutboxRouter(h)

	req := makeAdminTaskOutboxRequest(http.MethodPost, "/api/v1/admin/task-outbox/"+row.ID.String()+"/retry/", adminID)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", w.Code)
	}
}
