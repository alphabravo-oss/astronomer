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
)

func fakeQuotaRunTx(q *fakeQuotaQuerier) quotaRunTxFunc {
	return func(_ context.Context, fn func(QuotaMutationTx) error) error {
		q.mu.Lock()
		plans := make(map[string]sqlc.QuotaPlan, len(q.plans))
		for name, plan := range q.plans {
			plans[name] = plan
		}
		audits := append([]string(nil), q.auditOps...)
		q.mu.Unlock()
		if err := fn(q); err != nil {
			q.mu.Lock()
			q.plans, q.auditOps = plans, audits
			q.mu.Unlock()
			return err
		}
		return nil
	}
}

func TestQuotaAuditFailureRollsBackEveryMutation(t *testing.T) {
	callerID := uuid.New()
	for _, operation := range []string{"create", "update", "delete"} {
		t.Run(operation, func(t *testing.T) {
			q := newFakeQuotaQuerier(sqlc.User{ID: callerID, IsSuperuser: true})
			if operation != "create" {
				q.plans["custom"] = sqlc.QuotaPlan{Name: "custom", Enforcement: "hard", MaxClustersPerProject: 7}
			}
			q.outboxErr = errors.New("audit-SENTINEL")
			h := NewQuotaHandler(q)
			h.SetRunTx(fakeQuotaRunTx(q))
			w := httptest.NewRecorder()
			switch operation {
			case "create":
				h.CreatePlan(w, authedRequest(http.MethodPost, "/api/v1/admin/quota-plans/", callerID, []byte(`{"name":"custom","enforcement":"hard"}`)))
			case "update":
				r := withURLParam(authedRequest(http.MethodPut, "/api/v1/admin/quota-plans/custom/", callerID, []byte(`{"enforcement":"soft"}`)), "name", "custom")
				h.UpdatePlan(w, r)
			case "delete":
				r := withURLParam(authedRequest(http.MethodDelete, "/api/v1/admin/quota-plans/custom/", callerID, nil), "name", "custom")
				h.DeletePlan(w, r)
			}

			if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "audit_unavailable") || strings.Contains(w.Body.String(), "audit-SENTINEL") {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if operation == "create" && len(q.plans) != 0 {
				t.Fatalf("create rollback retained plans=%+v", q.plans)
			}
			if operation == "update" && q.plans["custom"].Enforcement != "hard" {
				t.Fatalf("update rollback retained plan=%+v", q.plans["custom"])
			}
			if operation == "delete" {
				if _, ok := q.plans["custom"]; !ok {
					t.Fatal("delete rollback removed plan")
				}
			}
			if len(q.auditOps) != 0 {
				t.Fatalf("rollback retained audits=%v", q.auditOps)
			}
		})
	}
}

func TestEveryQuotaMutationUsesTransactionalExecutor(t *testing.T) {
	path, err := filepath.Abs("quotas.go")
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"CreatePlan": false, "UpdatePlan": false, "DeletePlan": false}
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
			if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "executeQuotaMutation" {
				want[fn.Name.Name] = true
			}
			return true
		})
	}
	for name, found := range want {
		if !found {
			t.Errorf("%s does not use executeQuotaMutation", name)
		}
	}
}
