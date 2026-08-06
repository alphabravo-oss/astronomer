package migrations_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestMigration153CorrectsCharlieAlertDeepLinkConstraint(t *testing.T) {
	up, err := os.ReadFile("153_charlie_alert_deep_link_constraint.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(up)
	if !strings.Contains(text, `^/dashboard/charlie\?tab=findings&finding=[0-9a-f-]{36}$`) ||
		strings.Contains(text, `^/dashboard/charlie\\?tab=findings&finding=[0-9a-f-]{36}$`) {
		t.Fatal("migration 153 does not install the literal-question-mark deep-link constraint")
	}
	if !strings.Contains(text, "DROP CONSTRAINT charlie_alert_delivery_deep_link") ||
		!strings.Contains(text, "ADD CONSTRAINT charlie_alert_delivery_deep_link") {
		t.Fatal("migration 153 does not replace the existing constraint atomically")
	}
}

func TestMigration153AcceptsOnlyCanonicalFindingDeepLinks(t *testing.T) {
	dsn := os.Getenv("CHARLIE_ALERT_POLICY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("CHARLIE_ALERT_POLICY_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	schema := "charlie_alert_link_" + uuid.NewString()[:8]
	if _, err = conn.Exec(ctx, fmt.Sprintf("CREATE SCHEMA %s; SET search_path TO %s", schema, schema)); err != nil {
		t.Fatal(err)
	}
	defer conn.Exec(context.Background(), fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", schema))
	if _, err = conn.Exec(ctx, `CREATE TABLE charlie_alert_deliveries (
		deep_link text NOT NULL,
		CONSTRAINT charlie_alert_delivery_deep_link CHECK (deep_link <> '')
	)`); err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("153_charlie_alert_deep_link_constraint.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	valid := "/dashboard/charlie?tab=findings&finding=" + uuid.NewString()
	if _, err = conn.Exec(ctx, `INSERT INTO charlie_alert_deliveries VALUES ($1)`, valid); err != nil {
		t.Fatalf("canonical finding deep link rejected: %v", err)
	}
	for _, invalid := range []string{
		"/dashboard/charlieXtab=findings&finding=" + uuid.NewString(),
		"/dashboard/charlie?tab=findings&finding=not-a-uuid",
		"https://external.invalid/dashboard/charlie?tab=findings&finding=" + uuid.NewString(),
	} {
		if _, err = conn.Exec(ctx, `INSERT INTO charlie_alert_deliveries VALUES ($1)`, invalid); err == nil {
			t.Fatalf("non-canonical finding deep link accepted: %q", invalid)
		}
	}
}
