package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type transactionalPlatformDefaultQ struct {
	*fakePlatformDefaultTemplateQuerier
	tasks    []sqlc.UpsertClusterTemplateApplicationWithTaskOutboxParams
	audits   []sqlc.UpsertAuditOutboxParams
	seen     []sqlc.UpsertAuditOutboxParams
	auditErr error
	locks    int
}

func (q *transactionalPlatformDefaultQ) GetPlatformConfigForUpdate(_ context.Context) (sqlc.PlatformConfiguration, error) {
	q.locks++
	return q.config, q.getCfgErr
}

func (q *transactionalPlatformDefaultQ) UpsertClusterTemplateApplicationWithTaskOutbox(_ context.Context, arg sqlc.UpsertClusterTemplateApplicationWithTaskOutboxParams) (sqlc.ClusterTemplateApplication, error) {
	q.tasks = append(q.tasks, arg)
	q.upserts = append(q.upserts, sqlc.UpsertClusterTemplateApplicationParams{ClusterID: arg.ClusterID, TemplateID: arg.TemplateID, SpecSnapshot: arg.SpecSnapshot})
	return sqlc.ClusterTemplateApplication{ClusterID: arg.ClusterID, TemplateID: arg.TemplateID, SpecSnapshot: arg.SpecSnapshot, Status: "pending"}, nil
}

func (q *transactionalPlatformDefaultQ) UpsertTaskOutbox(_ context.Context, arg sqlc.UpsertTaskOutboxParams) (sqlc.TaskOutbox, error) {
	return sqlc.TaskOutbox{ID: uuid.New(), TaskType: arg.TaskType, Payload: arg.Payload}, nil
}

func (q *transactionalPlatformDefaultQ) UpsertAuditOutbox(_ context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	q.seen = append(q.seen, arg)
	if q.auditErr != nil {
		return sqlc.AuditOutbox{}, q.auditErr
	}
	q.audits = append(q.audits, arg)
	return sqlc.AuditOutbox{ID: arg.ID, Action: arg.Action, Detail: arg.Detail}, nil
}

func fakePlatformDefaultRunTx(q *transactionalPlatformDefaultQ) platformDefaultTemplateRunTxFunc {
	return func(_ context.Context, fn func(PlatformDefaultTemplateMutationTx) error) error {
		config := q.config
		upserts := append([]sqlc.UpsertClusterTemplateApplicationParams(nil), q.upserts...)
		tasks := append([]sqlc.UpsertClusterTemplateApplicationWithTaskOutboxParams(nil), q.tasks...)
		audits := append([]sqlc.UpsertAuditOutboxParams(nil), q.audits...)
		if err := fn(q); err != nil {
			q.config, q.upserts, q.tasks, q.audits = config, upserts, tasks, audits
			return err
		}
		return nil
	}
}

func transactionalPlatformDefaultHandler(q *transactionalPlatformDefaultQ) *PlatformDefaultTemplateHandler {
	h := NewPlatformDefaultTemplateHandler(q)
	h.SetRunTx(fakePlatformDefaultRunTx(q))
	return h
}

func TestEveryPlatformDefaultMutationEntryPointUsesTransaction(t *testing.T) {
	calls := parsedMethodCalls(t, "platform_default_template.go")
	for _, method := range []string{"Update", "Reapply"} {
		if !calls[method]["runTx"] {
			t.Errorf("PlatformDefaultTemplateHandler.%s does not call the transaction runner", method)
		}
	}
}

