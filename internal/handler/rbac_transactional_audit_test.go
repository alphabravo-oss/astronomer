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

type stagedRBACMutationTx struct {
	RBACMutationTx
	role      sqlc.GlobalRole
	auditRows []sqlc.UpsertAuditOutboxParams
	auditErr  error
}

func (tx *stagedRBACMutationTx) CreateGlobalRole(_ context.Context, arg sqlc.CreateGlobalRoleParams) (sqlc.GlobalRole, error) {
	tx.role = sqlc.GlobalRole{ID: uuid.New(), Name: arg.Name, DisplayName: arg.DisplayName}
	return tx.role, nil
}

func (tx *stagedRBACMutationTx) UpsertAuditOutbox(_ context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	if tx.auditErr != nil {
		return sqlc.AuditOutbox{}, tx.auditErr
	}
	tx.auditRows = append(tx.auditRows, arg)
	return sqlc.AuditOutbox{ID: arg.ID, Action: arg.Action, ResourceType: arg.ResourceType}, nil
}

func TestExecuteRBACMutationCommitsRoleAndAuditTogether(t *testing.T) {
	for _, tc := range []struct {
		name       string
		auditErr   error
		wantErr    bool
		wantRoles  int
		wantAudits int
	}{
		{name: "commit", wantRoles: 1, wantAudits: 1},
		{name: "audit failure rolls back privilege change", auditErr: errors.New("audit unavailable"), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			committedRoles := []sqlc.GlobalRole{}
			committedAudits := []sqlc.UpsertAuditOutboxParams{}
			h := &RBACHandler{}
			h.SetRunTx(func(ctx context.Context, fn func(RBACMutationTx) error) error {
				tx := &stagedRBACMutationTx{auditErr: tc.auditErr}
				if err := fn(tx); err != nil {
					return err
				}
				committedRoles = append(committedRoles, tx.role)
				committedAudits = append(committedAudits, tx.auditRows...)
				return nil
			})
			r := httptest.NewRequest(http.MethodPost, "/api/v1/rbac/global-roles/", nil)
			params := sqlc.CreateGlobalRoleParams{Name: "incident-responder", DisplayName: "Incident Responder"}

			_, err := executeRBACMutation(r, h,
				func(q RBACMutationTx) (sqlc.GlobalRole, error) { return q.CreateGlobalRole(r.Context(), params) },
				func() (sqlc.GlobalRole, error) {
					t.Fatal("production transaction unexpectedly used fallback")
					return sqlc.GlobalRole{}, nil
				},
				func(role sqlc.GlobalRole) rbacAuditEvent {
					return rbacAuditEvent{action: "role.create", resourceType: "global_role", resourceID: role.ID.String(), resourceName: role.Name, status: http.StatusCreated}
				})
			if (err != nil) != tc.wantErr {
				t.Fatalf("executeRBACMutation error = %v, wantErr=%v", err, tc.wantErr)
			}
			if len(committedRoles) != tc.wantRoles || len(committedAudits) != tc.wantAudits {
				t.Fatalf("committed roles/audits = %d/%d, want %d/%d", len(committedRoles), len(committedAudits), tc.wantRoles, tc.wantAudits)
			}
			if len(committedAudits) == 1 && (committedAudits[0].Action != "role.create" || committedAudits[0].ResourceType != "global_role") {
				t.Fatalf("audit intent = %#v", committedAudits[0])
			}
		})
	}
}

func TestCreateGlobalRoleReturnsAuditUnavailableAndRollsBack(t *testing.T) {
	committed := false
	h := &RBACHandler{}
	h.SetRunTx(func(ctx context.Context, fn func(RBACMutationTx) error) error {
		tx := &stagedRBACMutationTx{auditErr: errors.New("postgres unavailable")}
		if err := fn(tx); err != nil {
			return err
		}
		committed = true
		return nil
	})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/rbac/global-roles/", strings.NewReader(`{"name":"incident-responder","permissions":{},"rules":[]}`))
	w := httptest.NewRecorder()

	h.CreateGlobalRole(w, r)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", w.Code, w.Body.String())
	}
	if committed {
		t.Fatal("role transaction committed despite audit failure")
	}
	if !strings.Contains(w.Body.String(), `"code":"audit_unavailable"`) {
		t.Fatalf("response does not expose audit_unavailable: %s", w.Body.String())
	}
}

func TestEveryRBACPrivilegeMutationUsesTransactionalExecutor(t *testing.T) {
	path, err := filepath.Abs("rbac.go")
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"CreateGlobalRole": false, "UpdateGlobalRole": false, "DeleteGlobalRole": false,
		"CreateClusterRole": false, "UpdateClusterRole": false, "DeleteClusterRole": false,
		"CreateProjectRole": false, "UpdateProjectRole": false, "DeleteProjectRole": false,
		"CreateGlobalRoleBinding": false, "DeleteGlobalRoleBinding": false,
		"CreateClusterRoleBinding": false, "DeleteClusterRoleBinding": false,
		"CreateProjectRoleBinding": false, "DeleteProjectRoleBinding": false,
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
			if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "executeRBACMutation" {
				want[fn.Name.Name] = true
			}
			return true
		})
	}
	for name, found := range want {
		if !found {
			t.Errorf("%s does not use executeRBACMutation", name)
		}
	}
}
