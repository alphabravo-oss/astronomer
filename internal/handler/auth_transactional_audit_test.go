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
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type stagedAuthMutationTx struct {
	AuthMutationTx
	token    sqlc.ApiToken
	revoked  bool
	invalid  bool
	failed   bool
	audits   []sqlc.UpsertAuditOutboxParams
	auditErr error
}

func (tx *stagedAuthMutationTx) RecordFailedLoginAttempt(_ context.Context, arg sqlc.RecordFailedLoginAttemptParams) (sqlc.User, error) {
	tx.failed = true
	row := sqlc.User{ID: arg.ID, Username: "operator", FailedLoginCount: arg.LockoutThreshold}
	row.LockedUntil = arg.LockedUntil
	row.LockedReason = arg.LockedReason
	return row, nil
}

func (tx *stagedAuthMutationTx) RevokeJWT(context.Context, sqlc.RevokeJWTParams) error {
	tx.revoked = true
	return nil
}

func (tx *stagedAuthMutationTx) InvalidateAllTokens(context.Context, sqlc.InvalidateAllTokensParams) error {
	tx.invalid = true
	return nil
}

func (tx *stagedAuthMutationTx) CreateAPIToken(_ context.Context, arg sqlc.CreateAPITokenParams) (sqlc.ApiToken, error) {
	tx.token = sqlc.ApiToken{ID: uuid.New(), UserID: arg.UserID, Name: arg.Name, Prefix: arg.Prefix}
	return tx.token, nil
}

func (tx *stagedAuthMutationTx) UpsertAuditOutbox(_ context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	if tx.auditErr != nil {
		return sqlc.AuditOutbox{}, tx.auditErr
	}
	tx.audits = append(tx.audits, arg)
	return sqlc.AuditOutbox{ID: arg.ID, Action: arg.Action}, nil
}

func TestSelfServiceTokenAndAuditCommitTogether(t *testing.T) {
	for _, tc := range []struct {
		name       string
		auditErr   error
		wantErr    bool
		wantCommit int
	}{
		{name: "commit", wantCommit: 1},
		{name: "audit failure rolls back token", auditErr: errors.New("audit unavailable"), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			committedTokens, committedAudits := 0, 0
			h := &AuthHandler{}
			h.SetRunTx(func(_ context.Context, fn func(AuthMutationTx) error) error {
				tx := &stagedAuthMutationTx{auditErr: tc.auditErr}
				if err := fn(tx); err != nil {
					return err
				}
				committedTokens++
				committedAudits += len(tx.audits)
				return nil
			})
			r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/tokens/", nil)
			params := sqlc.CreateAPITokenParams{UserID: uuid.New(), Name: "automation", Prefix: "astro_abcd"}

			_, err := executeAuthMutation(r, h,
				func(q AuthMutationTx) (sqlc.ApiToken, error) { return q.CreateAPIToken(r.Context(), params) },
				func() (sqlc.ApiToken, error) {
					t.Fatal("production transaction unexpectedly used fallback")
					return sqlc.ApiToken{}, nil
				},
				func(token sqlc.ApiToken) clusterAuditEvent {
					return clusterAuditEvent{action: "auth.token.create", resourceType: "api_token", resourceID: token.ID.String(), status: http.StatusCreated}
				})
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr=%v", err, tc.wantErr)
			}
			if committedTokens != tc.wantCommit || committedAudits != tc.wantCommit {
				t.Fatalf("committed token/audit = %d/%d, want %d each", committedTokens, committedAudits, tc.wantCommit)
			}
		})
	}
}

