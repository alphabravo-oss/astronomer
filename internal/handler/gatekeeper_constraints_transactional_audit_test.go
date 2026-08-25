package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

type transactionalGatekeeperQ struct {
	*fakeGatekeeperQuerier
	tasks    []sqlc.UpsertTaskOutboxParams
	audits   []sqlc.UpsertAuditOutboxParams
	taskErr  error
	auditErr error
	rowLocks int
}

func (q *transactionalGatekeeperQ) GetAuthoredConstraintByNameForUpdate(ctx context.Context, arg sqlc.GetAuthoredConstraintByNameForUpdateParams) (sqlc.AuthoredConstraint, error) {
	q.rowLocks++
	return q.GetAuthoredConstraintByName(ctx, sqlc.GetAuthoredConstraintByNameParams(arg))
}

func (q *transactionalGatekeeperQ) MarkAuthoredConstraintDeleted(_ context.Context, arg sqlc.MarkAuthoredConstraintDeletedParams) (sqlc.AuthoredConstraint, error) {
	row, ok := q.authored[arg.Name]
	if !ok || row.ClusterID != arg.ClusterID {
		return sqlc.AuthoredConstraint{}, pgx.ErrNoRows
	}
	row.DesiredState = "absent"
	row.SyncStatus = "pending"
	row.Generation++
	row.LastError = ""
	q.authored[arg.Name] = row
	return row, nil
}

func (q *transactionalGatekeeperQ) UpsertTaskOutbox(_ context.Context, arg sqlc.UpsertTaskOutboxParams) (sqlc.TaskOutbox, error) {
	if q.taskErr != nil {
		return sqlc.TaskOutbox{}, q.taskErr
	}
	q.tasks = append(q.tasks, arg)
	return sqlc.TaskOutbox{ID: uuid.New(), TaskType: arg.TaskType, Payload: arg.Payload}, nil
}

func (q *transactionalGatekeeperQ) UpsertAuditOutbox(_ context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	if q.auditErr != nil {
		return sqlc.AuditOutbox{}, q.auditErr
	}
	q.audits = append(q.audits, arg)
	return sqlc.AuditOutbox{ID: arg.ID, Action: arg.Action, Detail: arg.Detail}, nil
}

func cloneGatekeeperConstraints(source map[string]sqlc.AuthoredConstraint) map[string]sqlc.AuthoredConstraint {
	out := make(map[string]sqlc.AuthoredConstraint, len(source))
	for key, row := range source {
		out[key] = row
	}
	return out
}

func fakeGatekeeperRunTx(q *transactionalGatekeeperQ) gatekeeperConstraintRunTxFunc {
	return func(_ context.Context, fn func(GatekeeperConstraintMutationTx) error) error {
		authored := cloneGatekeeperConstraints(q.authored)
		upserts, deletes := q.upserts, q.deletes
		tasks := append([]sqlc.UpsertTaskOutboxParams(nil), q.tasks...)
		audits := append([]sqlc.UpsertAuditOutboxParams(nil), q.audits...)
		if err := fn(q); err != nil {
			q.authored, q.upserts, q.deletes, q.tasks, q.audits = authored, upserts, deletes, tasks, audits
			return err
		}
		return nil
	}
}

func transactionalGatekeeperHandler(q *transactionalGatekeeperQ, clusterID uuid.UUID, requester K8sRequester) *GatekeeperConstraintsHandler {
	h := NewGatekeeperConstraintsHandler(q, requester)
	h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: clustersVerbBindings(clusterID, rbac.VerbUpdate)})
	h.SetRunTx(fakeGatekeeperRunTx(q))
	return h
}

func TestGatekeeperCreateCommitsDesiredStateTaskAndAuditBeforeRemoteEffect(t *testing.T) {
	clusterID := uuid.New()
	q := &transactionalGatekeeperQ{fakeGatekeeperQuerier: newFakeGatekeeperQuerier()}
	requester := &stubK8sRequester{}
	h := transactionalGatekeeperHandler(q, clusterID, requester)
	w := httptest.NewRecorder()
	h.CreateConstraint(w, authedConstraintReq(http.MethodPost, "/", clusterID.String(), ConstraintYAMLRequest{YAML: sampleConstraintYAML}))

	if w.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	row := q.authored["must-have-foo"]
	if row.DesiredState != "present" || row.SyncStatus != "pending" || row.Generation != 1 || len(q.tasks) != 1 || len(q.audits) != 1 {
		t.Fatalf("row=%+v tasks=%d audits=%d", row, len(q.tasks), len(q.audits))
	}
	if len(requester.snapshot()) != 0 {
		t.Fatalf("HTTP path performed remote effect before worker: %+v", requester.snapshot())
	}
	if strings.Contains(string(q.tasks[0].Payload), "enforcementAction") || strings.Contains(string(q.audits[0].Detail), "enforcementAction") {
		t.Fatalf("task/audit leaked authored YAML: task=%s audit=%s", q.tasks[0].Payload, q.audits[0].Detail)
	}
	response := decodeConstraintValidation(t, w.Body.Bytes())
	if response.Applied || response.Status != "pending" || response.TaskID == "" {
		t.Fatalf("response=%+v", response)
	}
}

