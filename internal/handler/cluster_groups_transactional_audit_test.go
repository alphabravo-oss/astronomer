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

type stagedClusterGroupMutationTx struct {
	ClusterGroupMutationTx
	group    sqlc.ClusterGroup
	audits   []sqlc.UpsertAuditOutboxParams
	auditErr error
}

func (tx *stagedClusterGroupMutationTx) CreateClusterGroup(_ context.Context, arg sqlc.CreateClusterGroupParams) (sqlc.ClusterGroup, error) {
	tx.group = sqlc.ClusterGroup{ID: uuid.New(), Name: arg.Name, Slug: arg.Slug}
	return tx.group, nil
}

func (tx *stagedClusterGroupMutationTx) UpsertAuditOutbox(_ context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	if tx.auditErr != nil {
		return sqlc.AuditOutbox{}, tx.auditErr
	}
	tx.audits = append(tx.audits, arg)
	return sqlc.AuditOutbox{ID: arg.ID, Action: arg.Action}, nil
}

func TestClusterGroupStateAndAllAuditsCommitTogether(t *testing.T) {
	for _, tc := range []struct {
		name       string
		auditErr   error
		wantErr    bool
		wantCommit int
	}{
		{name: "commit group and audits", wantCommit: 1},
		{name: "audit failure rolls back group", auditErr: errors.New("audit unavailable"), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			committedGroups, committedAudits := 0, 0
			h := NewClusterGroupHandler(nil)
			h.SetRunTx(func(_ context.Context, fn func(ClusterGroupMutationTx) error) error {
				tx := &stagedClusterGroupMutationTx{auditErr: tc.auditErr}
				if err := fn(tx); err != nil {
					return err
				}
				if tx.group.ID != uuid.Nil {
					committedGroups++
				}
				committedAudits += len(tx.audits)
				return nil
			})
			r := httptest.NewRequest(http.MethodPost, "/api/v1/cluster-groups/", nil)
			params := sqlc.CreateClusterGroupParams{Name: "production", Slug: "production"}

			_, err := executeClusterGroupMutation(r, h,
				func(q ClusterGroupMutationTx) (sqlc.ClusterGroup, error) {
					return q.CreateClusterGroup(r.Context(), params)
				},
				func() (sqlc.ClusterGroup, error) {
					t.Fatal("production transaction unexpectedly used fallback")
					return sqlc.ClusterGroup{}, nil
				},
				func(row sqlc.ClusterGroup) []clusterAuditEvent {
					return []clusterAuditEvent{
						{action: "admin.cluster_group.created", resourceType: "cluster_group", resourceID: row.ID.String(), status: http.StatusCreated},
						{action: "admin.cluster_group.moved_cluster", resourceType: "cluster", resourceID: uuid.NewString(), status: http.StatusCreated},
					}
				})
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr=%v", err, tc.wantErr)
			}
			wantAudits := 2 * tc.wantCommit
			if committedGroups != tc.wantCommit || committedAudits != wantAudits {
				t.Fatalf("committed group/audits = %d/%d, want %d/%d", committedGroups, committedAudits, tc.wantCommit, wantAudits)
			}
		})
	}
}

func TestEveryClusterGroupMutationUsesTransactionalExecutor(t *testing.T) {
	path, err := filepath.Abs("cluster_groups.go")
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"Create": false, "Update": false, "Delete": false, "MoveClusters": false}
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
			if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "executeClusterGroupMutation" {
				want[fn.Name.Name] = true
			}
			return true
		})
	}
	for name, found := range want {
		if !found {
			t.Errorf("%s does not use executeClusterGroupMutation", name)
		}
	}
}
