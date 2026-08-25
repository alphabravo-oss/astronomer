package handler

import (
	"bytes"
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
	"github.com/alphabravocompany/astronomer-go/internal/notify"
)

func fakeNotificationTemplateRunTx(q *fakeNotifyQuerier) notificationTemplateRunTxFunc {
	return func(_ context.Context, fn func(NotificationTemplateMutationTx) error) error {
		rows := make(map[string]sqlc.NotificationTemplate, len(q.rows))
		for key, row := range q.rows {
			rows[key] = row
		}
		audits := append([]string(nil), q.auditOps...)
		if err := fn(q); err != nil {
			q.rows, q.auditOps = rows, audits
			return err
		}
		return nil
	}
}

func TestNotificationTemplateAuditFailureRollsBackMutations(t *testing.T) {
	for _, operation := range []string{"update", "delete"} {
		t.Run(operation, func(t *testing.T) {
			h, q, ctx := newNotifyHandler(t)
			key := notify.KeyEmailAccountLocked
			if operation == "delete" {
				q.rows[key] = sqlc.NotificationTemplate{ID: uuid.New(), TemplateKey: key, BodyTpl: "original", Enabled: true}
			}
			q.outboxErr = errors.New("audit-SENTINEL")
			h.SetRunTx(fakeNotificationTemplateRunTx(q))
			var req *http.Request
			if operation == "update" {
				req = httptest.NewRequest(http.MethodPut, "/", bytes.NewBufferString(`{"body":"replacement"}`))
			} else {
				req = httptest.NewRequest(http.MethodDelete, "/", nil)
			}
			req = withNotifyURLParam(req.WithContext(ctx), "key", key)
			w := httptest.NewRecorder()

			if operation == "update" {
				h.Update(w, req)
			} else {
				h.Delete(w, req)
			}

			if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "audit_unavailable") || strings.Contains(w.Body.String(), "audit-SENTINEL") {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if operation == "update" && len(q.rows) != 0 {
				t.Fatalf("update rollback retained rows: %+v", q.rows)
			}
			if operation == "delete" && q.rows[key].BodyTpl != "original" {
				t.Fatalf("delete rollback lost row: %+v", q.rows)
			}
			if len(q.auditOps) != 0 {
				t.Fatalf("rollback retained audits=%v", q.auditOps)
			}
		})
	}
}

func TestEveryNotificationTemplateMutationUsesTransactionalExecutor(t *testing.T) {
	path, err := filepath.Abs("notification_templates.go")
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"Update": false, "Delete": false}
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
			if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "executeNotificationTemplateMutation" {
				want[fn.Name.Name] = true
			}
			return true
		})
	}
	for name, found := range want {
		if !found {
			t.Errorf("%s does not use executeNotificationTemplateMutation", name)
		}
	}
}
