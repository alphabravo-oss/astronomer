package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	appmiddleware "github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

type adminQueuesTestQueries struct {
	user      sqlc.User
	operation sqlc.AdminQueueOperation
	audits    []sqlc.CreateAuditLogV1Params
}

func (q *adminQueuesTestQueries) GetUserByID(context.Context, uuid.UUID) (sqlc.User, error) {
	return q.user, nil
}
func (q *adminQueuesTestQueries) CreateAuditLogV1(_ context.Context, arg sqlc.CreateAuditLogV1Params) error {
	q.audits = append(q.audits, arg)
	return nil
}
func (q *adminQueuesTestQueries) GetAdminQueueOperation(context.Context, uuid.UUID) (sqlc.AdminQueueOperation, error) {
	return q.operation, nil
}

type adminQueuesTestInspector struct {
	runs    int
	deletes int
}

func (*adminQueuesTestInspector) Queues() ([]string, error) { return nil, nil }
func (*adminQueuesTestInspector) GetQueueInfo(string) (*asynq.QueueInfo, error) {
	return &asynq.QueueInfo{}, nil
}
func (*adminQueuesTestInspector) ListArchivedTasks(string, ...asynq.ListOption) ([]*asynq.TaskInfo, error) {
	return nil, nil
}
func (i *adminQueuesTestInspector) RunTask(string, string) error {
	i.runs++
	return nil
}
func (i *adminQueuesTestInspector) DeleteTask(string, string) error {
	i.deletes++
	return nil
}

type adminQueueMutationTestTx struct {
	fakeOperationIdempotencyStore
	operation sqlc.AdminQueueOperation
	createErr error
	taskErr   error
	auditErr  error
	tasks     []sqlc.UpsertTaskOutboxParams
	audits    []sqlc.UpsertAuditOutboxParams
}

func (tx *adminQueueMutationTestTx) CreateAdminQueueOperation(_ context.Context, arg sqlc.CreateAdminQueueOperationParams) (sqlc.AdminQueueOperation, error) {
	if tx.createErr != nil {
		return sqlc.AdminQueueOperation{}, tx.createErr
	}
	row := tx.operation
	if row.ID == uuid.Nil {
		row = sqlc.AdminQueueOperation{
			ID: uuid.New(), Action: arg.Action, QueueName: arg.QueueName, TaskID: arg.TaskID,
			Status: "pending", RequestedBy: arg.RequestedBy, CreatedAt: time.Now(), UpdatedAt: time.Now(),
		}
	}
	return row, nil
}
func (tx *adminQueueMutationTestTx) UpsertTaskOutbox(_ context.Context, arg sqlc.UpsertTaskOutboxParams) (sqlc.TaskOutbox, error) {
	if tx.taskErr != nil {
		return sqlc.TaskOutbox{}, tx.taskErr
	}
	tx.tasks = append(tx.tasks, arg)
	return sqlc.TaskOutbox{ID: uuid.New()}, nil
}
func (tx *adminQueueMutationTestTx) UpsertAuditOutbox(_ context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	if tx.auditErr != nil {
		return sqlc.AuditOutbox{}, tx.auditErr
	}
	tx.audits = append(tx.audits, arg)
	return sqlc.AuditOutbox{ID: uuid.New()}, nil
}

