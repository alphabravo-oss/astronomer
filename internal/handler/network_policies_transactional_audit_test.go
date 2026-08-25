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
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
)

type stagedNetworkPolicyMutationTx struct {
	NetworkPolicyMutationTx
	applications []sqlc.NetworkPolicyApplication
	tasks        []sqlc.UpsertTaskOutboxParams
	audits       []sqlc.UpsertAuditOutboxParams
	auditErr     error
}

func (tx *stagedNetworkPolicyMutationTx) UpsertNetworkPolicyApplication(_ context.Context, arg sqlc.UpsertNetworkPolicyApplicationParams) (sqlc.NetworkPolicyApplication, error) {
	row := sqlc.NetworkPolicyApplication{
		ID: uuid.New(), TemplateID: arg.TemplateID, ClusterID: arg.ClusterID,
		Namespace: arg.Namespace, PolicyName: arg.PolicyName, Status: "pending",
	}
	tx.applications = append(tx.applications, row)
	return row, nil
}

func (tx *stagedNetworkPolicyMutationTx) UpsertTaskOutbox(_ context.Context, arg sqlc.UpsertTaskOutboxParams) (sqlc.TaskOutbox, error) {
	tx.tasks = append(tx.tasks, arg)
	return sqlc.TaskOutbox{ID: uuid.New(), TaskType: arg.TaskType}, nil
}

func (tx *stagedNetworkPolicyMutationTx) UpsertAuditOutbox(_ context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	if tx.auditErr != nil {
		return sqlc.AuditOutbox{}, tx.auditErr
	}
	tx.audits = append(tx.audits, arg)
	return sqlc.AuditOutbox{ID: arg.ID, Action: arg.Action}, nil
}

func TestNetworkPolicyBatchTasksAndAuditCommitTogether(t *testing.T) {
	for _, tc := range []struct {
		name       string
		auditErr   error
		wantErr    bool
		wantCommit int
	}{
		{name: "commit batch tasks and audit", wantCommit: 1},
		{name: "audit failure rolls back batch and tasks", auditErr: errors.New("audit unavailable"), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			committedApplications, committedTasks, committedAudits := 0, 0, 0
			h := NewNetworkPolicyHandler(nil)
			h.SetRunTx(func(_ context.Context, fn func(NetworkPolicyMutationTx) error) error {
				tx := &stagedNetworkPolicyMutationTx{auditErr: tc.auditErr}
				if err := fn(tx); err != nil {
					return err
				}
				committedApplications += len(tx.applications)
				committedTasks += len(tx.tasks)
				committedAudits += len(tx.audits)
				return nil
			})
			r := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/test/network-policies/applications/", nil)
			clusterID, templateID := uuid.New(), uuid.New()
			namespaces := []string{"payments", "orders"}

			_, err := executeNetworkPolicyMutation(r, h,
				func(q NetworkPolicyMutationTx) (networkPolicyApplyResult, error) {
					result := networkPolicyApplyResult{}
					for _, namespace := range namespaces {
						row, upsertErr := q.UpsertNetworkPolicyApplication(r.Context(), sqlc.UpsertNetworkPolicyApplicationParams{
							TemplateID: templateID, ClusterID: clusterID, Namespace: namespace, PolicyName: "astronomer-default-deny",
						})
						if upsertErr != nil {
							return networkPolicyApplyResult{}, upsertErr
						}
						if taskErr := enqueueNetworkPolicyApplyTaskOutbox(r, q, row.ID); taskErr != nil {
							return networkPolicyApplyResult{}, taskErr
						}
						result.applications = append(result.applications, row)
						result.namespaces = append(result.namespaces, namespace)
					}
					return result, nil
				},
				func() (networkPolicyApplyResult, error) {
					t.Fatal("production transaction unexpectedly used fallback")
					return networkPolicyApplyResult{}, nil
				},
				func(networkPolicyApplyResult) clusterAuditEvent {
					return clusterAuditEvent{action: "cluster.network_policy.applied", resourceType: "cluster", resourceID: clusterID.String(), status: http.StatusAccepted}
				})
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr=%v", err, tc.wantErr)
			}
			wantRows := len(namespaces) * tc.wantCommit
			if committedApplications != wantRows || committedTasks != wantRows || committedAudits != tc.wantCommit {
				t.Fatalf("committed applications/tasks/audits = %d/%d/%d, want %d/%d/%d", committedApplications, committedTasks, committedAudits, wantRows, wantRows, tc.wantCommit)
			}
		})
	}
}

func TestEveryNetworkPolicyMutationUsesTransactionalExecutor(t *testing.T) {
	path, err := filepath.Abs("network_policies.go")
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"CreateTemplate": false, "UpdateTemplate": false, "DeleteTemplate": false,
		"CreateApplications": false, "DeleteApplication": false, "Reapply": false,
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
			if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "executeNetworkPolicyMutation" {
				want[fn.Name.Name] = true
			}
			return true
		})
	}
	for name, found := range want {
		if !found {
			t.Errorf("%s does not use executeNetworkPolicyMutation", name)
		}
	}
}

var _ tasks.TaskOutboxWriter = (*stagedNetworkPolicyMutationTx)(nil)
