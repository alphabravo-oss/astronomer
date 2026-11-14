//go:build integration

package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/identity"
	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
	"github.com/alphabravocompany/astronomer-go/internal/testdb"
)

func TestIdentityBindingAdministrationIsAtomicAndRevokesSessions(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := testdb.Pool(t)
	q := sqlc.New(pool)
	user, err := q.CreateUser(ctx, sqlc.CreateUserParams{Email: "binding@example.test", Username: "binding", IsActive: true})
	if err != nil {
		t.Fatal(err)
	}
	other, err := q.CreateUser(ctx, sqlc.CreateUserParams{Email: "other-binding@example.test", Username: "other-binding", IsActive: true})
	if err != nil {
		t.Fatal(err)
	}
	svc := identity.New(config.IdentitySettings{Mode: "both", Issuer: "https://identity.example.test"}, q, nil)
	manager := auth.MustNewJWTManager("test-identity-binding-signing-key", 60)
	manager.SetIdentityValidator(svc.ValidateSession)
	h := NewSSOHandler(svc, manager, "")
	failCommit := false
	h.SetRunTx(func(ctx context.Context, fn func(SSOCallbackTx) error) error {
		tx, e := pool.Begin(ctx)
		if e != nil {
			return e
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if e = fn(sqlc.New(tx)); e != nil {
			return e
		}
		if failCommit {
			return errors.New("injected transaction failure")
		}
		return tx.Commit(ctx)
	})
	call := func(method string, id uuid.UUID, subject string, authenticated bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/api/v1/settings/identity/bindings/", strings.NewReader(fmt.Sprintf(`{"user_id":%q,"subject":%q}`, id.String(), subject)))
		if authenticated {
			r = r.WithContext(reqctx.WithUser(ctx, &reqctx.User{ID: user.ID.String()}))
		}
		w := httptest.NewRecorder()
		h.Bindings(w, r)
		return w
	}
	if got := call("POST", user.ID, "subject", false).Code; got != 401 {
		t.Fatalf("anonymous binding status %d", got)
	}
	failCommit = true
	if got := call("POST", user.ID, "subject", true).Code; got != 409 {
		t.Fatalf("failed commit status %d", got)
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM shared_identity_bindings`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed transaction leaked link: %v", err)
	}
	failCommit = false
	if got := call("POST", user.ID, "subject", true).Code; got != 200 {
		t.Fatalf("binding status %d", got)
	}
	if got := call("POST", other.ID, "subject", true).Code; got != 409 {
		t.Fatalf("subject transferred to another account: %d", got)
	}
	token, err := manager.GenerateIdentitySession(ctx, user.ID, svc.Issuer(), "subject", time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = manager.ValidateTokenContext(ctx, token); err != nil {
		t.Fatal(err)
	}
	if got := call("GET", user.ID, "", true); got.Code != 200 || !strings.Contains(got.Body.String(), "subject") {
		t.Fatal("linked account not listed")
	}
	// An administrator must be able to unlink a disabled account too.
	if _, err = pool.Exec(ctx, `UPDATE users SET is_active=false WHERE id=$1`, user.ID); err != nil {
		t.Fatal(err)
	}
	if got := call("DELETE", user.ID, "subject", true).Code; got != 200 {
		t.Fatalf("unlink disabled account status %d", got)
	}
	if _, err = pool.Exec(ctx, `UPDATE users SET is_active=true WHERE id=$1`, user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = manager.ValidateTokenContext(ctx, token); err == nil {
		t.Fatal("removed binding still authenticates cached token")
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM audit_outbox WHERE action IN ('identity.binding_created','identity.binding_deleted')`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("expected exactly two committed binding audits, got %d: %v", count, err)
	}
}
