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

func TestMigration154RequiresExactApprovalDecisionIdentity(t *testing.T) {
	up, err := os.ReadFile("154_charlie_approval_decision_idempotency.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(up)
	for _, required := range []string{
		"decision_request_id UUID",
		"decision VARCHAR(8)",
		"ALTER COLUMN decision_request_id SET NOT NULL",
		"CHECK (decision IN ('approve', 'reject'))",
		"UNIQUE (connection_id, decision_request_id)",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("migration 154 is missing %q", required)
		}
	}
}

func TestMigration154BackfillsAndConstrainsApprovalDecisions(t *testing.T) {
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
	t.Cleanup(func() {
		if err := conn.Close(context.Background()); err != nil {
			t.Errorf("close migration test connection: %v", err)
		}
	})
	schema := "charlie_approval_idem_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	if _, err = conn.Exec(ctx, fmt.Sprintf("CREATE SCHEMA %s; SET search_path TO %s", schema, schema)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := conn.Exec(context.Background(), fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", schema)); err != nil {
			t.Errorf("drop migration test schema: %v", err)
		}
	})
	if _, err = conn.Exec(ctx, `CREATE TABLE charlie_action_approvals (
		id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
		connection_id uuid NOT NULL,
		state varchar(16) NOT NULL,
		rationale_digest varchar(128) NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	connectionA, connectionB := uuid.New(), uuid.New()
	if _, err = conn.Exec(ctx, `INSERT INTO charlie_action_approvals (connection_id, state, rationale_digest)
		VALUES ($1, 'approved', 'first'), ($1, 'rejected', 'second')`, connectionA); err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("154_charlie_approval_decision_idempotency.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	var total, distinctRequests, approvals, rejections int
	if err = conn.QueryRow(ctx, `SELECT count(*), count(DISTINCT decision_request_id),
		count(*) FILTER (WHERE decision = 'approve'), count(*) FILTER (WHERE decision = 'reject')
		FROM charlie_action_approvals`).Scan(&total, &distinctRequests, &approvals, &rejections); err != nil {
		t.Fatal(err)
	}
	if total != 2 || distinctRequests != 2 || approvals != 1 || rejections != 1 {
		t.Fatalf("unexpected backfill total=%d distinct=%d approve=%d reject=%d", total, distinctRequests, approvals, rejections)
	}
	requestID := uuid.New()
	if _, err = conn.Exec(ctx, `INSERT INTO charlie_action_approvals (connection_id, state, rationale_digest, decision_request_id, decision)
		VALUES ($1, 'pending', 'third', $2, 'approve')`, connectionA, requestID); err != nil {
		t.Fatalf("valid decision row rejected: %v", err)
	}
	for name, attempt := range map[string]func() error{
		"missing request": func() error {
			_, attemptErr := conn.Exec(ctx, `INSERT INTO charlie_action_approvals (connection_id, state, rationale_digest, decision) VALUES ($1, 'pending', 'x', 'approve')`, connectionA)
			return attemptErr
		},
		"invalid decision": func() error {
			_, attemptErr := conn.Exec(ctx, `INSERT INTO charlie_action_approvals (connection_id, state, rationale_digest, decision_request_id, decision) VALUES ($1, 'pending', 'x', gen_random_uuid(), 'deny')`, connectionA)
			return attemptErr
		},
		"reused request in deployment": func() error {
			_, attemptErr := conn.Exec(ctx, `INSERT INTO charlie_action_approvals (connection_id, state, rationale_digest, decision_request_id, decision) VALUES ($1, 'pending', 'x', $2, 'reject')`, connectionA, requestID)
			return attemptErr
		},
	} {
		if err = attempt(); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
	if _, err = conn.Exec(ctx, `INSERT INTO charlie_action_approvals (connection_id, state, rationale_digest, decision_request_id, decision)
		VALUES ($1, 'pending', 'isolated', $2, 'reject')`, connectionB, requestID); err != nil {
		t.Fatalf("a different deployment could not reuse its own request identifier: %v", err)
	}
}
