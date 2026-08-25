package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
)

type transactionalControlPlaneSnapshotQ struct {
	cluster     sqlc.Cluster
	rows        map[uuid.UUID]sqlc.ControlPlaneSnapshot
	tasks       []sqlc.UpsertTaskOutboxParams
	audits      []sqlc.UpsertAuditOutboxParams
	idempotency map[string]sqlc.OperationIdempotencyKey
	taskErr     error
	auditErr    error
}

func (q *transactionalControlPlaneSnapshotQ) ReserveOperationIdempotencyKey(_ context.Context, arg sqlc.ReserveOperationIdempotencyKeyParams) (sqlc.OperationIdempotencyKey, error) {
	key := operationIdempotencyMapKey(arg.Scope, arg.IdempotencyKey)
	row, ok := q.idempotency[key]
	if !ok {
		row = sqlc.OperationIdempotencyKey{Scope: arg.Scope, IdempotencyKey: arg.IdempotencyKey}
		q.idempotency[key] = row
	}
	return row, nil
}

func (q *transactionalControlPlaneSnapshotQ) AttachOperationIdempotencyKey(_ context.Context, arg sqlc.AttachOperationIdempotencyKeyParams) (sqlc.OperationIdempotencyKey, error) {
	key := operationIdempotencyMapKey(arg.Scope, arg.IdempotencyKey)
	row := q.idempotency[key]
	row.OperationTable = arg.OperationTable
	row.OperationID = pgtype.UUID{Bytes: arg.OperationID, Valid: true}
	row.Response = append(json.RawMessage(nil), arg.Response...)
	q.idempotency[key] = row
	return row, nil
}

func (q *transactionalControlPlaneSnapshotQ) GetClusterByID(_ context.Context, id uuid.UUID) (sqlc.Cluster, error) {
	if q.cluster.ID != id {
		return sqlc.Cluster{}, pgx.ErrNoRows
	}
	return q.cluster, nil
}

func (q *transactionalControlPlaneSnapshotQ) CreateControlPlaneSnapshot(_ context.Context, arg sqlc.CreateControlPlaneSnapshotParams) (sqlc.ControlPlaneSnapshot, error) {
	row := sqlc.ControlPlaneSnapshot{
		ID: uuid.New(), ClusterID: arg.ClusterID, Name: arg.Name,
		Status: arg.Status, Location: arg.Location, RequestedByID: arg.RequestedByID,
	}
	q.rows[row.ID] = row
	return row, nil
}

func (q *transactionalControlPlaneSnapshotQ) GetControlPlaneSnapshotByID(_ context.Context, id uuid.UUID) (sqlc.ControlPlaneSnapshot, error) {
	row, ok := q.rows[id]
	if !ok {
		return sqlc.ControlPlaneSnapshot{}, pgx.ErrNoRows
	}
	return row, nil
}

func (q *transactionalControlPlaneSnapshotQ) ListControlPlaneSnapshotsByCluster(context.Context, sqlc.ListControlPlaneSnapshotsByClusterParams) ([]sqlc.ControlPlaneSnapshot, error) {
	return nil, nil
}

func (q *transactionalControlPlaneSnapshotQ) CountControlPlaneSnapshotsByCluster(context.Context, uuid.UUID) (int64, error) {
	return int64(len(q.rows)), nil
}

func (q *transactionalControlPlaneSnapshotQ) MarkControlPlaneSnapshotStatus(_ context.Context, arg sqlc.MarkControlPlaneSnapshotStatusParams) error {
	row := q.rows[arg.ID]
	row.Status, row.Error = arg.Status, arg.Error
	q.rows[arg.ID] = row
	return nil
}

func (q *transactionalControlPlaneSnapshotQ) MarkControlPlaneSnapshotFailed(_ context.Context, arg sqlc.MarkControlPlaneSnapshotFailedParams) error {
	row := q.rows[arg.ID]
	row.Status, row.Error = "failed", arg.Error
	q.rows[arg.ID] = row
	return nil
}

func (q *transactionalControlPlaneSnapshotQ) UpsertTaskOutbox(_ context.Context, arg sqlc.UpsertTaskOutboxParams) (sqlc.TaskOutbox, error) {
	if q.taskErr != nil {
		return sqlc.TaskOutbox{}, q.taskErr
	}
	q.tasks = append(q.tasks, arg)
	return sqlc.TaskOutbox{ID: uuid.New(), TaskType: arg.TaskType, QueueName: arg.QueueName}, nil
}

func (q *transactionalControlPlaneSnapshotQ) UpsertAuditOutbox(_ context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	if q.auditErr != nil {
		return sqlc.AuditOutbox{}, q.auditErr
	}
	q.audits = append(q.audits, arg)
	return sqlc.AuditOutbox{ID: arg.ID, Action: arg.Action, Detail: arg.Detail}, nil
}

func cloneControlPlaneSnapshotRows(source map[uuid.UUID]sqlc.ControlPlaneSnapshot) map[uuid.UUID]sqlc.ControlPlaneSnapshot {
	result := make(map[uuid.UUID]sqlc.ControlPlaneSnapshot, len(source))
	for id, row := range source {
		result[id] = row
	}
	return result
}

