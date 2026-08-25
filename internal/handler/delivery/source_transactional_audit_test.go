package delivery

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

type stagedSourceMutationTx struct {
	SourceMutationTx
	row      sqlc.CreateDeliverySourceRow
	audits   []sqlc.UpsertAuditOutboxParams
	auditErr error
}

func (tx *stagedSourceMutationTx) CreateDeliverySource(_ context.Context, arg sqlc.CreateDeliverySourceParams) (sqlc.CreateDeliverySourceRow, error) {
	tx.row = sqlc.CreateDeliverySourceRow{ID: uuid.New(), ProjectID: arg.ProjectID, Name: arg.Name, SourceType: arg.SourceType, AuthMode: arg.AuthMode, CredentialKeyVersion: arg.CredentialKeyVersion}
	return tx.row, nil
}

func (tx *stagedSourceMutationTx) UpsertAuditOutbox(_ context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	if tx.auditErr != nil {
		return sqlc.AuditOutbox{}, tx.auditErr
	}
	tx.audits = append(tx.audits, arg)
	return sqlc.AuditOutbox{ID: arg.ID, Action: arg.Action}, nil
}

func TestDeliverySourceStateAndAuditCommitTogether(t *testing.T) {
	for _, tc := range []struct {
		name       string
		auditErr   error
		wantErr    bool
		wantCommit int
	}{
		{name: "commit", wantCommit: 1},
		{name: "audit failure rolls back encrypted source", auditErr: errors.New("audit unavailable"), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			committedRows, committedAudits := 0, 0
			h := NewSourceHandler(nil, nil, 1)
			h.SetRunTx(func(_ context.Context, fn func(SourceMutationTx) error) error {
				tx := &stagedSourceMutationTx{auditErr: tc.auditErr}
				if err := fn(tx); err != nil {
					return err
				}
				committedRows++
				committedAudits += len(tx.audits)
				return nil
			})
			r := httptest.NewRequest(http.MethodPost, "/api/v1/delivery/sources/", nil)
			params := sqlc.CreateDeliverySourceParams{ProjectID: uuid.New(), Name: "platform", SourceType: "git", AuthMode: "bearer", CredentialEncrypted: "ciphertext", CredentialKeyVersion: 1}

			_, err := executeSourceMutation(r, h,
				func(q SourceMutationTx) (sqlc.CreateDeliverySourceRow, error) {
					return q.CreateDeliverySource(r.Context(), params)
				},
				func() (sqlc.CreateDeliverySourceRow, error) {
					t.Fatal("production transaction unexpectedly used fallback")
					return sqlc.CreateDeliverySourceRow{}, nil
				},
				func(row sqlc.CreateDeliverySourceRow) deliveryAuditEvent {
					return deliveryAuditEvent{action: "delivery.source.created", resourceType: "delivery_source", resourceID: row.ID.String(), status: http.StatusCreated}
				})
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr=%v", err, tc.wantErr)
			}
			if committedRows != tc.wantCommit || committedAudits != tc.wantCommit {
				t.Fatalf("committed source/audit = %d/%d, want %d each", committedRows, committedAudits, tc.wantCommit)
			}
		})
	}
}

func TestEveryDeliverySourceMutationUsesTransactionalExecutor(t *testing.T) {
	path, err := filepath.Abs("source.go")
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"Create": false, "Update": false, "Delete": false, "Verify": false, "RotateCredential": false}
	for _, decl := range file.Decls {
		fnDecl, ok := decl.(*ast.FuncDecl)
		if !ok || fnDecl.Body == nil {
			continue
		}
		if _, tracked := want[fnDecl.Name.Name]; !tracked {
			continue
		}
		ast.Inspect(fnDecl.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch fn := call.Fun.(type) {
			case *ast.Ident:
				if fn.Name == "executeSourceMutation" {
					want[fnDecl.Name.Name] = true
				}
			case *ast.SelectorExpr:
				if fn.Sel.Name == "runTx" {
					want[fnDecl.Name.Name] = true
				}
			}
			return true
		})
	}
	for name, found := range want {
		if !found {
			t.Errorf("%s does not use executeSourceMutation", name)
		}
	}
}
