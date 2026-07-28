package sqlc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Content guard for MarkRunningAgentUpgradeSucceededByVersion.
//
// Since the self-upgrade hardening, the patch ack no longer completes an upgrade
// operation: this query is the primary success edge, matching the heartbeat's
// AgentVersion against the operation's target_version. The two sides are
// configured independently — target_version comes from the operator's request or
// config.AgentImageTag (the chart's image.agent.tag), while AgentVersion is the
// pkg/version.Version ldflag baked into the image at build time. Under exact
// equality a bare "v" prefix or a case difference makes every SUCCESSFUL upgrade
// sit in `running`, be re-claimed and re-dispatched every 5 minutes (a fresh
// preflight Pod and watchdog Job each time), and finally be reported as FAILED by
// the stuck-operation sweeper — which for a batched rollout reads as a
// fleet-wide failure.
func TestMarkRunningAgentUpgradeSucceededByVersion_NormalizesTheMatch(t *testing.T) {
	sql := markRunningAgentUpgradeSucceededByVersion
	if strings.Contains(sql, "AND target_version = $2") {
		t.Fatalf("the success match is exact string equality again:\n%s", sql)
	}
	for _, want := range []string{"ltrim(", "lower(", "btrim("} {
		if !strings.Contains(sql, want) {
			t.Errorf("success match does not normalize with %s:\n%s", want, sql)
		}
	}
	// An empty target_version must never be matchable, or a heartbeat reporting
	// a bare "v" would complete an unrelated operation.
	if !strings.Contains(sql, "btrim(target_version) <> ''") {
		t.Errorf("success match does not exclude an empty target_version:\n%s", sql)
	}
}

// Opt-in behavioural coverage against real PostgreSQL — the string functions
// above are only meaningful if the engine agrees. Release rehearsals set
// AGENT_UPGRADE_MATCH_TEST_DATABASE_URL to a disposable database; the test owns
// and drops an isolated schema.
func TestMarkRunningAgentUpgradeSucceededByVersion_MatchesAcrossVersionSpellings(t *testing.T) {
	dsn := os.Getenv("AGENT_UPGRADE_MATCH_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("AGENT_UPGRADE_MATCH_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())

	schema := "agent_upgrade_match_" + uuid.NewString()[:8]
	if _, err := conn.Exec(ctx, fmt.Sprintf(`CREATE SCHEMA %s`, schema)); err != nil {
		t.Fatal(err)
	}
	defer conn.Exec(context.Background(), fmt.Sprintf(`DROP SCHEMA IF EXISTS %s CASCADE`, schema))
	if _, err := conn.Exec(ctx, fmt.Sprintf(`SET search_path TO %s`, schema)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `
CREATE TABLE agent_lifecycle_operations (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL,
    operation_type text NOT NULL,
    status text NOT NULL,
    target_version text NOT NULL DEFAULT '',
    target_image text NOT NULL DEFAULT '',
    current_version text NOT NULL DEFAULT '',
    strategy text NOT NULL DEFAULT '',
    operation_spec jsonb NOT NULL DEFAULT '{}'::jsonb,
    requested_by uuid,
    started_at timestamptz,
    completed_at timestamptz,
    last_error text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
)`); err != nil {
		t.Fatal(err)
	}

	q := New(conn)
	cases := []struct {
		name          string
		targetVersion string
		heartbeat     string
		want          int64
	}{
		{"identical", "0.3.1", "0.3.1", 1},
		{"heartbeat carries a v prefix", "0.3.1", "v0.3.1", 1},
		{"target carries a v prefix", "v0.3.1", "0.3.1", 1},
		{"case differs", "V0.3.1", "v0.3.1", 1},
		{"whitespace", " 0.3.1 ", "0.3.1", 1},
		{"genuinely different versions", "0.3.1", "0.3.2", 0},
		{"empty target is never matched", "", "v", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clusterID := uuid.New()
			if _, err := conn.Exec(ctx, `
INSERT INTO agent_lifecycle_operations (id, cluster_id, operation_type, status, target_version)
VALUES ($1, $2, 'agent_upgrade', 'running', $3)`, uuid.New(), clusterID, tc.targetVersion); err != nil {
				t.Fatal(err)
			}
			got, err := q.MarkRunningAgentUpgradeSucceededByVersion(ctx, MarkRunningAgentUpgradeSucceededByVersionParams{
				ClusterID:     clusterID,
				TargetVersion: tc.heartbeat,
			})
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("target_version %q vs heartbeat %q completed %d operations, want %d",
					tc.targetVersion, tc.heartbeat, got, tc.want)
			}
		})
	}
}