func adminQueueMutationRequest(t *testing.T, h *AdminQueuesHandler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	router := chi.NewRouter()
	router.Post("/admin/queues/{queue}/dlq/{id}/retry/", h.RetryDLQ)
	router.Delete("/admin/queues/{queue}/dlq/{id}/", h.DiscardDLQ)
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("Idempotency-Key", "admin-queue-test")
	uid := h.queries.(*adminQueuesTestQueries).user.ID
	ctx := appmiddleware.SetAuthenticatedUserForTest(req.Context(), &appmiddleware.AuthenticatedUser{ID: uid.String(), AuthMethod: "jwt"})
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func newAdminQueueMutationHandler(tx *adminQueueMutationTestTx) (*AdminQueuesHandler, *adminQueuesTestInspector, *bool) {
	uid := uuid.New()
	queries := &adminQueuesTestQueries{user: sqlc.User{ID: uid, IsActive: true, IsSuperuser: true}}
	inspector := &adminQueuesTestInspector{}
	h := NewAdminQueuesHandler(inspector, queries)
	committed := false
	var transactionMu sync.Mutex
	h.SetRunTx(func(ctx context.Context, fn func(AdminQueueMutationTx) error) error {
		transactionMu.Lock()
		defer transactionMu.Unlock()
		if err := fn(tx); err != nil {
			return err
		}
		committed = true
		return nil
	})
	return h, inspector, &committed
}

func TestAdminQueueRetryAndDiscardReplayExactReceiptOnceUnderRace(t *testing.T) {
	for _, tc := range []struct {
		name, method, path string
	}{
		{name: "retry", method: http.MethodPost, path: "/admin/queues/critical/dlq/task-123/retry/"},
		{name: "discard", method: http.MethodDelete, path: "/admin/queues/critical/dlq/task-123/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := &adminQueueMutationTestTx{}
			h, _, _ := newAdminQueueMutationHandler(tx)
			responses := make([]*httptest.ResponseRecorder, 2)
			var wg sync.WaitGroup
			for i := range responses {
				wg.Add(1)
				go func(index int) {
					defer wg.Done()
					responses[index] = adminQueueMutationRequest(t, h, tc.method, tc.path)
				}(i)
			}
			wg.Wait()
			for _, response := range responses {
				if response.Code != http.StatusAccepted {
					t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
				}
			}
			if responses[0].Body.String() != responses[1].Body.String() || responses[0].Header().Get("Location") != responses[1].Header().Get("Location") {
				t.Fatalf("replay receipt changed: first=%s second=%s", responses[0].Body.String(), responses[1].Body.String())
			}
			if len(tx.tasks) != 1 || len(tx.audits) != 1 {
				t.Fatalf("racing replay created tasks/audits=%d/%d, want 1/1", len(tx.tasks), len(tx.audits))
			}
		})
	}
}

func TestAdminQueueReplayKeyRejectsChangedTarget(t *testing.T) {
	tx := &adminQueueMutationTestTx{}
	h, _, _ := newAdminQueueMutationHandler(tx)
	first := adminQueueMutationRequest(t, h, http.MethodPost, "/admin/queues/critical/dlq/task-123/retry/")
	changed := adminQueueMutationRequest(t, h, http.MethodPost, "/admin/queues/critical/dlq/task-456/retry/")
	if first.Code != http.StatusAccepted || changed.Code != http.StatusConflict {
		t.Fatalf("first/changed status=%d/%d changed=%s", first.Code, changed.Code, changed.Body.String())
	}
	if len(tx.tasks) != 1 || len(tx.audits) != 1 {
		t.Fatalf("changed target created tasks/audits=%d/%d", len(tx.tasks), len(tx.audits))
	}
}

