package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/events"
)

type transactionalAllowlistQ struct {
	*fakeAllowlistQuerier
	tasks     []sqlc.UpsertTaskOutboxParams
	auditRows []sqlc.UpsertAuditOutboxParams
	taskErr   error
	auditErr  error
	rowLocks  int
}

func (q *transactionalAllowlistQ) GetApiserverAllowlistForUpdate(ctx context.Context, clusterID uuid.UUID) (sqlc.ApiserverAllowlist, error) {
	q.rowLocks++
	if q.row == nil {
		return sqlc.ApiserverAllowlist{}, pgx.ErrNoRows
	}
	return q.GetApiserverAllowlistByClusterID(ctx, clusterID)
}

func (q *transactionalAllowlistQ) UpsertTaskOutbox(_ context.Context, arg sqlc.UpsertTaskOutboxParams) (sqlc.TaskOutbox, error) {
	if q.taskErr != nil {
		return sqlc.TaskOutbox{}, q.taskErr
	}
	q.tasks = append(q.tasks, arg)
	return sqlc.TaskOutbox{ID: uuid.New(), TaskType: arg.TaskType, Payload: arg.Payload}, nil
}

func (q *transactionalAllowlistQ) UpsertAuditOutbox(_ context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	if q.auditErr != nil {
		return sqlc.AuditOutbox{}, q.auditErr
	}
	q.auditRows = append(q.auditRows, arg)
	return sqlc.AuditOutbox{ID: arg.ID, Action: arg.Action, Detail: arg.Detail}, nil
}

func fakeAllowlistRunTx(q *transactionalAllowlistQ) apiserverAllowlistRunTxFunc {
	return func(_ context.Context, fn func(ApiserverAllowlistMutationTx) error) error {
		var row *sqlc.ApiserverAllowlist
		if q.row != nil {
			copy := *q.row
			row = &copy
		}
		upserted := q.upserted
		tasks := append([]sqlc.UpsertTaskOutboxParams(nil), q.tasks...)
		audits := append([]sqlc.UpsertAuditOutboxParams(nil), q.auditRows...)
		if err := fn(q); err != nil {
			q.row, q.upserted, q.tasks, q.auditRows = row, upserted, tasks, audits
			return err
		}
		return nil
	}
}

func transactionalAllowlistHandler(q *transactionalAllowlistQ) *ApiserverAllowlistHandler {
	h := NewApiserverAllowlistHandler(q)
	h.SetTaskBuilder(func(clusterID uuid.UUID) (*asynq.Task, error) {
		payload, _ := json.Marshal(map[string]string{"cluster_id": clusterID.String()})
		return asynq.NewTask("apiserver_allowlist:reconcile", payload), nil
	})
	h.SetRunTx(fakeAllowlistRunTx(q))
	return h
}

func allowlistUpdateRequest(clusterID uuid.UUID) *http.Request {
	body, _ := json.Marshal(AllowlistUpdateRequest{CIDRs: []string{"10.0.0.0/8"}, Mode: "monitor"})
	return httptest.NewRequest(http.MethodPut, "/clusters/"+clusterID.String()+"/apiserver-allowlist/", bytes.NewReader(body))
}

func TestApiserverAllowlistUpdateCommitsDesiredStateTaskAndAudit(t *testing.T) {
	clusterID := uuid.New()
	q := &transactionalAllowlistQ{fakeAllowlistQuerier: &fakeAllowlistQuerier{cluster: sqlc.Cluster{ID: clusterID, Provider: "eks"}}}
	h := transactionalAllowlistHandler(q)
	w := httptest.NewRecorder()
	newRouterWithHandler(h).ServeHTTP(w, allowlistUpdateRequest(clusterID))

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if q.rowLocks != 1 || q.row == nil || len(q.tasks) != 1 || len(q.auditRows) != 1 {
		t.Fatalf("locks=%d row=%+v tasks=%d audits=%d", q.rowLocks, q.row, len(q.tasks), len(q.auditRows))
	}
	if q.tasks[0].TaskType != "apiserver_allowlist:reconcile" || strings.Contains(string(q.tasks[0].Payload), "10.0.0.0/8") {
		t.Fatalf("task leaked desired CIDRs or has wrong type: %+v", q.tasks[0])
	}
	if strings.Contains(string(q.auditRows[0].Detail), "10.0.0.0/8") || q.auditRows[0].Action != "cluster.apiserver_allowlist.updated" {
		t.Fatalf("audit row=%+v", q.auditRows[0])
	}
}

