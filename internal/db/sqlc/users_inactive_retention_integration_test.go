package sqlc

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestDeactivateInactiveUsersPostgresSemantics(t *testing.T) {
	dsn := os.Getenv("INACTIVE_USER_RETENTION_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("INACTIVE_USER_RETENTION_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `
CREATE TEMP TABLE users (
  id uuid PRIMARY KEY, username text NOT NULL, email text NOT NULL,
  is_active boolean NOT NULL, is_superuser boolean NOT NULL, is_service boolean NOT NULL,
  last_login timestamptz, date_joined timestamptz NOT NULL, created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL, tokens_invalidated_at timestamptz
);
CREATE TEMP TABLE sso_sessions (jti uuid PRIMARY KEY, user_id uuid NOT NULL);
CREATE TEMP TABLE audit_log (
  source text NOT NULL, action text NOT NULL, resource_type text NOT NULL,
  resource_id text NOT NULL, resource_name text NOT NULL, detail jsonb NOT NULL,
  action_class text NOT NULL
);`)
	if err != nil {
		t.Fatalf("create temp schema: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	old := now.Add(-120 * 24 * time.Hour)
	recent := now.Add(-2 * 24 * time.Hour)
	ids := map[string]uuid.UUID{
		"old": uuid.New(), "never": uuid.New(), "super": uuid.New(),
		"service": uuid.New(), "inactive": uuid.New(), "recent": uuid.New(),
	}
	for name, id := range ids {
		joined, login, active, superuser, service := old, any(old), true, false, false
		switch name {
		case "never":
			login = nil
		case "super":
			superuser = true
		case "service":
			service = true
		case "inactive":
			active = false
		case "recent":
			joined, login = old, recent
		}
		_, err = tx.Exec(ctx, `INSERT INTO users
      (id,username,email,is_active,is_superuser,is_service,last_login,date_joined,created_at,updated_at)
      VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$8,$9)`,
			id, name, name+"@example.test", active, superuser, service, login, joined, recent)
		if err != nil {
			t.Fatalf("insert %s: %v", name, err)
		}
		_, err = tx.Exec(ctx, `INSERT INTO sso_sessions (jti,user_id) VALUES ($1,$2)`, uuid.New(), id)
		if err != nil {
			t.Fatalf("insert session %s: %v", name, err)
		}
	}

	rows, err := New(tx).DeactivateInactiveUsers(ctx, DeactivateInactiveUsersParams{
		InvalidatedAt: now, Cutoff: now.Add(-90 * 24 * time.Hour), RetentionDays: 90,
	})
	if err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("affected rows = %d, want 2: %+v", len(rows), rows)
	}
	affected := map[uuid.UUID]DeactivateInactiveUsersRow{}
	for _, row := range rows {
		affected[row.ID] = row
		if !row.TokensInvalidatedAt.Valid || !row.TokensInvalidatedAt.Time.Equal(now) {
			t.Errorf("user %s token cutoff = %+v, want %s", row.Username, row.TokensInvalidatedAt, now)
		}
		if row.SsoSessionsCleared != 1 {
			t.Errorf("user %s sessions cleared = %d, want 1", row.Username, row.SsoSessionsCleared)
		}
	}
	for _, name := range []string{"old", "never"} {
		if _, ok := affected[ids[name]]; !ok {
			t.Errorf("eligible user %s was not deactivated", name)
		}
	}
	for _, name := range []string{"super", "service", "inactive", "recent"} {
		if _, ok := affected[ids[name]]; ok {
			t.Errorf("excluded user %s was deactivated", name)
		}
	}
	var auditCount, remainingSessions int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM audit_log`).Scan(&auditCount); err != nil {
		t.Fatalf("count audits: %v", err)
	}
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM sso_sessions`).Scan(&remainingSessions); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if auditCount != 2 || remainingSessions != 4 {
		t.Fatalf("audit/session counts = %d/%d, want 2/4", auditCount, remainingSessions)
	}
}
