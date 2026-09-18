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
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/reqctx"

	"github.com/go-chi/chi/v5"
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
	type coverage struct{ tx, audit, sessions bool }
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
				if value.Sel.Name == "mutateUser" {
					state.tx = true
				}
			case *ast.CallExpr:
				if ident, ok := value.Fun.(*ast.Ident); ok {
					switch ident.Name {
					case "recordAuditOutbox":
						state.audit = true
					case "invalidateUserSessionsTx":
						state.sessions = true
					}
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
	for _, name := range []string{"UpdateUser", "DeleteUser", "ResetUserPassword", "ForceLogoutUser"} {
		if !want[name].sessions {
			t.Errorf("%s does not atomically revoke local and SSO sessions", name)
		}
	}
}

func TestAdministrativeUserMutationsFailClosedWithoutTransactionRunner(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		body   string
		invoke func(*ResourceHandler, http.ResponseWriter, *http.Request)
	}{
		{name: "create", method: http.MethodPost, path: "/api/v1/users/", body: `{}`, invoke: (*ResourceHandler).CreateUser},
		{name: "update", method: http.MethodPatch, path: "/api/v1/users/bad/", body: `{}`, invoke: (*ResourceHandler).UpdateUser},
		{name: "delete", method: http.MethodDelete, path: "/api/v1/users/bad/", invoke: (*ResourceHandler).DeleteUser},
		{name: "reset password", method: http.MethodPost, path: "/api/v1/users/bad/reset-password/", body: `{}`, invoke: (*ResourceHandler).ResetUserPassword},
		{name: "unlock", method: http.MethodPost, path: "/api/v1/admin/users/bad/unlock/", invoke: (*ResourceHandler).UnlockUser},
		{name: "force logout", method: http.MethodPost, path: "/api/v1/admin/users/bad/force-logout/", invoke: (*ResourceHandler).ForceLogoutUser},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := &ResourceHandler{queries: &transactionalUserBaseQuerier{}}
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			recorder := httptest.NewRecorder()

			tc.invoke(h, recorder, req)

			if recorder.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503; body=%s", recorder.Code, recorder.Body.String())
			}
			if !strings.Contains(recorder.Body.String(), `"code":"runner_unwired"`) {
				t.Fatalf("response = %s, want runner_unwired", recorder.Body.String())
			}
		})
	}
}

type stagedUserRevocationTx struct {
	UserMutationTx
	user                  sqlc.User
	sessions              []sqlc.SsoSession
	auditErr              error
	invalidationAttempted bool
	deleteAttempted       bool
}

func (tx *stagedUserRevocationTx) GetUserByIDForUpdate(_ context.Context, _ uuid.UUID) (sqlc.User, error) {
	return tx.user, nil
}

func (tx *stagedUserRevocationTx) ListSSOSessionsByUser(_ context.Context, _ uuid.UUID) ([]sqlc.SsoSession, error) {
	return append([]sqlc.SsoSession(nil), tx.sessions...), nil
}

func (tx *stagedUserRevocationTx) InvalidateAllTokens(_ context.Context, _ sqlc.InvalidateAllTokensParams) error {
	tx.invalidationAttempted = true
	return nil
}

func (tx *stagedUserRevocationTx) DeleteSSOSessionsByUser(_ context.Context, _ uuid.UUID) error {
	tx.deleteAttempted = true
	return nil
}

func (tx *stagedUserRevocationTx) UpsertAuditOutbox(_ context.Context, _ sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	return sqlc.AuditOutbox{}, tx.auditErr
}

func TestForceLogoutRollsBackRevocationAndSessionsWhenAuditFails(t *testing.T) {
	admin := sqlc.User{ID: uuid.New(), Email: "admin@example.com", Username: "admin", IsActive: true, IsSuperuser: true}
	target := sqlc.User{ID: uuid.New(), Email: "target@example.com", Username: "target", IsActive: true}
	queries := &resourceQuerierForceLogout{users: map[uuid.UUID]sqlc.User{admin.ID: admin, target.ID: target}}
	h := NewResourceHandlerWithQueries(queries, nil)
	backchannel := &recordingBackchannel{}
	h.SetSSOBackchannelClient(backchannel)
	h.SetEncryptor(newTestEncryptor(t))

	tx := &stagedUserRevocationTx{
		user:     target,
		auditErr: errors.New("audit unavailable"),
		sessions: []sqlc.SsoSession{{
			Jti: "session", UserID: target.ID, ProviderName: "oidc",
			UpstreamIDTokenEncrypted: "not-used-before-commit",
			EndSessionEndpoint:       "https://identity.example.com/logout",
			ExpiresAt:                time.Now().Add(time.Hour),
		}},
	}
	committedInvalidation := false
	committedSessionDelete := false
	h.SetUserRunTx(func(_ context.Context, fn func(UserMutationTx) error) error {
		if err := fn(tx); err != nil {
			return err
		}
		committedInvalidation = tx.invalidationAttempted
		committedSessionDelete = tx.deleteAttempted
		return nil
	})

	router := chi.NewRouter()
	router.Post("/api/v1/admin/users/{id}/force-logout/", h.ForceLogoutUser)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/users/"+target.ID.String()+"/force-logout/", nil)
	req = req.WithContext(reqctx.WithUser(req.Context(), &reqctx.User{ID: admin.ID.String(), AuthMethod: "jwt"}))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", recorder.Code, recorder.Body.String())
	}
	if !tx.invalidationAttempted || !tx.deleteAttempted {
		t.Fatal("transaction did not reach revocation and SSO-session deletion before the audit failure")
	}
	if committedInvalidation || committedSessionDelete {
		t.Fatalf("rolled-back state committed: invalidation=%v sessionDelete=%v", committedInvalidation, committedSessionDelete)
	}
	if len(backchannel.calls) != 0 {
		t.Fatalf("post-commit backchannel calls = %d, want 0", len(backchannel.calls))
	}
	if len(queries.invalidated) != 0 {
		t.Fatalf("direct invalidation writes = %d, want 0", len(queries.invalidated))
	}
}
