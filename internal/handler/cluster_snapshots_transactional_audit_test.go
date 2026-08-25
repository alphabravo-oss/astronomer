package handler

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/events"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
)

func (f *fakeSnapshotQuerier) GetClusterSnapshotForUpdate(ctx context.Context, id uuid.UUID) (sqlc.ClusterSnapshot, error) {
	f.mu.Lock()
	f.snapshotLocks++
	f.mu.Unlock()
	return f.GetClusterSnapshotByID(ctx, id)
}

func (f *fakeSnapshotQuerier) GetClusterSnapshotScheduleForUpdate(ctx context.Context, id uuid.UUID) (sqlc.ClusterSnapshotSchedule, error) {
	f.mu.Lock()
	f.scheduleLocks++
	f.mu.Unlock()
	return f.GetClusterSnapshotScheduleByID(ctx, id)
}

func (f *fakeSnapshotQuerier) UpsertTaskOutbox(_ context.Context, arg sqlc.UpsertTaskOutboxParams) (sqlc.TaskOutbox, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.taskOutboxErr != nil {
		return sqlc.TaskOutbox{}, f.taskOutboxErr
	}
	f.taskOutbox = append(f.taskOutbox, arg)
	return sqlc.TaskOutbox{ID: uuid.New(), TaskType: arg.TaskType, Payload: arg.Payload, QueueName: arg.QueueName}, nil
}

func (f *fakeSnapshotQuerier) UpsertAuditOutbox(_ context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.auditOutboxErr != nil {
		return sqlc.AuditOutbox{}, f.auditOutboxErr
	}
	f.auditOutbox = append(f.auditOutbox, arg)
	return sqlc.AuditOutbox{ID: arg.ID, Action: arg.Action, Detail: arg.Detail}, nil
}