func fakeControlPlaneSnapshotRunTx(q *transactionalControlPlaneSnapshotQ) controlPlaneSnapshotRunTxFunc {
	return func(_ context.Context, fn func(ControlPlaneSnapshotMutationTx) error) error {
		rows := cloneControlPlaneSnapshotRows(q.rows)
		tasksBefore := append([]sqlc.UpsertTaskOutboxParams(nil), q.tasks...)
		auditsBefore := append([]sqlc.UpsertAuditOutboxParams(nil), q.audits...)
		idempotencyBefore := cloneOperationIdempotencyMap(q.idempotency)
		if err := fn(q); err != nil {
			q.rows, q.tasks, q.audits, q.idempotency = rows, tasksBefore, auditsBefore, idempotencyBefore
			return err
		}
		return nil
	}
}

func TestControlPlaneSnapshotTriggerCommitsStateTaskAndAuditTogether(t *testing.T) {
	for _, tc := range []struct {
		name       string
		taskErr    error
		auditErr   error
		wantStatus int
		wantCommit int
	}{
		{name: "commit", wantStatus: http.StatusAccepted, wantCommit: 1},
		{name: "task failure rollback", taskErr: errors.New("task-SENTINEL"), wantStatus: http.StatusInternalServerError},
		{name: "audit failure rollback", auditErr: errors.New("audit-SENTINEL"), wantStatus: http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clusterID := uuid.New()
			q := &transactionalControlPlaneSnapshotQ{
				cluster: sqlc.Cluster{ID: clusterID, Name: "production", Distribution: "rke2"},
				rows:    map[uuid.UUID]sqlc.ControlPlaneSnapshot{}, idempotency: map[string]sqlc.OperationIdempotencyKey{},
				taskErr: tc.taskErr, auditErr: tc.auditErr,
			}
			requester := newFakeSnapshotRequester()
			h := NewControlPlaneSnapshotHandler(q)
			h.SetRequester(requester)
			h.SetRunTx(fakeControlPlaneSnapshotRunTx(q))
			rctx := chi.NewRouteContext()
			rctx.URLParams.Add("cluster_id", clusterID.String())
			req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/"+clusterID.String()+"/control-plane-snapshots/", strings.NewReader(`{"name":"cp-safe","location":"s3"}`))
			req.Header.Set("Idempotency-Key", uuid.NewString())
			req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
			w := httptest.NewRecorder()

			h.TriggerSnapshot(w, req)

			if w.Code != tc.wantStatus || strings.Contains(w.Body.String(), "SENTINEL") {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if len(q.rows) != tc.wantCommit || len(q.tasks) != tc.wantCommit || len(q.audits) != tc.wantCommit {
				t.Fatalf("committed row/task/audit=%d/%d/%d, want %d", len(q.rows), len(q.tasks), len(q.audits), tc.wantCommit)
			}
			if len(requester.snapshot()) != 0 {
				t.Fatal("transactional request path applied a privileged Job inline")
			}
			if tc.wantCommit == 1 {
				if q.tasks[0].TaskType != tasks.ControlPlaneSnapshotApplyType || q.tasks[0].QueueName != tasks.ClusterTemplateApplyQueueName {
					t.Fatalf("task route=%s/%s", q.tasks[0].TaskType, q.tasks[0].QueueName)
				}
				for _, row := range q.rows {
					if row.Status != "pending" {
						t.Fatalf("committed desired state=%q, want pending", row.Status)
					}
				}
			}
		})
	}
}

func TestControlPlaneSnapshotTriggerReplaysDurableReceipt(t *testing.T) {
	clusterID := uuid.New()
	q := &transactionalControlPlaneSnapshotQ{
		cluster: sqlc.Cluster{ID: clusterID, Name: "production", Distribution: "rke2"},
		rows:    map[uuid.UUID]sqlc.ControlPlaneSnapshot{}, idempotency: map[string]sqlc.OperationIdempotencyKey{},
	}
	h := NewControlPlaneSnapshotHandler(q)
	h.SetRunTx(fakeControlPlaneSnapshotRunTx(q))
	key := uuid.NewString()
	var firstID uuid.UUID
	for attempt := 0; attempt < 2; attempt++ {
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("cluster_id", clusterID.String())
		req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/"+clusterID.String()+"/control-plane-snapshots/", strings.NewReader(`{"name":"cp-safe","location":"s3"}`))
		req.Header.Set("Idempotency-Key", key)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		w := httptest.NewRecorder()
		h.TriggerSnapshot(w, req)
		if w.Code != http.StatusAccepted || w.Header().Get("Retry-After") != "2" || w.Header().Get("Location") == "" {
			t.Fatalf("attempt %d status=%d headers=%v body=%s", attempt, w.Code, w.Header(), w.Body.String())
		}
		var got ControlPlaneSnapshotResponse
		unwrap(t, w, &got)
		if attempt == 0 {
			firstID = got.ID
		} else if got.ID != firstID {
			t.Fatalf("replay id=%s, want %s", got.ID, firstID)
		}
	}
	if len(q.rows) != 1 || len(q.tasks) != 1 || len(q.audits) != 1 {
		t.Fatalf("replay duplicated row/task/audit=%d/%d/%d", len(q.rows), len(q.tasks), len(q.audits))
	}
}
