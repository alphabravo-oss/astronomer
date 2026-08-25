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

type stagedCloudCredentialMutationTx struct {
	CloudCredentialMutationTx
	row      sqlc.CloudCredential
	tasks    int
	audits   []sqlc.UpsertAuditOutboxParams
	taskErr  error
	auditErr error
}

func (tx *stagedCloudCredentialMutationTx) CreateCloudCredential(_ context.Context, arg sqlc.CreateCloudCredentialParams) (sqlc.CloudCredential, error) {
	tx.row = sqlc.CloudCredential{ID: uuid.New(), ProjectID: arg.ProjectID, Name: arg.Name, Provider: arg.Provider, DataEncrypted: arg.DataEncrypted, TargetRefs: arg.TargetRefs}
	return tx.row, nil
}

func (tx *stagedCloudCredentialMutationTx) UpsertCloudCredentialMaterializationWithTaskOutbox(_ context.Context, arg sqlc.UpsertCloudCredentialMaterializationWithTaskOutboxParams) (sqlc.CloudCredentialMaterialization, error) {
	if tx.taskErr != nil {
		return sqlc.CloudCredentialMaterialization{}, tx.taskErr
	}
	tx.tasks++
	return sqlc.CloudCredentialMaterialization{CredentialID: arg.CredentialID, ClusterID: arg.ClusterID, Namespace: arg.Namespace, SecretName: arg.SecretName}, nil
}

func (tx *stagedCloudCredentialMutationTx) UpsertAuditOutbox(_ context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	if tx.auditErr != nil {
		return sqlc.AuditOutbox{}, tx.auditErr
	}
	tx.audits = append(tx.audits, arg)
	return sqlc.AuditOutbox{ID: arg.ID, Action: arg.Action}, nil
}

func TestCloudCredentialCommitsEncryptedStateTaskAndAuditTogether(t *testing.T) {
	for _, tc := range []struct {
		name       string
		taskErr    error
		auditErr   error
		wantErr    bool
		wantCommit int
	}{
		{name: "commit all", wantCommit: 1},
		{name: "task intent failure rolls back all", taskErr: errors.New("task outbox unavailable"), wantErr: true},
		{name: "audit intent failure rolls back all", auditErr: errors.New("audit outbox unavailable"), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			committedRows, committedTasks, committedAudits := 0, 0, 0
			h := NewCloudCredentialHandler(nil)
			h.SetRunTx(func(_ context.Context, fn func(CloudCredentialMutationTx) error) error {
				tx := &stagedCloudCredentialMutationTx{taskErr: tc.taskErr, auditErr: tc.auditErr}
				if err := fn(tx); err != nil {
					return err
				}
				committedRows++
				committedTasks += tx.tasks
				committedAudits += len(tx.audits)
				return nil
			})
			r := httptest.NewRequest(http.MethodPost, "/api/v1/projects/p/cloud-credentials/", nil)
			projectID, clusterID := uuid.New(), uuid.New()
			params := sqlc.CreateCloudCredentialParams{ProjectID: projectID, Name: "aws-prod", Provider: "aws", DataEncrypted: "ciphertext"}
			refs := []TargetRef{{ClusterID: clusterID, Namespace: "apps", SecretName: "aws-prod"}}

			_, err := executeCloudCredentialMutation(r, h,
				func(q CloudCredentialMutationTx) (sqlc.CloudCredential, error) {
					row, mutationErr := q.CreateCloudCredential(r.Context(), params)
					if mutationErr != nil {
						return sqlc.CloudCredential{}, mutationErr
					}
					return row, h.stageMaterializationRefs(r.Context(), q, row, refs, "apply")
				},
				func() (sqlc.CloudCredential, error) {
					t.Fatal("production transaction unexpectedly used fallback")
					return sqlc.CloudCredential{}, nil
				},
				func(row sqlc.CloudCredential) clusterAuditEvent {
					return clusterAuditEvent{action: "cloud_credentials.created", resourceType: "cloud_credential", resourceID: row.ID.String(), status: http.StatusCreated}
				})
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr=%v", err, tc.wantErr)
			}
			if committedRows != tc.wantCommit || committedTasks != tc.wantCommit || committedAudits != tc.wantCommit {
				t.Fatalf("committed state/task/audit = %d/%d/%d, want %d each", committedRows, committedTasks, committedAudits, tc.wantCommit)
			}
		})
	}
}

func TestCloudCredentialDeleteDedupeSeparatesOperationsAndCredentials(t *testing.T) {
	ref := TargetRef{ClusterID: uuid.New(), Namespace: "apps", SecretName: "cloud-creds"}
	firstCredential, secondCredential := uuid.New(), uuid.New()
	first := cloudCredentialMaterializeDedupeKey(firstCredential, ref, "delete", "request-1")
	if repeated := cloudCredentialMaterializeDedupeKey(firstCredential, ref, "delete", "request-2"); repeated == first {
		t.Fatal("a later delete operation must not be suppressed by a previously delivered task")
	}
	if other := cloudCredentialMaterializeDedupeKey(secondCredential, ref, "delete", "request-1"); other == first {
		t.Fatal("different credentials targeting the same Secret must not share a delete dedupe key")
	}
}

func TestEveryCloudCredentialMutationUsesTransactionalExecutor(t *testing.T) {
	path, err := filepath.Abs("cloud_credentials.go")
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"Create": false, "Update": false, "Delete": false}
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
			if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "executeCloudCredentialMutation" {
				want[fn.Name.Name] = true
			}
			return true
		})
	}
	for name, found := range want {
		if !found {
			t.Errorf("%s does not use executeCloudCredentialMutation", name)
		}
	}
}
