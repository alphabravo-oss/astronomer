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

type stagedClusterTemplateMutationTx struct {
	ClusterTemplateMutationTx
	application sqlc.ClusterTemplateApplication
	tasks       []sqlc.UpsertClusterTemplateApplicationWithTaskOutboxParams
	audits      []sqlc.UpsertAuditOutboxParams
	auditErr    error
}

func (tx *stagedClusterTemplateMutationTx) UpsertClusterTemplateApplicationWithTaskOutbox(_ context.Context, arg sqlc.UpsertClusterTemplateApplicationWithTaskOutboxParams) (sqlc.ClusterTemplateApplication, error) {
	tx.tasks = append(tx.tasks, arg)
	tx.application = sqlc.ClusterTemplateApplication{
		ClusterID: arg.ClusterID, TemplateID: arg.TemplateID,
		SpecSnapshot: arg.SpecSnapshot, Status: ClusterTemplateStatusPending,
	}
	return tx.application, nil
}

func (tx *stagedClusterTemplateMutationTx) UpsertAuditOutbox(_ context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	if tx.auditErr != nil {
		return sqlc.AuditOutbox{}, tx.auditErr
	}
	tx.audits = append(tx.audits, arg)
	return sqlc.AuditOutbox{ID: arg.ID, Action: arg.Action}, nil
}

func TestClusterTemplateApplicationTaskAndAuditCommitTogether(t *testing.T) {
	for _, tc := range []struct {
		name       string
		auditErr   error
		wantErr    bool
		wantCommit int
	}{
		{name: "commit application task and audit", wantCommit: 1},
		{name: "audit failure rolls back application and task", auditErr: errors.New("audit unavailable"), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			committedApplications, committedTasks, committedAudits := 0, 0, 0
			h := NewClusterTemplateHandler(nil)
			h.SetRunTx(func(_ context.Context, fn func(ClusterTemplateMutationTx) error) error {
				tx := &stagedClusterTemplateMutationTx{auditErr: tc.auditErr}
				if err := fn(tx); err != nil {
					return err
				}
				if tx.application.ClusterID != uuid.Nil {
					committedApplications++
				}
				committedTasks += len(tx.tasks)
				committedAudits += len(tx.audits)
				return nil
			})
			r := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/test/template/", nil)
			params := sqlc.UpsertClusterTemplateApplicationParams{
				ClusterID: uuid.New(), TemplateID: uuid.New(), SpecSnapshot: []byte(`{"environment":"production"}`),
			}

			_, err := executeClusterTemplateMutation(r, h,
				func(q ClusterTemplateMutationTx) (sqlc.ClusterTemplateApplication, error) {
					return upsertClusterTemplateApplicationAndTask(r, q, params)
				},
				func() (sqlc.ClusterTemplateApplication, error) {
					t.Fatal("production transaction unexpectedly used fallback")
					return sqlc.ClusterTemplateApplication{}, nil
				},
				func(row sqlc.ClusterTemplateApplication) clusterAuditEvent {
					return clusterAuditEvent{action: "cluster.template_applied", resourceType: "cluster", resourceID: row.ClusterID.String(), status: http.StatusAccepted}
				})
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr=%v", err, tc.wantErr)
			}
			if committedApplications != tc.wantCommit || committedTasks != tc.wantCommit || committedAudits != tc.wantCommit {
				t.Fatalf("committed application/task/audit = %d/%d/%d, want %d each", committedApplications, committedTasks, committedAudits, tc.wantCommit)
			}
		})
	}
}

func TestEveryClusterTemplateMutationUsesTransactionalExecutor(t *testing.T) {
	path, err := filepath.Abs("cluster_templates.go")
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"Create": false, "Update": false, "Delete": false,
		"Apply": false, "Reapply": false, "Detach": false,
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
			if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "executeClusterTemplateMutation" {
				want[fn.Name.Name] = true
			}
			return true
		})
	}
	for name, found := range want {
		if !found {
			t.Errorf("%s does not use executeClusterTemplateMutation", name)
		}
	}
}
