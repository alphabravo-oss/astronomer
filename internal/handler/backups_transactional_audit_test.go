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

type stagedBackupMutationTx struct {
	BackupMutationTx
	backup   sqlc.Backup
	audits   []sqlc.UpsertAuditOutboxParams
	auditErr error
}

func (tx *stagedBackupMutationTx) CreateBackup(_ context.Context, arg sqlc.CreateBackupParams) (sqlc.Backup, error) {
	tx.backup = sqlc.Backup{ID: uuid.New(), Name: arg.Name, StorageID: arg.StorageID, BackupType: arg.BackupType, Status: arg.Status}
	return tx.backup, nil
}

func (tx *stagedBackupMutationTx) UpsertAuditOutbox(_ context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	if tx.auditErr != nil {
		return sqlc.AuditOutbox{}, tx.auditErr
	}
	tx.audits = append(tx.audits, arg)
	return sqlc.AuditOutbox{ID: arg.ID, Action: arg.Action}, nil
}

func TestBackupStateAndAuditCommitTogether(t *testing.T) {
	for _, tc := range []struct {
		name       string
		auditErr   error
		wantErr    bool
		wantCommit int
	}{
		{name: "commit", wantCommit: 1},
		{name: "audit failure rolls back backup", auditErr: errors.New("audit unavailable"), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			committedRows, committedAudits := 0, 0
			h := NewBackupHandler(nil)
			h.SetRunTx(func(_ context.Context, fn func(BackupMutationTx) error) error {
				tx := &stagedBackupMutationTx{auditErr: tc.auditErr}
				if err := fn(tx); err != nil {
					return err
				}
				committedRows++
				committedAudits += len(tx.audits)
				return nil
			})
			r := httptest.NewRequest(http.MethodPost, "/api/v1/backups/", nil)
			params := sqlc.CreateBackupParams{Name: "production", StorageID: uuid.New(), BackupType: "full", Status: "pending"}

			_, err := executeMutation(r, h.runTx,
				func(q BackupMutationTx) (sqlc.Backup, error) { return q.CreateBackup(r.Context(), params) },
				func(row sqlc.Backup) mutationAuditEvent {
					return mutationAuditEvent{action: "backup.create", resourceType: "backup", resourceID: row.ID.String(), status: http.StatusCreated}
				})
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr=%v", err, tc.wantErr)
			}
			if committedRows != tc.wantCommit || committedAudits != tc.wantCommit {
				t.Fatalf("committed backup/audit = %d/%d, want %d each", committedRows, committedAudits, tc.wantCommit)
			}
		})
	}
}

func TestEveryBackupMutationUsesTransactionalExecutor(t *testing.T) {
	paths, err := filepath.Glob("backups*.go")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"CreateStorageConfig": false, "DeleteStorageConfig": false, "UpdateStorageConfig": false,
		"CreateBackup": false, "DeleteBackup": false, "CreateSchedule": false,
		"DeleteSchedule": false, "UpdateSchedule": false, "TriggerSchedule": false, "CreateRestoreByBackup": false,
	}
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if parseErr != nil {
			t.Fatal(parseErr)
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
				if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "executeMutation" {
					want[fn.Name.Name] = true
				}
				return true
			})
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("%s does not use executeMutation", name)
		}
	}
}
