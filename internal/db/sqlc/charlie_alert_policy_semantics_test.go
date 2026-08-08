package sqlc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// This opt-in PostgreSQL test pins the revision semantics that cannot be
// faithfully exercised by a query mock. Release/CI rehearsals can provide a
// disposable database through CHARLIE_ALERT_POLICY_TEST_DATABASE_URL.
func TestUpsertCharlieAlertPolicyRevisionSemantics(t *testing.T) {
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
	schema := "charlie_alert_policy_" + uuid.NewString()[:8]
	if _, err = conn.Exec(ctx, fmt.Sprintf("CREATE SCHEMA %s; SET search_path TO %s", schema, schema)); err != nil {
		t.Fatal(err)
	}
	defer conn.Exec(context.Background(), fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", schema))
	if _, err = conn.Exec(ctx, `
CREATE TABLE charlie_alert_policies (
    connection_id uuid PRIMARY KEY,
    enabled boolean NOT NULL DEFAULT true,
    minimum_severity varchar(16) NOT NULL DEFAULT 'medium',
    dedupe_window_seconds integer NOT NULL DEFAULT 1800,
    escalation_after_seconds integer NOT NULL DEFAULT 3600,
    quiet_hours_enabled boolean NOT NULL DEFAULT false,
    quiet_hours_start char(5) NOT NULL DEFAULT '22:00',
    quiet_hours_end char(5) NOT NULL DEFAULT '07:00',
    quiet_hours_timezone varchar(64) NOT NULL DEFAULT 'UTC',
    revision bigint NOT NULL DEFAULT 1,
    updated_by_id uuid,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
)`); err != nil {
		t.Fatal(err)
	}
	queries := New(conn)
	connectionID := uuid.New()
	params := UpsertCharlieAlertPolicyParams{
		ConnectionID: connectionID, Enabled: true, MinimumSeverity: "high",
		DedupeWindowSeconds: 900, EscalationAfterSeconds: 3600,
		QuietHoursEnabled: true, QuietHoursStart: "22:00", QuietHoursEnd: "07:00",
		QuietHoursTimezone: "UTC", UpdatedByID: pgtype.UUID{}, ExpectedRevision: 0,
	}
	created, err := queries.UpsertCharlieAlertPolicy(ctx, params)
	if err != nil || created.Revision != 1 {
		t.Fatalf("absent revision-zero create revision=%d err=%v", created.Revision, err)
	}
	params.ExpectedRevision = created.Revision
	params.MinimumSeverity = "critical"
	updated, err := queries.UpsertCharlieAlertPolicy(ctx, params)
	if err != nil || updated.Revision != 2 || updated.MinimumSeverity != "critical" {
		t.Fatalf("exact update row=%+v err=%v", updated, err)
	}
	params.ExpectedRevision = 1
	if _, err = queries.UpsertCharlieAlertPolicy(ctx, params); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("stale existing update err=%v want no rows", err)
	}
	params.ConnectionID = uuid.New()
	params.ExpectedRevision = 9
	if _, err = queries.UpsertCharlieAlertPolicy(ctx, params); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("absent nonzero update err=%v want no rows", err)
	}
}

