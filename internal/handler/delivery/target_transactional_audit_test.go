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

type stagedTargetMutationTx struct {
	TargetMutationTx
	row      sqlc.DeliveryTarget
	audits   []sqlc.UpsertAuditOutboxParams
	auditErr error
}

func (tx *stagedTargetMutationTx) CreateDeliveryTarget(_ context.Context, arg sqlc.CreateDeliveryTargetParams) (sqlc.DeliveryTarget, error) {
	tx.row = sqlc.DeliveryTarget{ID: uuid.New(), ProjectID: arg.ProjectID, Name: arg.Name, BundleVersionID: arg.BundleVersionID, Generation: 1, ResourceVersion: 1}
	return tx.row, nil
}

func (tx *stagedTargetMutationTx) UpsertAuditOutbox(_ context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	if tx.auditErr != nil {
		return sqlc.AuditOutbox{}, tx.auditErr
	}
	tx.audits = append(tx.audits, arg)
	return sqlc.AuditOutbox{ID: arg.ID, Action: arg.Action}, nil
}

func TestDeliveryTargetStateAndAuditCommitTogether(t *testing.T) {
	for _, tc := range []struct {
		name       string
		auditErr   error
		wantErr    bool
		wantCommit int
	}{
		{name: "commit", wantCommit: 1},
		{name: "audit failure rolls back target", auditErr: errors.New("audit unavailable"), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			committedRows, committedAudits := 0, 0
			h := NewTargetHandler(nil, nil, nil)
			h.SetRunTx(func(_ context.Context, fn func(TargetMutationTx) error) error {
				tx := &stagedTargetMutationTx{auditErr: tc.auditErr}
				if err := fn(tx); err != nil {
					return err
				}
				committedRows++
				committedAudits += len(tx.audits)
				return nil
			})
			r := httptest.NewRequest(http.MethodPost, "/api/v1/delivery/targets/", nil)
			params := sqlc.CreateDeliveryTargetParams{ProjectID: uuid.New(), Name: "production", BundleVersionID: uuid.New()}

			_, err := executeTargetMutation(r, h,
				func(q TargetMutationTx) (sqlc.DeliveryTarget, error) {
					return q.CreateDeliveryTarget(r.Context(), params)
				},
				func() (sqlc.DeliveryTarget, error) {
					t.Fatal("production transaction unexpectedly used fallback")
					return sqlc.DeliveryTarget{}, nil
				},
				func(row sqlc.DeliveryTarget) deliveryAuditEvent {
					return deliveryAuditEvent{action: "delivery.target.created", resourceType: "delivery_target", resourceID: row.ID.String(), status: http.StatusCreated}
				})
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr=%v", err, tc.wantErr)
			}
			if committedRows != tc.wantCommit || committedAudits != tc.wantCommit {
				t.Fatalf("committed target/audit = %d/%d, want %d each", committedRows, committedAudits, tc.wantCommit)
			}
		})
	}
}

func TestEveryDeliveryTargetMutationUsesTransactionalExecutor(t *testing.T) {
	path, err := filepath.Abs("target.go")
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"Create": false, "Update": false, "Delete": false, "Orphan": false}
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
				if fn.Name == "executeTargetMutation" {
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
			t.Errorf("%s does not use executeTargetMutation", name)
		}
	}
}