func cloneSnapshotMap(source map[uuid.UUID]sqlc.ClusterSnapshot) map[uuid.UUID]sqlc.ClusterSnapshot {
	result := make(map[uuid.UUID]sqlc.ClusterSnapshot, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneRestoreMap(source map[uuid.UUID]sqlc.ClusterRestore) map[uuid.UUID]sqlc.ClusterRestore {
	result := make(map[uuid.UUID]sqlc.ClusterRestore, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneScheduleMap(source map[uuid.UUID]sqlc.ClusterSnapshotSchedule) map[uuid.UUID]sqlc.ClusterSnapshotSchedule {
	result := make(map[uuid.UUID]sqlc.ClusterSnapshotSchedule, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneOperationIdempotencyMap(source map[string]sqlc.OperationIdempotencyKey) map[string]sqlc.OperationIdempotencyKey {
	result := make(map[string]sqlc.OperationIdempotencyKey, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func fakeClusterSnapshotRunTx(q *fakeSnapshotQuerier) clusterSnapshotRunTxFunc {
	return func(_ context.Context, fn func(ClusterSnapshotMutationTx) error) error {
		q.mu.Lock()
		snapshots := cloneSnapshotMap(q.snapshots)
		restores := cloneRestoreMap(q.restores)
		schedules := cloneScheduleMap(q.schedules)
		idempotency := cloneOperationIdempotencyMap(q.idempotency)
		tasksBefore := append([]sqlc.UpsertTaskOutboxParams(nil), q.taskOutbox...)
		auditsBefore := append([]sqlc.UpsertAuditOutboxParams(nil), q.auditOutbox...)
		q.mu.Unlock()
		if err := fn(q); err != nil {
			q.mu.Lock()
			q.snapshots, q.restores, q.schedules, q.idempotency = snapshots, restores, schedules, idempotency
			q.taskOutbox, q.auditOutbox = tasksBefore, auditsBefore
			q.mu.Unlock()
			return err
		}
		return nil
	}
}

func transactionalSnapshotHandler(q *fakeSnapshotQuerier) (*ClusterSnapshotsHandler, *events.Bus, *fakeSnapshotRequester) {
	bus := events.NewBus()
	requester := newFakeSnapshotRequester()
	h := NewClusterSnapshotsHandler(q)
	h.SetRunTx(fakeClusterSnapshotRunTx(q))
	h.SetEventBus(bus)
	h.SetRequester(requester)
	return h, bus, requester
}

func TestClusterSnapshotCreateCommitsStateTaskAuditBeforePublishing(t *testing.T) {
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
			q := newFakeSnapshotQuerier(clusterID, "production")
			q.taskOutboxErr, q.auditOutboxErr = tc.taskErr, tc.auditErr
			h, bus, requester := transactionalSnapshotHandler(q)
			ch := pubSubscribe(t, bus)
			body := mustSnapshotJSON(t, map[string]any{"includedNamespaces": []string{"payments"}, "ttl": "24h"})
			req := snapshotReq(t, http.MethodPost, "/", body, map[string]string{"cluster_id": clusterID.String()})
			w := httptest.NewRecorder()

			h.CreateSnapshot(w, req)

			if w.Code != tc.wantStatus || strings.Contains(w.Body.String(), "SENTINEL") {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if len(q.snapshots) != tc.wantCommit || len(q.taskOutbox) != tc.wantCommit || len(q.auditOutbox) != tc.wantCommit {
				t.Fatalf("committed snapshot/task/audit=%d/%d/%d, want %d", len(q.snapshots), len(q.taskOutbox), len(q.auditOutbox), tc.wantCommit)
			}
			if calls := requester.snapshot(); len(calls) != 0 {
				t.Fatalf("transactional HTTP path performed %d remote calls", len(calls))
			}
			if tc.wantCommit == 0 {
				select {
				case event := <-ch:
					t.Fatalf("rollback published event: %+v", event)
				default:
				}
				return
			}
			if q.taskOutbox[0].TaskType != tasks.ClusterSnapshotApplyOperationType || q.taskOutbox[0].QueueName != tasks.ClusterTemplateApplyQueueName {
				t.Fatalf("task outbox route=%s/%s", q.taskOutbox[0].TaskType, q.taskOutbox[0].QueueName)
			}
			if strings.Contains(string(q.taskOutbox[0].Payload), "payments") || strings.Contains(string(q.auditOutbox[0].Detail), "payments") {
				t.Fatal("snapshot specification leaked into task or audit envelope")
			}
			_ = pubReceive(t, ch, events.TypeSnapshotChanged)
		})
	}
}

func TestClusterSnapshotCreateReplaysDurableReceipt(t *testing.T) {
	clusterID := uuid.New()
	q := newFakeSnapshotQuerier(clusterID, "production")
	h, _, _ := transactionalSnapshotHandler(q)
	key := uuid.NewString()
	var first SnapshotResponse
	for attempt := 0; attempt < 2; attempt++ {
		req := snapshotReq(t, http.MethodPost, "/api/v1/clusters/"+clusterID.String()+"/snapshots", mustSnapshotJSON(t, map[string]any{"ttl": "24h"}), map[string]string{"cluster_id": clusterID.String()})
		req.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		h.CreateSnapshot(w, req)
		if w.Code != http.StatusAccepted || w.Header().Get("Retry-After") != "2" || w.Header().Get("Location") == "" {
			t.Fatalf("attempt %d status=%d headers=%v body=%s", attempt, w.Code, w.Header(), w.Body.String())
		}
		var got SnapshotResponse
		unwrap(t, w, &got)
		if attempt == 0 {
			first = got
		} else if got.ID != first.ID {
			t.Fatalf("replay id=%s, want %s", got.ID, first.ID)
		}
	}
	if len(q.snapshots) != 1 || len(q.taskOutbox) != 1 || len(q.auditOutbox) != 1 {
		t.Fatalf("replay duplicated snapshot/task/audit=%d/%d/%d", len(q.snapshots), len(q.taskOutbox), len(q.auditOutbox))
	}
}

func TestClusterSnapshotRestoreAndDeleteRollbackAtomically(t *testing.T) {
	clusterID := uuid.New()
	q := newFakeSnapshotQuerier(clusterID, "production")
	snapshot, _ := q.CreateClusterSnapshot(context.Background(), sqlc.CreateClusterSnapshotParams{
		ClusterID: clusterID, VeleroName: "production-backup", VeleroNamespace: "velero", Phase: "Completed",
	})
	q.auditOutboxErr = errors.New("audit-SENTINEL")
	h, bus, requester := transactionalSnapshotHandler(q)
	ch := pubSubscribe(t, bus)

	restoreReq := snapshotReq(t, http.MethodPost, "/", mustSnapshotJSON(t, map[string]any{"spec": map[string]any{"includedNamespaces": []string{"payments"}}}), map[string]string{
		"cluster_id": clusterID.String(), "id": snapshot.ID.String(),
	})
	restoreW := httptest.NewRecorder()
	h.CreateRestore(restoreW, restoreReq)
	if restoreW.Code != http.StatusServiceUnavailable || len(q.restores) != 0 || len(q.taskOutbox) != 0 {
		t.Fatalf("restore rollback status=%d restores=%d tasks=%d", restoreW.Code, len(q.restores), len(q.taskOutbox))
	}

	deleteReq := snapshotReq(t, http.MethodDelete, "/", nil, map[string]string{
		"cluster_id": clusterID.String(), "id": snapshot.ID.String(),
	})
	deleteW := httptest.NewRecorder()
	h.DeleteSnapshot(deleteW, deleteReq)
	if deleteW.Code != http.StatusServiceUnavailable || len(q.snapshots) != 1 || len(q.taskOutbox) != 0 || q.snapshotLocks != 1 {
		t.Fatalf("delete rollback status=%d snapshots=%d tasks=%d locks=%d", deleteW.Code, len(q.snapshots), len(q.taskOutbox), q.snapshotLocks)
	}
	if len(requester.snapshot()) != 0 {
		t.Fatal("rolled-back restore/delete invoked the member cluster")
	}
	select {
	case event := <-ch:
		t.Fatalf("rolled-back restore/delete published event: %+v", event)
	default:
	}
}

func TestClusterSnapshotScheduleCRUDRollsBackMandatoryAudit(t *testing.T) {
	clusterID := uuid.New()
	q := newFakeSnapshotQuerier(clusterID, "production")
	q.auditOutboxErr = errors.New("audit-SENTINEL")
	h, _, _ := transactionalSnapshotHandler(q)
	createBody := mustSnapshotJSON(t, map[string]any{"name": "daily", "cron_schedule": "0 2 * * *"})
	createW := httptest.NewRecorder()
	h.CreateSchedule(createW, snapshotReq(t, http.MethodPost, "/", createBody, map[string]string{"cluster_id": clusterID.String()}))
	if createW.Code != http.StatusServiceUnavailable || len(q.schedules) != 0 {
		t.Fatalf("create rollback status=%d schedules=%d", createW.Code, len(q.schedules))
	}

	q.auditOutboxErr = nil
	seed, _ := q.CreateClusterSnapshotSchedule(context.Background(), sqlc.CreateClusterSnapshotScheduleParams{
		ClusterID: clusterID, Name: "daily", CronSchedule: "0 2 * * *", Enabled: true,
	})
	q.auditOutboxErr = errors.New("audit-SENTINEL")
	updateW := httptest.NewRecorder()
	h.UpdateSchedule(updateW, snapshotReq(t, http.MethodPut, "/", mustSnapshotJSON(t, map[string]any{"name": "nightly", "cron_schedule": "0 3 * * *"}), map[string]string{
		"cluster_id": clusterID.String(), "id": seed.ID.String(),
	}))
	if updateW.Code != http.StatusServiceUnavailable || q.schedules[seed.ID].Name != "daily" || q.scheduleLocks != 1 {
		t.Fatalf("update rollback status=%d row=%+v locks=%d", updateW.Code, q.schedules[seed.ID], q.scheduleLocks)
	}

	deleteW := httptest.NewRecorder()
	h.DeleteSchedule(deleteW, snapshotReq(t, http.MethodDelete, "/", nil, map[string]string{
		"cluster_id": clusterID.String(), "id": seed.ID.String(),
	}))
	if deleteW.Code != http.StatusServiceUnavailable || len(q.schedules) != 1 || q.scheduleLocks != 2 {
		t.Fatalf("delete rollback status=%d schedules=%d locks=%d", deleteW.Code, len(q.schedules), q.scheduleLocks)
	}
}

func TestEveryClusterSnapshotMutationUsesTransactionalExecutor(t *testing.T) {
	path, err := filepath.Abs("cluster_snapshots.go")
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"CreateSnapshot": false, "DeleteSnapshot": false, "CreateRestore": false,
		"CreateSchedule": false, "UpdateSchedule": false, "DeleteSchedule": false,
	}
	for _, declaration := range file.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		if _, tracked := want[fn.Name.Name]; !tracked {
			continue
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "executeClusterSnapshotMutation" {
				want[fn.Name.Name] = true
			}
			return true
		})
	}
	for name, found := range want {
		if !found {
			t.Errorf("%s does not use executeClusterSnapshotMutation", name)
		}
	}
}

func TestDeterministicDeleteBackupRequestNamePreservesReplayFence(t *testing.T) {
	operationID := uuid.NewString()
	longName := strings.Repeat("a", 253)
	first := deterministicDeleteBackupRequestName(longName, operationID)
	second := deterministicDeleteBackupRequestName(longName, operationID)
	if first != second || len(first) > 253 || !strings.HasSuffix(first, "-delete-"+operationID) {
		t.Fatalf("delete request name=%q len=%d", first, len(first))
	}
}
