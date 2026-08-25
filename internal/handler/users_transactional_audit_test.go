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

type transactionalUserBaseQuerier struct{ ResourceQuerier }

type stagedUserMutationTx struct {
	UserMutationTx
	user      sqlc.User
	auditRows []sqlc.UpsertAuditOutboxParams
	auditErr  error
}

func (tx *stagedUserMutationTx) CreateUser(_ context.Context, arg sqlc.CreateUserParams) (sqlc.User, error) {
	tx.user = sqlc.User{
		ID: uuid.New(), Email: arg.Email, Username: arg.Username,
		IsActive: arg.IsActive, IsStaff: arg.IsStaff, IsSuperuser: arg.IsSuperuser,
	}
	return tx.user, nil
}

func (tx *stagedUserMutationTx) UpsertAuditOutbox(_ context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	if tx.auditErr != nil {
		return sqlc.AuditOutbox{}, tx.auditErr
	}
	tx.auditRows = append(tx.auditRows, arg)
	return sqlc.AuditOutbox{ID: arg.ID, Action: arg.Action, ResourceType: arg.ResourceType}, nil
}

func TestCreateUserCommitsIdentityAndAuditTogether(t *testing.T) {
	for _, tc := range []struct {
		name       string
		auditErr   error
		wantStatus int
		wantCommit bool
	}{
		{name: "commit", wantStatus: http.StatusCreated, wantCommit: true},
		{name: "audit failure", auditErr: errors.New("postgres unavailable"), wantStatus: http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			committedUsers := []sqlc.User{}
			committedAudits := []sqlc.UpsertAuditOutboxParams{}
			h := &ResourceHandler{queries: &transactionalUserBaseQuerier{}}
			h.SetUserRunTx(func(ctx context.Context, fn func(UserMutationTx) error) error {
				tx := &stagedUserMutationTx{auditErr: tc.auditErr}
				if err := fn(tx); err != nil {
					return err
				}
				committedUsers = append(committedUsers, tx.user)
				committedAudits = append(committedAudits, tx.auditRows...)
				return nil
			})
			r := httptest.NewRequest(http.MethodPost, "/api/v1/users/", strings.NewReader(`{
				"email":"operator@example.com","username":"operator","password":"Correct-Horse-9472!","is_superuser":true
			}`))
			w := httptest.NewRecorder()

			h.CreateUser(w, r)

			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", w.Code, tc.wantStatus, w.Body.String())
			}
			if (len(committedUsers) == 1) != tc.wantCommit || (len(committedAudits) == 1) != tc.wantCommit {
				t.Fatalf("committed users/audits = %d/%d, wantCommit=%v", len(committedUsers), len(committedAudits), tc.wantCommit)
			}
			if tc.auditErr != nil && !strings.Contains(w.Body.String(), `"code":"audit_unavailable"`) {
				t.Fatalf("audit failure response = %s", w.Body.String())
			}
			if len(committedAudits) == 1 && committedAudits[0].Action != "user.create" {
				t.Fatalf("audit intent = %#v", committedAudits[0])
			}
		})
	}
}

func TestEveryAdministrativeUserMutationHasTransactionalAuditPath(t *testing.T) {
	path, err := filepath.Abs("users.go")
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	type coverage struct{ tx, audit bool }
	want := map[string]coverage{
		"CreateUser": {}, "UpdateUser": {}, "DeleteUser": {},
		"ResetUserPassword": {}, "UnlockUser": {}, "ForceLogoutUser": {},
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		state, tracked := want[fn.Name.Name]
		if !tracked {
			continue
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			switch value := node.(type) {
			case *ast.SelectorExpr:
				if value.Sel.Name == "userRunTx" {
					state.tx = true
				}
			case *ast.CallExpr:
				if ident, ok := value.Fun.(*ast.Ident); ok && ident.Name == "recordAuditOutbox" {
					state.audit = true
				}
			}
			return true
		})
		want[fn.Name.Name] = state
	}
	for name, state := range want {
		if !state.tx || !state.audit {
			t.Errorf("%s transactional coverage = %+v, want tx+audit", name, state)
		}
	}
}