func TestLogoutRevocationCutoffAndAuditCommitTogether(t *testing.T) {
	for _, tc := range []struct {
		name       string
		auditErr   error
		wantCommit int
	}{
		{name: "commit", wantCommit: 1},
		{name: "audit failure rolls back both revocation writes", auditErr: errors.New("audit unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			committedRevokes, committedCutoffs, committedAudits := 0, 0, 0
			h := &AuthHandler{}
			h.SetRunTx(func(_ context.Context, fn func(AuthMutationTx) error) error {
				tx := &stagedAuthMutationTx{auditErr: tc.auditErr}
				if err := fn(tx); err != nil {
					return err
				}
				if tx.revoked {
					committedRevokes++
				}
				if tx.invalid {
					committedCutoffs++
				}
				committedAudits += len(tx.audits)
				return nil
			})
			r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout/", nil)
			userID := uuid.New()
			_, err := executeAuthMutation(r, h,
				func(q AuthMutationTx) (logoutMutationResult, error) {
					if err := q.RevokeJWT(r.Context(), sqlc.RevokeJWTParams{Jti: "jti-1", UserID: userID}); err != nil {
						return logoutMutationResult{}, err
					}
					if err := q.InvalidateAllTokens(r.Context(), sqlc.InvalidateAllTokensParams{ID: userID}); err != nil {
						return logoutMutationResult{}, err
					}
					return logoutMutationResult{jti: "jti-1", userID: userID}, nil
				},
				func() (logoutMutationResult, error) {
					t.Fatal("production transaction unexpectedly used fallback")
					return logoutMutationResult{}, nil
				},
				func(result logoutMutationResult) clusterAuditEvent {
					return clusterAuditEvent{action: "auth.logout", resourceType: "user", resourceID: result.userID.String(), status: http.StatusOK}
				})
			if tc.auditErr == nil && err != nil {
				t.Fatal(err)
			}
			if tc.auditErr != nil && err == nil {
				t.Fatal("expected audit failure")
			}
			if committedRevokes != tc.wantCommit || committedCutoffs != tc.wantCommit || committedAudits != tc.wantCommit {
				t.Fatalf("committed revoke/cutoff/audit = %d/%d/%d, want %d each", committedRevokes, committedCutoffs, committedAudits, tc.wantCommit)
			}
		})
	}
}

func TestFailedLoginCounterLockAndAuditCommitTogether(t *testing.T) {
	for _, tc := range []struct {
		name       string
		auditErr   error
		wantCommit int
	}{
		{name: "commit", wantCommit: 1},
		{name: "audit failure rolls back lockout transition", auditErr: errors.New("audit unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			committedAttempts, committedAudits := 0, 0
			h := &AuthHandler{}
			h.SetLockoutPolicy(3, 15*time.Minute)
			h.SetRunTx(func(_ context.Context, fn func(AuthMutationTx) error) error {
				tx := &stagedAuthMutationTx{auditErr: tc.auditErr}
				if err := fn(tx); err != nil {
					return err
				}
				if tx.failed {
					committedAttempts++
				}
				committedAudits += len(tx.audits)
				return nil
			})
			r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login/", nil)
			userID := uuid.New()
			user := sqlc.User{ID: userID, Email: "operator@example.com", Username: "operator", FailedLoginCount: 2,
				LockedUntil: pgtype.Timestamptz{}}

			err := h.handleFailedAttempt(r.Context(), r, user, "bad_password")
			if tc.auditErr == nil && err != nil {
				t.Fatal(err)
			}
			if tc.auditErr != nil && err == nil {
				t.Fatal("expected audit failure")
			}
			if committedAttempts != tc.wantCommit || committedAudits != tc.wantCommit {
				t.Fatalf("committed attempt/audit = %d/%d, want %d each", committedAttempts, committedAudits, tc.wantCommit)
			}
		})
	}
}

func TestEverySelfServiceCredentialMutationUsesTransactionalExecutor(t *testing.T) {
	path, err := filepath.Abs("auth.go")
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"ChangePassword": false, "CreateToken": false, "RevokeToken": false, "Logout": false}
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
			if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "executeAuthMutation" {
				want[fn.Name.Name] = true
			}
			return true
		})
	}
	for name, found := range want {
		if !found {
			t.Errorf("%s does not use executeAuthMutation", name)
		}
	}
}
