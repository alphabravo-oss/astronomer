//go:build integration

package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/testdb"
)

func TestExpiredTokensDoNotConsumeQuota(t *testing.T) {
	t.Parallel()
	pool, err := pgxpool.New(context.Background(), testdb.DSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	q := sqlc.New(pool)
	user, err := q.CreateUser(t.Context(), sqlc.CreateUserParams{Email: "quota@example.test", Username: "quota", Password: "test-only", IsActive: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		expiry time.Time
	}{{"expired", time.Now().Add(-time.Hour)}, {"active", time.Now().Add(time.Hour)}} {
		if _, err := q.CreateAPIToken(t.Context(), sqlc.CreateAPITokenParams{UserID: user.ID, Name: tc.name, TokenHash: tc.name, Prefix: tc.name, Scopes: []byte(`[]`), ExpiresAt: pgtype.Timestamptz{Time: tc.expiry, Valid: true}}); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := q.CountActiveTokensForUser(t.Context(), user.ID); err != nil || n != 1 {
		t.Fatalf("quota count=%d err=%v", n, err)
	}
	if n, err := q.CountTokensByUser(t.Context(), user.ID); err != nil || n != 2 {
		t.Fatalf("listed token count=%d err=%v", n, err)
	}
}
