package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

func fakeAdminTaskOutboxRunTx(q *fakeAdminTaskOutboxQuerier) adminTaskOutboxRunTxFunc {
	return func(_ context.Context, fn func(AdminTaskOutboxMutationTx) error) error {
		q.mutationMu.Lock()
		defer q.mutationMu.Unlock()
		rows := append([]sqlc.TaskOutbox(nil), q.rows...)
		retried := append([]sqlc.RetryTaskOutboxParams(nil), q.retried...)
		audits := append([]sqlc.UpsertAuditOutboxParams(nil), q.auditRows...)
		if err := fn(q); err != nil {
			q.rows, q.retried, q.auditRows = rows, retried, audits
			return err
		}
		return nil
	}
}

func TestAdminTaskOutboxRetryReplaysExactReceiptOnceUnderRace(t *testing.T) {
	h, q, adminID, row := transactionalAdminTaskOutboxFixture("dead", nil)
	responses := make([]*httptest.ResponseRecorder, 2)
	var wg sync.WaitGroup
	for i := range responses {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			response := httptest.NewRecorder()
			adminTaskOutboxRouter(h).ServeHTTP(response, makeAdminTaskOutboxRequest(
				http.MethodPost, "/api/v1/admin/task-outbox/"+row.ID.String()+"/retry/", adminID,
			))
			responses[index] = response
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
	if q.rowLocks != 1 || len(q.retried) != 1 || len(q.auditRows) != 1 {
		t.Fatalf("racing replay locks/retries/audits=%d/%d/%d, want 1/1/1", q.rowLocks, len(q.retried), len(q.auditRows))
	}
}

func TestAdminTaskOutboxRetryKeyRejectsChangedTarget(t *testing.T) {
	h, q, adminID, firstRow := transactionalAdminTaskOutboxFixture("dead", nil)
	secondRow := makeTaskOutboxRow("dead")
	q.rows = append(q.rows, secondRow)
	router := adminTaskOutboxRouter(h)
	request := func(id uuid.UUID) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, makeAdminTaskOutboxRequest(http.MethodPost, "/api/v1/admin/task-outbox/"+id.String()+"/retry/", adminID))
		return response
	}
	first, changed := request(firstRow.ID), request(secondRow.ID)
	if first.Code != http.StatusAccepted || changed.Code != http.StatusConflict {
		t.Fatalf("first/changed status=%d/%d changed=%s", first.Code, changed.Code, changed.Body.String())
	}
	if len(q.retried) != 1 || len(q.auditRows) != 1 {
		t.Fatalf("changed target retries/audits=%d/%d", len(q.retried), len(q.auditRows))
	}
}

func transactionalAdminTaskOutboxFixture(status string, auditErr error) (*AdminTaskOutboxHandler, *fakeAdminTaskOutboxQuerier, uuid.UUID, sqlc.TaskOutbox) {
	adminID := uuid.New()
	row := makeTaskOutboxRow(status)
	q := &fakeAdminTaskOutboxQuerier{
		users:    map[uuid.UUID]sqlc.User{adminID: {ID: adminID, IsSuperuser: true}},
		rows:     []sqlc.TaskOutbox{row},
		auditErr: auditErr,
	}
	h := NewAdminTaskOutboxHandler(q)
	h.SetRunTx(fakeAdminTaskOutboxRunTx(q))
	return h, q, adminID, row
}

func TestAdminTaskOutboxRetryCommitsStateAndMandatoryAudit(t *testing.T) {
	h, q, adminID, row := transactionalAdminTaskOutboxFixture("dead", nil)
	w := httptest.NewRecorder()
	adminTaskOutboxRouter(h).ServeHTTP(w, makeAdminTaskOutboxRequest(
		http.MethodPost, "/api/v1/admin/task-outbox/"+row.ID.String()+"/retry/", adminID,
	))

	if w.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if q.rowLocks != 1 || len(q.retried) != 1 || len(q.auditRows) != 1 {
		t.Fatalf("locks=%d retries=%d audits=%d, want 1/1/1", q.rowLocks, len(q.retried), len(q.auditRows))
	}
	if q.rows[0].Status != "pending" || q.rows[0].AttemptCount != 0 || q.rows[0].LastError != "" {
		t.Fatalf("retried row=%+v", q.rows[0])
	}
	if q.auditRows[0].Action != "admin.task_outbox.retry" || strings.Contains(string(q.auditRows[0].Detail), "redis down") {
		t.Fatalf("audit row=%+v", q.auditRows[0])
	}
}

func TestAdminTaskOutboxRetryAuditFailureRollsBackState(t *testing.T) {
	h, q, adminID, row := transactionalAdminTaskOutboxFixture("dead", errors.New("audit-SENTINEL"))
	w := httptest.NewRecorder()
	adminTaskOutboxRouter(h).ServeHTTP(w, makeAdminTaskOutboxRequest(
		http.MethodPost, "/api/v1/admin/task-outbox/"+row.ID.String()+"/retry/", adminID,
	))

	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "audit_unavailable") || strings.Contains(w.Body.String(), "audit-SENTINEL") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if q.rows[0].Status != "dead" || q.rows[0].AttemptCount != row.AttemptCount || q.rows[0].LastError != row.LastError {
		t.Fatalf("rollback retained mutation: before=%+v after=%+v", row, q.rows[0])
	}
	if q.rowLocks != 1 || len(q.retried) != 0 || len(q.auditRows) != 0 {
		t.Fatalf("locks=%d retries=%d audits=%d, want 1/0/0", q.rowLocks, len(q.retried), len(q.auditRows))
	}
}

func TestAdminTaskOutboxRetryLocksAndRejectsDeliveredRow(t *testing.T) {
	h, q, adminID, row := transactionalAdminTaskOutboxFixture("delivered", nil)
	w := httptest.NewRecorder()
	adminTaskOutboxRouter(h).ServeHTTP(w, makeAdminTaskOutboxRequest(
		http.MethodPost, "/api/v1/admin/task-outbox/"+row.ID.String()+"/retry/", adminID,
	))

	if w.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if q.rowLocks != 1 || len(q.retried) != 0 || len(q.auditRows) != 0 || q.rows[0].Status != "delivered" {
		t.Fatalf("locks=%d retries=%d audits=%d row=%+v", q.rowLocks, len(q.retried), len(q.auditRows), q.rows[0])
	}
}
