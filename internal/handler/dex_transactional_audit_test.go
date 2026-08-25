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

type stagedDexMutationTx struct {
	DexMutationTx
	connector sqlc.StageCreateDexConnectorRow
	audits    []sqlc.UpsertAuditOutboxParams
	auditErr  error
}

func (tx *stagedDexMutationTx) StageCreateDexConnector(_ context.Context, arg sqlc.StageCreateDexConnectorParams) (sqlc.StageCreateDexConnectorRow, error) {
	tx.connector = sqlc.StageCreateDexConnectorRow{
		ID: uuid.New(), Name: arg.Name, Type: arg.Type, DisplayName: arg.DisplayName,
		Config: arg.Config, Enabled: arg.Enabled, RuntimeGeneration: 8,
	}
	return tx.connector, nil
}

func (tx *stagedDexMutationTx) UpsertAuditOutbox(_ context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	if tx.auditErr != nil {
		return sqlc.AuditOutbox{}, tx.auditErr
	}
	tx.audits = append(tx.audits, arg)
	return sqlc.AuditOutbox{ID: arg.ID, Action: arg.Action}, nil
}

func TestDexConnectorStageAndAuditCommitTogether(t *testing.T) {
	for _, tc := range []struct {
		name       string
		auditErr   error
		wantErr    bool
		wantCommit int
	}{
		{name: "commit", wantCommit: 1},
		{name: "audit failure restores connector and SSO generation", auditErr: errors.New("audit unavailable"), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			committedConnectors, committedAudits := 0, 0
			h := &DexHandler{}
			h.SetRunTx(func(_ context.Context, fn func(DexMutationTx) error) error {
				tx := &stagedDexMutationTx{auditErr: tc.auditErr}
				if err := fn(tx); err != nil {
					return err
				}
				committedConnectors++
				committedAudits += len(tx.audits)
				return nil
			})
			r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/dex/connectors/", nil)
			params := sqlc.StageCreateDexConnectorParams{Name: "okta", Type: "oidc", Enabled: true}

			_, err := executeDexMutation(r, h,
				func(q DexMutationTx) (sqlc.StageCreateDexConnectorRow, error) {
					return q.StageCreateDexConnector(r.Context(), params)
				},
				func() (sqlc.StageCreateDexConnectorRow, error) {
					t.Fatal("production transaction unexpectedly used fallback")
					return sqlc.StageCreateDexConnectorRow{}, nil
				},
				func(row sqlc.StageCreateDexConnectorRow) clusterAuditEvent {
					return clusterAuditEvent{action: "dex.connector.create", resourceType: "dex_connector", resourceID: row.ID.String(), status: http.StatusCreated}
				})
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr=%v", err, tc.wantErr)
			}
			if committedConnectors != tc.wantCommit || committedAudits != tc.wantCommit {
				t.Fatalf("committed connector/audit = %d/%d, want %d each", committedConnectors, committedAudits, tc.wantCommit)
			}
		})
	}
}

func TestEveryDexConfigurationMutationUsesTransactionalExecutor(t *testing.T) {
	path, err := filepath.Abs("dex_config.go")
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"CreateConnector": false, "UpdateConnector": false, "DeleteConnector": false, "UpdateSettings": false,
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
			if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "executeDexMutation" {
				want[fn.Name.Name] = true
			}
			return true
		})
	}
	for name, found := range want {
		if !found {
			t.Errorf("%s does not use executeDexMutation", name)
		}
	}
}