func TestApiserverAllowlistAuditFailureRollsBackAndPublishesNothing(t *testing.T) {
	clusterID := uuid.New()
	q := &transactionalAllowlistQ{
		fakeAllowlistQuerier: &fakeAllowlistQuerier{cluster: sqlc.Cluster{ID: clusterID, Provider: "eks"}},
		auditErr:             errors.New("audit-SENTINEL"),
	}
	h := transactionalAllowlistHandler(q)
	bus := events.NewBus()
	h.SetEventBus(bus)
	ch := pubSubscribe(t, bus)
	w := httptest.NewRecorder()
	newRouterWithHandler(h).ServeHTTP(w, allowlistUpdateRequest(clusterID))

	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "audit_unavailable") || strings.Contains(w.Body.String(), "audit-SENTINEL") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if q.row != nil || q.upserted != nil || len(q.tasks) != 0 || len(q.auditRows) != 0 {
		t.Fatalf("rollback retained row=%+v upsert=%+v tasks=%d audits=%d", q.row, q.upserted, len(q.tasks), len(q.auditRows))
	}
	select {
	case event := <-ch:
		t.Fatalf("rollback published event: %+v", event)
	default:
	}
}

func TestApiserverAllowlistTaskFailureRollsBackDesiredState(t *testing.T) {
	clusterID := uuid.New()
	q := &transactionalAllowlistQ{
		fakeAllowlistQuerier: &fakeAllowlistQuerier{cluster: sqlc.Cluster{ID: clusterID, Provider: "eks"}},
		taskErr:              errors.New("task-SENTINEL"),
	}
	h := transactionalAllowlistHandler(q)
	w := httptest.NewRecorder()
	newRouterWithHandler(h).ServeHTTP(w, allowlistUpdateRequest(clusterID))

	if w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), "task-SENTINEL") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if q.row != nil || q.upserted != nil || len(q.tasks) != 0 || len(q.auditRows) != 0 {
		t.Fatalf("rollback retained row=%+v upsert=%+v tasks=%d audits=%d", q.row, q.upserted, len(q.tasks), len(q.auditRows))
	}
}

func TestApiserverAllowlistReconcileCommitsTaskAndAuditWithoutInlineEffect(t *testing.T) {
	clusterID := uuid.New()
	q := &transactionalAllowlistQ{fakeAllowlistQuerier: &fakeAllowlistQuerier{
		cluster: sqlc.Cluster{ID: clusterID, Provider: "eks"},
		row:     &sqlc.ApiserverAllowlist{ClusterID: clusterID, Mode: "monitor"},
	}}
	h := transactionalAllowlistHandler(q)
	inlineCalls := 0
	h.SetReconciler(func(context.Context, uuid.UUID) error { inlineCalls++; return nil })
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/clusters/"+clusterID.String()+"/apiserver-allowlist/reconcile/", nil)
	req.Header.Set("Idempotency-Key", "allowlist-reconcile-transactional")
	newRouterWithHandler(h).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if q.rowLocks != 1 || len(q.tasks) != 1 || len(q.auditRows) != 1 || inlineCalls != 0 {
		t.Fatalf("locks=%d tasks=%d audits=%d inline=%d", q.rowLocks, len(q.tasks), len(q.auditRows), inlineCalls)
	}
}
