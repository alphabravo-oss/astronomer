package handler

import (
	"context"
	"encoding/json"
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
)

type transactionalControlPlaneQ struct {
	stubControlPlaneQ
	policy        sqlc.ControlPlanePolicy
	alert         sqlc.ControlPlaneAlert
	silences      map[uuid.UUID]sqlc.ControlPlaneSilence
	audits        []sqlc.UpsertAuditOutboxParams
	outboxErr     error
	policyMutated bool
	alertMutated  bool
}

func (q *transactionalControlPlaneQ) UpsertDefaultControlPlanePolicy(_ context.Context, _ sqlc.UpsertDefaultControlPlanePolicyParams) (sqlc.ControlPlanePolicy, error) {
	q.policyMutated = true
	return q.policy, nil
}

func (q *transactionalControlPlaneQ) AcknowledgeControlPlaneAlert(_ context.Context, _ sqlc.AcknowledgeControlPlaneAlertParams) (sqlc.ControlPlaneAlert, error) {
	q.alertMutated = true
	return q.alert, nil
}

func (q *transactionalControlPlaneQ) CreateControlPlaneSilence(_ context.Context, arg sqlc.CreateControlPlaneSilenceParams) (sqlc.ControlPlaneSilence, error) {
	item := sqlc.ControlPlaneSilence{
		ID: uuid.New(), Controller: arg.Controller, ConditionType: arg.ConditionType,
		Reason: arg.Reason, StartsAt: arg.StartsAt, EndsAt: arg.EndsAt,
	}
	q.silences[item.ID] = item
	return item, nil
}

func (q *transactionalControlPlaneQ) DeleteControlPlaneSilence(_ context.Context, id uuid.UUID) (sqlc.ControlPlaneSilence, error) {
	item, ok := q.silences[id]
	if !ok {
		return sqlc.ControlPlaneSilence{}, errors.New("missing silence")
	}
	delete(q.silences, id)
	return item, nil
}

func (q *transactionalControlPlaneQ) UpsertAuditOutbox(_ context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	if q.outboxErr != nil {
		return sqlc.AuditOutbox{}, q.outboxErr
	}
	q.audits = append(q.audits, arg)
	return sqlc.AuditOutbox{ID: arg.ID, Action: arg.Action}, nil
}

func cloneControlPlaneSilences(source map[uuid.UUID]sqlc.ControlPlaneSilence) map[uuid.UUID]sqlc.ControlPlaneSilence {
	cloned := make(map[uuid.UUID]sqlc.ControlPlaneSilence, len(source))
	for id, item := range source {
		cloned[id] = item
	}
	return cloned
}

func fakeControlPlaneRunTx(q *transactionalControlPlaneQ) controlPlaneRunTxFunc {
	return func(_ context.Context, fn func(ControlPlaneMutationTx) error) error {
		silences := cloneControlPlaneSilences(q.silences)
		audits := append([]sqlc.UpsertAuditOutboxParams(nil), q.audits...)
		policyMutated, alertMutated := q.policyMutated, q.alertMutated
		if err := fn(q); err != nil {
			q.silences, q.audits = silences, audits
			q.policyMutated, q.alertMutated = policyMutated, alertMutated
			return err
		}
		return nil
	}
}

func newTransactionalControlPlaneHandler(q *transactionalControlPlaneQ) *ControlPlaneHandler {
	h := NewControlPlaneHandler(q, nil, nil, nil, nil, nil, nil, nil)
	h.SetRunTx(fakeControlPlaneRunTx(q))
	return h
}

func TestControlPlaneAuditFailureRollsBackEveryMutation(t *testing.T) {
	alertID, silenceID := uuid.New(), uuid.New()
	for _, operation := range []string{"policy", "acknowledge", "create_silence", "delete_silence"} {
		t.Run(operation, func(t *testing.T) {
			q := &transactionalControlPlaneQ{
				policy: sqlc.ControlPlanePolicy{ID: uuid.New(), Name: "default"},
				alert:  sqlc.ControlPlaneAlert{ID: alertID, Controller: "delivery", ConditionType: "queue_depth", Status: "active"},
				silences: map[uuid.UUID]sqlc.ControlPlaneSilence{
					silenceID: {ID: silenceID, Controller: "delivery", ConditionType: "queue_depth", Reason: "incident"},
				},
				outboxErr: errors.New("audit-SENTINEL"),
			}
			h := newTransactionalControlPlaneHandler(q)
			w := httptest.NewRecorder()
			switch operation {
			case "policy":
				h.UpdatePolicy(w, httptest.NewRequest(http.MethodPut, "/api/v1/controllers/policy/", strings.NewReader(`{}`)))
			case "acknowledge":
				h.AcknowledgeAlert(w, withURLParam(httptest.NewRequest(http.MethodPost, "/api/v1/controllers/alerts/"+alertID.String()+"/acknowledge/", nil), "id", alertID.String()))
			case "create_silence":
				h.CreateSilence(w, httptest.NewRequest(http.MethodPost, "/api/v1/controllers/silences/", strings.NewReader(`{"controller":"delivery","conditionType":"queue_depth","reason":"incident"}`)))
			case "delete_silence":
				h.DeleteSilence(w, withURLParam(httptest.NewRequest(http.MethodDelete, "/api/v1/controllers/silences/"+silenceID.String()+"/", nil), "id", silenceID.String()))
			}
			if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "audit_unavailable") || strings.Contains(w.Body.String(), "audit-SENTINEL") {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if q.policyMutated || q.alertMutated || len(q.silences) != 1 || len(q.audits) != 0 {
				t.Fatalf("rollback retained effects policy=%v alert=%v silences=%d audits=%d", q.policyMutated, q.alertMutated, len(q.silences), len(q.audits))
			}
		})
	}
}

func TestControlPlaneSilenceAuditOmitsReason(t *testing.T) {
	const secretReason = "customer-secret-incident-reference"
	q := &transactionalControlPlaneQ{silences: map[uuid.UUID]sqlc.ControlPlaneSilence{}}
	h := newTransactionalControlPlaneHandler(q)
	w := httptest.NewRecorder()
	body, _ := json.Marshal(CreateControlPlaneSilenceRequest{Controller: "delivery", ConditionType: "queue_depth", Reason: secretReason, Duration: "2h"})
	h.CreateSilence(w, httptest.NewRequest(http.MethodPost, "/api/v1/controllers/silences/", strings.NewReader(string(body))))
	if w.Code != http.StatusCreated || len(q.audits) != 1 {
		t.Fatalf("status=%d audits=%d body=%s", w.Code, len(q.audits), w.Body.String())
	}
	auditJSON, _ := json.Marshal(q.audits[0])
	if strings.Contains(string(auditJSON), secretReason) || q.audits[0].ResourceName != "delivery" {
		t.Fatalf("silence audit disclosed reason: %s", auditJSON)
	}
}

func TestEveryControlPlaneMutationUsesTransactionalExecutor(t *testing.T) {
	path, err := filepath.Abs("control_plane.go")
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"UpdatePolicy": false, "AcknowledgeAlert": false, "CreateSilence": false, "DeleteSilence": false}
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
			if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "executeControlPlaneMutation" {
				want[fn.Name.Name] = true
			}
			return true
		})
	}
	for name, found := range want {
		if !found {
			t.Errorf("%s does not use executeControlPlaneMutation", name)
		}
	}
}