func TestAdminQueueRetryCommitsReceiptTaskAndAuditBefore202(t *testing.T) {
	tx := &adminQueueMutationTestTx{}
	h, inspector, committed := newAdminQueueMutationHandler(tx)
	rec := adminQueueMutationRequest(t, h, http.MethodPost, "/admin/queues/critical/dlq/task-123/retry/")
	if rec.Code != http.StatusAccepted || !*committed {
		t.Fatalf("status=%d committed=%v body=%s", rec.Code, *committed, rec.Body.String())
	}
	if !strings.Contains(rec.Header().Get("Location"), "/admin/queues/operations/") || rec.Header().Get("Retry-After") != "2" {
		t.Fatalf("receipt headers location=%q retry-after=%q", rec.Header().Get("Location"), rec.Header().Get("Retry-After"))
	}
	if inspector.runs != 0 || inspector.deletes != 0 {
		t.Fatalf("HTTP handler mutated Redis: runs=%d deletes=%d", inspector.runs, inspector.deletes)
	}
	if len(tx.tasks) != 1 || len(tx.audits) != 1 {
		t.Fatalf("tasks=%d audits=%d", len(tx.tasks), len(tx.audits))
	}
	var taskPayload map[string]any
	if err := json.Unmarshal(tx.tasks[0].Payload, &taskPayload); err != nil {
		t.Fatal(err)
	}
	if len(taskPayload) != 1 || taskPayload["operation_id"] == "" {
		t.Fatalf("task payload = %#v", taskPayload)
	}
	if strings.Contains(string(tx.audits[0].Detail), "last_err") || strings.Contains(string(tx.audits[0].Detail), "payload") {
		t.Fatalf("audit detail contains sensitive task fields: %s", tx.audits[0].Detail)
	}
	var envelope struct {
		Data AdminQueueOperationResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	body := envelope.Data
	if body.Status != "pending" || body.Action != "retry" || body.TaskID != "task-123" {
		t.Fatalf("receipt = %+v", body)
	}
}

func TestAdminQueueRetryVsDiscardConflictReturns409WithoutTaskOrAudit(t *testing.T) {
	tx := &adminQueueMutationTestTx{operation: sqlc.AdminQueueOperation{
		ID: uuid.New(), Action: "retry", QueueName: "critical", TaskID: "task-123", Status: "running",
	}}
	h, inspector, committed := newAdminQueueMutationHandler(tx)
	rec := adminQueueMutationRequest(t, h, http.MethodDelete, "/admin/queues/critical/dlq/task-123/")
	if rec.Code != http.StatusConflict || *committed {
		t.Fatalf("status=%d committed=%v body=%s", rec.Code, *committed, rec.Body.String())
	}
	if len(tx.tasks) != 0 || len(tx.audits) != 0 || inspector.deletes != 0 {
		t.Fatalf("conflict produced effects: tasks=%d audits=%d deletes=%d", len(tx.tasks), len(tx.audits), inspector.deletes)
	}
}

func TestAdminQueueFailedOperationCanBeDurablyRequeued(t *testing.T) {
	operationID := uuid.New()
	tx := &adminQueueMutationTestTx{operation: sqlc.AdminQueueOperation{
		ID: operationID, Action: "retry", QueueName: "critical", TaskID: "task-123", Status: "failed",
	}}
	h, inspector, committed := newAdminQueueMutationHandler(tx)
	rec := adminQueueMutationRequest(t, h, http.MethodPost, "/admin/queues/critical/dlq/task-123/retry/")
	if rec.Code != http.StatusAccepted || !*committed {
		t.Fatalf("status=%d committed=%v body=%s", rec.Code, *committed, rec.Body.String())
	}
	if len(tx.tasks) != 1 || !tx.tasks[0].DedupeKey.Valid {
		t.Fatalf("manual recovery must create an idempotency-fenced task intent, got %+v", tx.tasks)
	}
	if !strings.Contains(string(tx.tasks[0].Payload), operationID.String()) {
		t.Fatalf("recovery task does not target existing operation: %s", tx.tasks[0].Payload)
	}
	if inspector.runs != 0 {
		t.Fatal("manual recovery HTTP path touched Redis")
	}
}

func TestAdminQueueMutationRollsBackWhenAuditIntentFails(t *testing.T) {
	tx := &adminQueueMutationTestTx{auditErr: errors.New("postgres unavailable")}
	h, inspector, committed := newAdminQueueMutationHandler(tx)
	rec := adminQueueMutationRequest(t, h, http.MethodPost, "/admin/queues/critical/dlq/task-123/retry/")
	if rec.Code != http.StatusServiceUnavailable || *committed {
		t.Fatalf("status=%d committed=%v body=%s", rec.Code, *committed, rec.Body.String())
	}
	if inspector.runs != 0 || inspector.deletes != 0 {
		t.Fatal("audit rollback path touched Redis")
	}
}

func TestAdminQueueMutationFailsClosedWithoutTransactionRunner(t *testing.T) {
	uid := uuid.New()
	queries := &adminQueuesTestQueries{user: sqlc.User{ID: uid, IsActive: true, IsSuperuser: true}}
	inspector := &adminQueuesTestInspector{}
	h := NewAdminQueuesHandler(inspector, queries)
	rec := adminQueueMutationRequest(t, h, http.MethodDelete, "/admin/queues/critical/dlq/task-123/")
	if rec.Code != http.StatusServiceUnavailable || inspector.deletes != 0 {
		t.Fatalf("status=%d deletes=%d body=%s", rec.Code, inspector.deletes, rec.Body.String())
	}
}
