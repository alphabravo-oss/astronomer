package auth

import (
	"context"
	"log/slog"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type ensureAdminFakeQuerier struct {
	count               int64
	params              sqlc.CreateBootstrapAdminParams
	forcePasswordChange bool
}

func (q *ensureAdminFakeQuerier) CountUsers(context.Context) (int64, error) {
	return q.count, nil
}

func (q *ensureAdminFakeQuerier) CreateBootstrapAdmin(_ context.Context, arg sqlc.CreateBootstrapAdminParams) (sqlc.User, error) {
	q.params = arg
	return sqlc.User{
		ID:       uuid.New(),
		Email:    arg.Email,
		Username: arg.Username,
	}, nil
}

func (q *ensureAdminFakeQuerier) SetMustChangePassword(context.Context, uuid.UUID) error {
	q.forcePasswordChange = true
	return nil
}

func TestEnsureBootstrapAdminUsesDefaultIdentity(t *testing.T) {
	t.Setenv(bootstrapPasswordEnv, "test-password")
	q := &ensureAdminFakeQuerier{}

	if err := EnsureBootstrapAdmin(context.Background(), q, slog.Default()); err != nil {
		t.Fatalf("EnsureBootstrapAdmin returned error: %v", err)
	}

	if q.params.Username != "admin" {
		t.Fatalf("Username = %q, want admin", q.params.Username)
	}
	if q.params.Email != "admin@astronomer.local" {
		t.Fatalf("Email = %q, want admin@astronomer.local", q.params.Email)
	}
	if q.params.Password == "" || q.params.Password == "test-password" {
		t.Fatalf("Password was not bcrypt hashed")
	}
}

func TestEnsureBootstrapAdminUsesConfiguredIdentity(t *testing.T) {
	t.Setenv(bootstrapPasswordEnv, "test-password")
	t.Setenv(bootstrapUsernameEnv, "root")
	t.Setenv(bootstrapEmailEnv, "admin@alphabravo.io")
	q := &ensureAdminFakeQuerier{}

	if err := EnsureBootstrapAdmin(context.Background(), q, slog.Default()); err != nil {
		t.Fatalf("EnsureBootstrapAdmin returned error: %v", err)
	}

	if q.params.Username != "root" {
		t.Fatalf("Username = %q, want root", q.params.Username)
	}
	if q.params.Email != "admin@alphabravo.io" {
		t.Fatalf("Email = %q, want admin@alphabravo.io", q.params.Email)
	}
}

func TestEnsureBootstrapAdminCanRequirePasswordChange(t *testing.T) {
	t.Setenv(bootstrapPasswordEnv, "test-password")
	t.Setenv(bootstrapForcePasswordChangeEnv, "true")
	q := &ensureAdminFakeQuerier{}

	if err := EnsureBootstrapAdmin(context.Background(), q, slog.Default()); err != nil {
		t.Fatalf("EnsureBootstrapAdmin returned error: %v", err)
	}
	if !q.forcePasswordChange {
		t.Fatal("bootstrap admin was not marked for password change")
	}
}
