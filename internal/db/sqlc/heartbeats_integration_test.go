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
)

// TestRecordAgentHeartbeatAtomicWrite is an opt-in PostgreSQL test. It proves
// the common heartbeat advances narrow liveness/connection rows, refreshes the
// health sample timestamp, and leaves stable wide cluster inventory untouched.
func TestRecordAgentHeartbeatAtomicWrite(t *testing.T) {
	dsn := os.Getenv("HEARTBEAT_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("HEARTBEAT_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())

	schema := "heartbeat_" + uuid.NewString()[:8]
	if _, err := conn.Exec(ctx, fmt.Sprintf(`CREATE SCHEMA %s`, schema)); err != nil {
		t.Fatal(err)
	}
	defer conn.Exec(context.Background(), fmt.Sprintf(`DROP SCHEMA IF EXISTS %s CASCADE`, schema))
	if _, err := conn.Exec(ctx, fmt.Sprintf(`SET search_path TO %s`, schema)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `
CREATE TABLE clusters (
    id uuid PRIMARY KEY,
    decommissioned_at timestamptz,
    agent_version text NOT NULL DEFAULT '',
    kubernetes_version text NOT NULL DEFAULT '',
    node_count integer NOT NULL DEFAULT 0,
    distribution text NOT NULL DEFAULT ''
);
CREATE TABLE agent_connections (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters(id),
    status text NOT NULL,
    last_ping timestamptz
);
CREATE TABLE agent_lifecycle_operations (
    id uuid PRIMARY KEY,
    cluster_id uuid NOT NULL REFERENCES clusters(id),
    status text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE cluster_liveness (
    cluster_id uuid PRIMARY KEY REFERENCES clusters(id),
    last_heartbeat timestamptz,
    heartbeat_count bigint NOT NULL DEFAULT 0,
    commands_pending boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE cluster_health_statuses (
    cluster_id uuid PRIMARY KEY REFERENCES clusters(id),
    cpu_usage_percent double precision NOT NULL DEFAULT 0,
    memory_usage_percent double precision NOT NULL DEFAULT 0,
    pod_count integer NOT NULL DEFAULT 0,
    node_count integer NOT NULL DEFAULT 0,
    conditions jsonb NOT NULL DEFAULT '{}',
    last_check timestamptz NOT NULL DEFAULT now()
);`, pgx.QueryExecModeSimpleProtocol); err != nil {
		t.Fatal(err)
	}

	clusterID, connectionID := uuid.New(), uuid.New()
	if _, err := conn.Exec(ctx, `
INSERT INTO clusters (id) VALUES ($1);
INSERT INTO agent_connections (id, cluster_id, status) VALUES ($2, $1, 'connected')`, pgx.QueryExecModeSimpleProtocol, clusterID, connectionID); err != nil {
		t.Fatal(err)
	}

	q := New(conn)
	arg := RecordAgentHeartbeatParams{
		ConnectionID:       connectionID,
		ClusterID:          clusterID,
		AgentVersion:       "v1.2.3",
		KubernetesVersion:  "v1.31.1",
		NodeCount:          3,
		Distribution:       "k3s",
		CpuUsagePercent:    12.5,
		MemoryUsagePercent: 48.25,
		PodCount:           17,
		Conditions:         []byte(`{"connected":true}`),
	}
	pending, err := q.RecordAgentHeartbeat(ctx, arg)
	if err != nil {
		t.Fatal(err)
	}
	if pending {
		t.Fatal("fresh cluster unexpectedly reported pending lifecycle work")
	}

	var firstClusterXID, firstHealthXID string
	if err := conn.QueryRow(ctx, `SELECT xmin::text FROM clusters WHERE id=$1`, clusterID).Scan(&firstClusterXID); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, `SELECT xmin::text FROM cluster_health_statuses WHERE cluster_id=$1`, clusterID).Scan(&firstHealthXID); err != nil {
		t.Fatal(err)
	}
	if _, err := q.RecordAgentHeartbeat(ctx, arg); err != nil {
		t.Fatal(err)
	}

	var clusterXID, healthXID string
	var count int64
	var heartbeat, ping time.Time
	if err := conn.QueryRow(ctx, `SELECT xmin::text FROM clusters WHERE id=$1`, clusterID).Scan(&clusterXID); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, `SELECT xmin::text FROM cluster_health_statuses WHERE cluster_id=$1`, clusterID).Scan(&healthXID); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, `SELECT heartbeat_count,last_heartbeat FROM cluster_liveness WHERE cluster_id=$1`, clusterID).Scan(&count, &heartbeat); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, `SELECT last_ping FROM agent_connections WHERE id=$1`, connectionID).Scan(&ping); err != nil {
		t.Fatal(err)
	}
	if clusterXID != firstClusterXID {
		t.Fatalf("stable heartbeat rewrote wide cluster tuple: %s→%s", firstClusterXID, clusterXID)
	}
	if healthXID == firstHealthXID {
		t.Fatalf("stable heartbeat did not refresh health sample tuple: xid=%s", healthXID)
	}
	if count != 2 || heartbeat.IsZero() || ping.IsZero() {
		t.Fatalf("narrow liveness not advanced: count=%d heartbeat=%v ping=%v", count, heartbeat, ping)
	}

	arg.ConnectionID = uuid.New()
	if _, err := q.RecordAgentHeartbeat(ctx, arg); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("superseded connection error = %v, want pgx.ErrNoRows", err)
	}
}