func TestCharlieAlertReconcileCandidatesRequireFindingScope(t *testing.T) {
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
	schema := "charlie_alert_scope_" + uuid.NewString()[:8]
	if _, err = conn.Exec(ctx, fmt.Sprintf("CREATE SCHEMA %s; SET search_path TO %s", schema, schema)); err != nil {
		t.Fatal(err)
	}
	defer conn.Exec(context.Background(), fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", schema))
	for _, statement := range []string{`
CREATE TABLE charlie_connections (
    id uuid PRIMARY KEY, active boolean NOT NULL, emergency_disabled boolean NOT NULL,
    requested_mode text NOT NULL, verified_mode text NOT NULL,
    installation_id uuid NOT NULL, product_id text NOT NULL, product_slug text NOT NULL,
    deployment_id text NOT NULL, route_id text NOT NULL, central_url text NOT NULL,
    central_ca_fingerprint text NOT NULL, signing_key_id text NOT NULL,
    signing_key_fingerprint text NOT NULL, logical_agent_id text NOT NULL
)`, `CREATE TABLE charlie_alert_policies (
    connection_id uuid PRIMARY KEY, enabled boolean NOT NULL, minimum_severity text NOT NULL,
    escalation_after_seconds integer NOT NULL
)`, `CREATE TABLE charlie_findings (
    id uuid PRIMARY KEY, connection_id uuid NOT NULL, severity text NOT NULL,
    status text NOT NULL, execution_block_code text NOT NULL, repeat_count integer NOT NULL,
    updated_at timestamptz NOT NULL
)`, `CREATE TABLE charlie_finding_resources (
    finding_id uuid NOT NULL, resource_type text NOT NULL, resource_id text NOT NULL
)`, `CREATE TABLE charlie_alert_policy_channels (
    connection_id uuid NOT NULL, notification_channel_id uuid NOT NULL
)`, `CREATE TABLE notification_channels (id uuid PRIMARY KEY, enabled boolean NOT NULL)`, `CREATE TABLE charlie_alert_deliveries (
    finding_id uuid NOT NULL, notification_channel_id uuid, delivery_kind text NOT NULL
	)`} {
		if _, err = conn.Exec(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	connectionID, findingID, channelID, installationID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, seed := range []struct {
		statement string
		args      []any
	}{
		{`INSERT INTO charlie_connections VALUES ($1, true, false, 'read_only', 'read_only', $2, 'product-a', 'astronomer', 'deployment-a', 'route-a', 'https://charlie.example', 'ca-a', 'key-a', 'fingerprint-a', 'logical-a')`, []any{connectionID, installationID}},
		{`INSERT INTO charlie_alert_policies VALUES ($1, true, 'medium', 3600)`, []any{connectionID}},
		{`INSERT INTO charlie_findings VALUES ($1, $2, 'high', 'open', 'approval_required', 1, now())`, []any{findingID, connectionID}},
		{`INSERT INTO charlie_alert_policy_channels VALUES ($1, $2)`, []any{connectionID, channelID}},
		{`INSERT INTO notification_channels VALUES ($1, true)`, []any{channelID}},
	} {
		if _, err = conn.Exec(ctx, seed.statement, seed.args...); err != nil {
			t.Fatal(err)
		}
	}
	queries := New(conn)
	rows, err := queries.ListCharlieAlertReconcileCandidates(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("unscoped partial finding became an alert candidate: %+v", rows)
	}
	if _, err = conn.Exec(ctx, `INSERT INTO charlie_finding_resources VALUES ($1, 'management_component', 'astronomer-server')`, findingID); err != nil {
		t.Fatal(err)
	}
	rows, err = queries.ListCharlieAlertReconcileCandidates(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].FindingID != findingID || rows[0].ResourceType != "management_component" || rows[0].ResourceID != "astronomer-server" {
		t.Fatalf("scoped finding candidate mismatch: %+v", rows)
	}

	// Credential replacement keeps the historical finding bound to its source
	// generation, but the current generation owns notification policy and
	// delivery. Exact immutable lineage admits the retained finding.
	replacementID := uuid.New()
	if _, err = conn.Exec(ctx, `UPDATE charlie_connections SET active=false WHERE id=$1`, connectionID); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `INSERT INTO charlie_connections VALUES ($1, true, false, 'read_only', 'read_only', $2, 'product-a', 'astronomer', 'deployment-a', 'route-a', 'https://charlie.example', 'ca-a', 'key-a', 'fingerprint-a', 'logical-a')`, replacementID, installationID); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `INSERT INTO charlie_alert_policies VALUES ($1, true, 'medium', 3600)`, replacementID); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `INSERT INTO charlie_alert_policy_channels VALUES ($1, $2)`, replacementID, channelID); err != nil {
		t.Fatal(err)
	}
	rows, err = queries.ListCharlieAlertReconcileCandidates(ctx, 10)
	if err != nil || len(rows) != 1 || rows[0].FindingID != findingID {
		t.Fatalf("same-lineage replacement lost retained finding: rows=%+v err=%v", rows, err)
	}
	if _, err = conn.Exec(ctx, `UPDATE charlie_connections SET product_id='product-b' WHERE id=$1`, replacementID); err != nil {
		t.Fatal(err)
	}
	rows, err = queries.ListCharlieAlertReconcileCandidates(ctx, 10)
	if err != nil || len(rows) != 0 {
		t.Fatalf("cross-product replacement admitted retained finding: rows=%+v err=%v", rows, err)
	}
}