func TestGatekeeperCreateTaskOrAuditFailureRollsBack(t *testing.T) {
	for _, tc := range []struct {
		name     string
		taskErr  error
		auditErr error
		status   int
	}{
		{name: "task", taskErr: errors.New("task-SENTINEL"), status: http.StatusInternalServerError},
		{name: "audit", auditErr: errors.New("audit-SENTINEL"), status: http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clusterID := uuid.New()
			q := &transactionalGatekeeperQ{fakeGatekeeperQuerier: newFakeGatekeeperQuerier(), taskErr: tc.taskErr, auditErr: tc.auditErr}
			requester := &stubK8sRequester{}
			h := transactionalGatekeeperHandler(q, clusterID, requester)
			w := httptest.NewRecorder()
			h.CreateConstraint(w, authedConstraintReq(http.MethodPost, "/", clusterID.String(), ConstraintYAMLRequest{YAML: sampleConstraintYAML}))

			if w.Code != tc.status || strings.Contains(w.Body.String(), "SENTINEL") {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if tc.auditErr != nil && !strings.Contains(w.Body.String(), "audit_unavailable") {
				t.Fatalf("body=%s", w.Body.String())
			}
			if len(q.authored) != 0 || len(q.tasks) != 0 || len(q.audits) != 0 || len(requester.snapshot()) != 0 {
				t.Fatalf("rollback retained authored=%d tasks=%d audits=%d remote=%d", len(q.authored), len(q.tasks), len(q.audits), len(requester.snapshot()))
			}
		})
	}
}

func TestGatekeeperDeleteCommitsTombstoneTaskAndAuditWithoutRemoteEffect(t *testing.T) {
	clusterID := uuid.New()
	base := newFakeGatekeeperQuerier()
	base.authored["must-have-foo"] = sqlc.AuthoredConstraint{
		ID: uuid.New(), ClusterID: clusterID, Name: "must-have-foo", Kind: "K8sRequiredFoo",
		ApiVersion: "constraints.gatekeeper.sh/v1beta1", Yaml: sampleConstraintYAML,
		DesiredState: "present", SyncStatus: "synced", Generation: 1, ObservedGeneration: 1,
	}
	q := &transactionalGatekeeperQ{fakeGatekeeperQuerier: base}
	requester := &stubK8sRequester{}
	h := transactionalGatekeeperHandler(q, clusterID, requester)
	r := authedConstraintReq(http.MethodDelete, "/", clusterID.String(), nil)
	chi.RouteContext(r.Context()).URLParams.Add("name", "must-have-foo")
	w := httptest.NewRecorder()
	h.DeleteConstraint(w, r)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	row := q.authored["must-have-foo"]
	if q.rowLocks != 1 || row.DesiredState != "absent" || row.SyncStatus != "pending" || row.Generation != 2 || len(q.tasks) != 1 || len(q.audits) != 1 {
		t.Fatalf("locks=%d row=%+v tasks=%d audits=%d", q.rowLocks, row, len(q.tasks), len(q.audits))
	}
	if len(requester.snapshot()) != 0 {
		t.Fatalf("HTTP path performed remote delete: %+v", requester.snapshot())
	}
}

func TestGatekeeperDeleteAuditFailureRestoresPresentState(t *testing.T) {
	clusterID := uuid.New()
	base := newFakeGatekeeperQuerier()
	base.authored["must-have-foo"] = sqlc.AuthoredConstraint{
		ID: uuid.New(), ClusterID: clusterID, Name: "must-have-foo", Kind: "K8sRequiredFoo",
		Yaml: sampleConstraintYAML, DesiredState: "present", SyncStatus: "synced", Generation: 1, ObservedGeneration: 1,
	}
	q := &transactionalGatekeeperQ{fakeGatekeeperQuerier: base, auditErr: errors.New("audit-SENTINEL")}
	h := transactionalGatekeeperHandler(q, clusterID, &stubK8sRequester{})
	r := authedConstraintReq(http.MethodDelete, "/", clusterID.String(), nil)
	chi.RouteContext(r.Context()).URLParams.Add("name", "must-have-foo")
	w := httptest.NewRecorder()
	h.DeleteConstraint(w, r)

	row := q.authored["must-have-foo"]
	if w.Code != http.StatusServiceUnavailable || row.DesiredState != "present" || row.SyncStatus != "synced" || row.Generation != 1 || len(q.tasks) != 0 || len(q.audits) != 0 {
		t.Fatalf("status=%d row=%+v tasks=%d audits=%d body=%s", w.Code, row, len(q.tasks), len(q.audits), w.Body.String())
	}
}
