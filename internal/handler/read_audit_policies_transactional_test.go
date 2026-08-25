package handler

import (
	"bytes"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

func TestReadAuditPolicyAuditFailureRollsBackEveryMutation(t *testing.T) {
	for _, operation := range []string{"create", "update", "delete"} {
		t.Run(operation, func(t *testing.T) {
			q := newFakeQuerier(true)
			policyID := uuid.New()
			if operation != "create" {
				q.rows[policyID] = sqlc.ReadAuditPolicy{
					ID: policyID, Name: "original", PathPattern: "/original", Verbs: "GET",
					SampleRate: 1, Enabled: true, CreatedAt: time.Now(), UpdatedAt: time.Now(),
				}
			}
			q.outboxErr = errors.New("audit-SENTINEL")
			invalidator := &countingPolicyInvalidator{}
			h := NewReadAuditPolicyHandler(q, nil)
			h.SetRunTx(fakeReadAuditPolicyRunTx(q))
			h.SetCacheInvalidator(invalidator)
			router := newPolicyRouter(h)

			var req *http.Request
			switch operation {
			case "create":
				req = httptest.NewRequest(http.MethodPost, "/api/v1/admin/read-audit-policies/", bytes.NewBufferString(`{"name":"new","path_pattern":"/new"}`))
			case "update":
				req = httptest.NewRequest(http.MethodPut, "/api/v1/admin/read-audit-policies/"+policyID.String()+"/", bytes.NewBufferString(`{"path_pattern":"/replacement"}`))
			case "delete":
				req = httptest.NewRequest(http.MethodDelete, "/api/v1/admin/read-audit-policies/"+policyID.String()+"/", nil)
			}
			req = withAuth(req, q.user.ID)
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "audit_unavailable") || strings.Contains(w.Body.String(), "audit-SENTINEL") {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if invalidator.calls != 0 || len(q.auditOps) != 0 {
				t.Fatalf("rollback invalidated/persisted audit: invalidations=%d audits=%v", invalidator.calls, q.auditOps)
			}
			if operation == "create" && len(q.rows) != 0 {
				t.Fatalf("create rollback retained rows: %+v", q.rows)
			}
			if operation == "update" && q.rows[policyID].PathPattern != "/original" {
				t.Fatalf("update rollback retained path=%q", q.rows[policyID].PathPattern)
			}
			if operation == "delete" {
				if _, ok := q.rows[policyID]; !ok {
					t.Fatal("delete rollback removed policy")
				}
			}
		})
	}
}

func TestEveryReadAuditPolicyMutationUsesTransactionalExecutor(t *testing.T) {
	path, err := filepath.Abs("read_audit_policies.go")
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"Create": false, "Update": false, "Delete": false}
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
			if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "executeReadAuditPolicyMutation" {
				want[fn.Name.Name] = true
			}
			return true
		})
	}
	for name, found := range want {
		if !found {
			t.Errorf("%s does not use executeReadAuditPolicyMutation", name)
		}
	}
}