func TestPlatformDefaultUpdateLocksAndRollsBackWithAudit(t *testing.T) {
	callerID, oldID, newID := uuid.New(), uuid.New(), uuid.New()
	q := &transactionalPlatformDefaultQ{
		fakePlatformDefaultTemplateQuerier: newFakePlatformDefaultTemplateQuerier(sqlc.User{ID: callerID, IsSuperuser: true}),
		auditErr:                           errors.New("audit-SENTINEL"),
	}
	q.config = sqlc.PlatformConfiguration{ID: 1, DefaultClusterTemplateID: pgtype.UUID{Bytes: oldID, Valid: true}}
	q.templates[newID] = sqlc.ClusterTemplate{ID: newID, Name: "new-default", Spec: json.RawMessage(`{"SECRET-CONFIG-MARKER":true}`)}
	h := transactionalPlatformDefaultHandler(q)
	body, _ := json.Marshal(map[string]any{"template_id": newID.String()})
	w := httptest.NewRecorder()
	h.Update(w, authedRequest(http.MethodPut, "/", callerID, body))

	if w.Code != http.StatusServiceUnavailable || q.locks != 1 || uuid.UUID(q.config.DefaultClusterTemplateID.Bytes) != oldID || len(q.audits) != 0 {
		t.Fatalf("status=%d locks=%d config=%+v audits=%d body=%s", w.Code, q.locks, q.config, len(q.audits), w.Body.String())
	}
	if len(q.seen) != 1 || strings.Contains(string(q.seen[0].Detail), "SECRET-CONFIG-MARKER") || strings.Contains(w.Body.String(), "SENTINEL") {
		t.Fatalf("config leaked via audit/error: seen=%+v body=%s", q.seen, w.Body.String())
	}
}

func TestPlatformDefaultReapplyCommitsApplicationIdentifierTaskAndAuditTogether(t *testing.T) {
	callerID, templateID, clusterID := uuid.New(), uuid.New(), uuid.New()
	q := &transactionalPlatformDefaultQ{fakePlatformDefaultTemplateQuerier: newFakePlatformDefaultTemplateQuerier(sqlc.User{ID: callerID, IsSuperuser: true})}
	q.config = sqlc.PlatformConfiguration{ID: 1, DefaultClusterTemplateID: pgtype.UUID{Bytes: templateID, Valid: true}}
	q.templates[templateID] = sqlc.ClusterTemplate{ID: templateID, Name: "baseline", Spec: json.RawMessage(`{"SECRET-SPEC-MARKER":true}`)}
	q.clusters[clusterID] = sqlc.Cluster{ID: clusterID, Name: "prod"}
	h := transactionalPlatformDefaultHandler(q)
	r := withURLParam(authedRequest(http.MethodPost, "/", callerID, nil), "cluster_id", clusterID.String())
	w := httptest.NewRecorder()
	h.Reapply(w, r)

	if w.Code != http.StatusAccepted || q.locks != 1 || len(q.upserts) != 1 || len(q.tasks) != 1 || len(q.audits) != 1 {
		t.Fatalf("status=%d locks=%d apps=%d tasks=%d audits=%d body=%s", w.Code, q.locks, len(q.upserts), len(q.tasks), len(q.audits), w.Body.String())
	}
	if !strings.Contains(string(q.tasks[0].Payload), clusterID.String()) || strings.Contains(string(q.tasks[0].Payload), "SECRET-SPEC-MARKER") || strings.Contains(string(q.audits[0].Detail), "SECRET-SPEC-MARKER") {
		t.Fatalf("task/audit payload contract violated: task=%s audit=%s", q.tasks[0].Payload, q.audits[0].Detail)
	}
}

func TestPlatformDefaultReapplyAuditFailureRollsBackApplicationAndTask(t *testing.T) {
	callerID, templateID, clusterID := uuid.New(), uuid.New(), uuid.New()
	q := &transactionalPlatformDefaultQ{
		fakePlatformDefaultTemplateQuerier: newFakePlatformDefaultTemplateQuerier(sqlc.User{ID: callerID, IsSuperuser: true}),
		auditErr:                           errors.New("audit unavailable"),
	}
	q.config = sqlc.PlatformConfiguration{ID: 1, DefaultClusterTemplateID: pgtype.UUID{Bytes: templateID, Valid: true}}
	q.templates[templateID] = sqlc.ClusterTemplate{ID: templateID, Name: "baseline", Spec: json.RawMessage(`{"environment":"production"}`)}
	q.clusters[clusterID] = sqlc.Cluster{ID: clusterID, Name: "prod"}
	h := transactionalPlatformDefaultHandler(q)
	r := withURLParam(authedRequest(http.MethodPost, "/", callerID, nil), "cluster_id", clusterID.String())
	w := httptest.NewRecorder()
	h.Reapply(w, r)

	if w.Code != http.StatusServiceUnavailable || len(q.upserts) != 0 || len(q.tasks) != 0 || len(q.audits) != 0 {
		t.Fatalf("status=%d apps=%d tasks=%d audits=%d body=%s", w.Code, len(q.upserts), len(q.tasks), len(q.audits), w.Body.String())
	}
}
