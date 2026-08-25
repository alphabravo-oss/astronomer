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
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type transactionalNativeRBACQ struct {
	*fakeNativeRBACQuerier
	rules     map[uuid.UUID]sqlc.NativeRbacRule
	audits    []sqlc.UpsertAuditOutboxParams
	outboxErr error
}

func newTransactionalNativeRBACQ() *transactionalNativeRBACQ {
	return &transactionalNativeRBACQ{fakeNativeRBACQuerier: &fakeNativeRBACQuerier{}, rules: map[uuid.UUID]sqlc.NativeRbacRule{}}
}

func (q *transactionalNativeRBACQ) CreateNativeRBACRule(_ context.Context, arg sqlc.CreateNativeRBACRuleParams) (sqlc.NativeRbacRule, error) {
	q.created++
	rule := sqlc.NativeRbacRule{
		ID: uuid.New(), UserID: arg.UserID, ClusterID: arg.ClusterID, Namespace: arg.Namespace,
		ApiGroup: arg.ApiGroup, Resource: arg.Resource, Verbs: arg.Verbs, CreatedByID: arg.CreatedByID,
	}
	q.rules[rule.ID] = rule
	return rule, nil
}

func (q *transactionalNativeRBACQ) GetNativeRBACRuleByID(_ context.Context, id uuid.UUID) (sqlc.NativeRbacRule, error) {
	rule, ok := q.rules[id]
	if !ok {
		return sqlc.NativeRbacRule{}, pgx.ErrNoRows
	}
	return rule, nil
}

func (q *transactionalNativeRBACQ) GetNativeRBACRuleForUpdate(ctx context.Context, id uuid.UUID) (sqlc.NativeRbacRule, error) {
	return q.GetNativeRBACRuleByID(ctx, id)
}

func (q *transactionalNativeRBACQ) DeleteNativeRBACRule(_ context.Context, id uuid.UUID) error {
	delete(q.rules, id)
	return nil
}

func (q *transactionalNativeRBACQ) UpsertAuditOutbox(_ context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	if q.outboxErr != nil {
		return sqlc.AuditOutbox{}, q.outboxErr
	}
	q.audits = append(q.audits, arg)
	return sqlc.AuditOutbox{ID: arg.ID, Action: arg.Action}, nil
}

func cloneNativeRules(source map[uuid.UUID]sqlc.NativeRbacRule) map[uuid.UUID]sqlc.NativeRbacRule {
	cloned := make(map[uuid.UUID]sqlc.NativeRbacRule, len(source))
	for id, rule := range source {
		cloned[id] = rule
	}
	return cloned
}

func fakeNativeRBACRunTx(q *transactionalNativeRBACQ) nativeRBACRunTxFunc {
	return func(_ context.Context, fn func(NativeRBACMutationTx) error) error {
		rules := cloneNativeRules(q.rules)
		audits := append([]sqlc.UpsertAuditOutboxParams(nil), q.audits...)
		created := q.created
		if err := fn(q); err != nil {
			q.rules, q.audits, q.created = rules, audits, created
			return err
		}
		return nil
	}
}

func TestNativeRBACAuditFailureRollsBackAndPreservesCache(t *testing.T) {
	targetUser, existingID := uuid.New(), uuid.New()
	for _, operation := range []string{"create", "delete"} {
		t.Run(operation, func(t *testing.T) {
			q := newTransactionalNativeRBACQ()
			if operation == "delete" {
				q.rules[existingID] = sqlc.NativeRbacRule{ID: existingID, UserID: targetUser, Resource: "secrets", Verbs: []string{"read"}}
			}
			q.outboxErr = errors.New("audit-SENTINEL")
			h := NewNativeRBACHandler(q)
			h.SetRunTx(fakeNativeRBACRunTx(q))
			invalidations := 0
			h.SetInvalidator(func(string) { invalidations++ })
			w := httptest.NewRecorder()
			if operation == "create" {
				body := `{"userId":"` + targetUser.String() + `","resource":"secrets","verbs":["read"]}`
				h.Create(w, httptest.NewRequest(http.MethodPost, "/api/v1/native-rbac-rules/", strings.NewReader(body)))
			} else {
				h.Delete(w, withURLParam(httptest.NewRequest(http.MethodDelete, "/api/v1/native-rbac-rules/"+existingID.String()+"/", nil), "id", existingID.String()))
			}
			if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "audit_unavailable") || strings.Contains(w.Body.String(), "audit-SENTINEL") {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if operation == "create" && len(q.rules) != 0 {
				t.Fatalf("create rollback retained rules=%+v", q.rules)
			}
			if operation == "delete" {
				if _, ok := q.rules[existingID]; !ok {
					t.Fatal("delete rollback removed native rule")
				}
			}
			if len(q.audits) != 0 || invalidations != 0 {
				t.Fatalf("rollback retained audits=%d invalidations=%d", len(q.audits), invalidations)
			}
		})
	}
}

func TestEveryNativeRBACMutationUsesTransactionalExecutor(t *testing.T) {
	path, err := filepath.Abs("native_rbac.go")
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"Create": false, "Delete": false}
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
			if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "executeNativeRBACMutation" {
				want[fn.Name.Name] = true
			}
			return true
		})
	}
	for name, found := range want {
		if !found {
			t.Errorf("%s does not use executeNativeRBACMutation", name)
		}
	}
}
