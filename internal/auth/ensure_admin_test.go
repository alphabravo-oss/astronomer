package auth

import (
	"context"
	"log/slog"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type ensureAdminFakeQuerier struct {
	count  int64
	params sqlc.CreateBootstrapAdminParams
}

func (q *ensureAdminFakeQuerier) CountUsers(context.Context) (int64, error) {
	return q.count, nil
}

func (q *ensureAdminFakeQuerier) CreateBootstrapAdmin(_ context.Context, arg sqlc.CreateBootstrapAdminParams) (sqlc.User, error) {
	q.params = arg
	return sqlc.User{
		Email:    arg.Email,
		Username: arg.Username,
	}, nil
}

func TestEnsureBootstrapAdminUsesDefaultIdentity(t *testing.T) {
	q := &ensureAdminFakeQuerier{}

	if err := EnsureBootstrapAdmin(context.Background(), q, BootstrapAdminConfig{Password: "test-password"}, slog.Default()); err != nil {
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
	q := &ensureAdminFakeQuerier{}

	if err := EnsureBootstrapAdmin(context.Background(), q, BootstrapAdminConfig{
		Password: "test-password", Username: "root", Email: "admin@alphabravo.io",
	}, slog.Default()); err != nil {
		t.Fatalf("EnsureBootstrapAdmin returned error: %v", err)
	}

	if q.params.Username != "root" {
		t.Fatalf("Username = %q, want root", q.params.Username)
	}
	if q.params.Email != "admin@alphabravo.io" {
		t.Fatalf("Email = %q, want admin@alphabravo.io", q.params.Email)
	}
}
