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
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type stagedDashboardMutationTx struct {
	DashboardMutationTx
	widget   sqlc.DashboardWidget
	audits   []sqlc.UpsertAuditOutboxParams
	auditErr error
}

func (tx *stagedDashboardMutationTx) CreateDashboardWidget(_ context.Context, arg sqlc.CreateDashboardWidgetParams) (sqlc.DashboardWidget, error) {
	tx.widget = sqlc.DashboardWidget{ID: uuid.New(), Name: arg.Name, WidgetType: arg.WidgetType, Scope: arg.Scope}
	return tx.widget, nil
}

func (tx *stagedDashboardMutationTx) UpsertAuditOutbox(_ context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	if tx.auditErr != nil {
		return sqlc.AuditOutbox{}, tx.auditErr
	}
	tx.audits = append(tx.audits, arg)
	return sqlc.AuditOutbox{ID: arg.ID, Action: arg.Action}, nil
}

func TestDashboardStateAndAuditCommitTogether(t *testing.T) {
	for _, tc := range []struct {
		name       string
		auditErr   error
		wantErr    bool
		wantCommit int
	}{
		{name: "commit widget and audit", wantCommit: 1},
		{name: "audit failure rolls back widget", auditErr: errors.New("audit unavailable"), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			committedWidgets, committedAudits := 0, 0
			h := NewDashboardHandler(nil)
			h.SetRunTx(func(_ context.Context, fn func(DashboardMutationTx) error) error {
				tx := &stagedDashboardMutationTx{auditErr: tc.auditErr}
				if err := fn(tx); err != nil {
					return err
				}
				if tx.widget.ID != uuid.Nil {
					committedWidgets++
				}
				committedAudits += len(tx.audits)
				return nil
			})
			r := httptest.NewRequest(http.MethodPost, "/api/v1/admin/dashboard-widgets/", nil)
			params := sqlc.CreateDashboardWidgetParams{Name: "health", WidgetType: "prom_stat", Scope: "global"}

			_, err := executeDashboardMutation(r, h,
				func(q DashboardMutationTx) (sqlc.DashboardWidget, error) {
					return q.CreateDashboardWidget(r.Context(), params)
				},
				func() (sqlc.DashboardWidget, error) {
					t.Fatal("production transaction unexpectedly used fallback")
					return sqlc.DashboardWidget{}, nil
				},
				func(row sqlc.DashboardWidget) clusterAuditEvent {
					return clusterAuditEvent{
						action: "admin.dashboard_widget.created", resourceType: "dashboard_widget",
						resourceID: row.ID.String(), resourceName: row.Name, status: http.StatusCreated,
					}
				})
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr=%v", err, tc.wantErr)
			}
			if committedWidgets != tc.wantCommit || committedAudits != tc.wantCommit {
				t.Fatalf("committed widgets/audits = %d/%d, want %d/%d", committedWidgets, committedAudits, tc.wantCommit, tc.wantCommit)
			}
		})
	}
}

func TestEveryDashboardMutationUsesTransactionalExecutor(t *testing.T) {
	path, err := filepath.Abs("dashboards.go")
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"AdminCreate": false, "AdminUpdate": false, "AdminDelete": false,
		"AdminCreateDatasource": false, "AdminUpdateDatasource": false, "AdminDeleteDatasource": false,
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
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
			if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "executeDashboardMutation" {
				want[fn.Name.Name] = true
			}
			return true
		})
	}
	for name, found := range want {
		if !found {
			t.Errorf("%s does not use executeDashboardMutation", name)
		}
	}
}
