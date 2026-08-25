package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

const (
	postgresFailoverSchema = "astronomer-postgres-failover-certification/v1"
	postgresFailoverLabel  = "astronomer.qualification=postgres-failover"
)

type postgresFailoverEvidence struct {
	SchemaVersion string                      `json:"schema_version"`
	RunID         string                      `json:"run_id"`
	Status        string                      `json:"status"`
	Timestamps    postgresFailoverTimestamps  `json:"timestamps"`
	Provenance    postgresFailoverProvenance  `json:"provenance"`
	Topology      postgresFailoverTopology    `json:"topology"`
	Thresholds    postgresFailoverThresholds  `json:"thresholds"`
	Measurements  postgresFailoverMeasurement `json:"measurements"`
	Checks        []postgresFailoverCheck     `json:"checks"`
	Events        []postgresFailoverEvent     `json:"events"`
	ResidualScope []string                    `json:"residual_scope"`
}

type postgresFailoverTimestamps struct {
	StartedAt   string `json:"started_at"`
	FaultAt     string `json:"fault_at,omitempty"`
	PromotedAt  string `json:"promoted_at,omitempty"`
	RecoveredAt string `json:"recovered_at,omitempty"`
	CompletedAt string `json:"completed_at"`
	GeneratedAt string `json:"generated_at"`
}

type postgresFailoverProvenance struct {
	SourceCommit       string `json:"source_commit"`
	SourceRepository   string `json:"source_repository"`
	SourceRef          string `json:"source_ref"`
	Workflow           string `json:"workflow"`
	WorkflowRunID      string `json:"workflow_run_id"`
	WorkflowRunAttempt string `json:"workflow_run_attempt"`
	GoVersion          string `json:"go_version"`
	PostgresVersion    string `json:"postgres_version,omitempty"`
	PostgresImage      string `json:"postgres_image"`
	PostgresImageID    string `json:"postgres_image_id"`
}

type postgresFailoverTopology struct {
	Replication         string `json:"replication"`
	CommitMode          string `json:"commit_mode"`
	ApplicationEndpoint string `json:"application_endpoint"`
	FailureInjection    string `json:"failure_injection"`
	Promotion           string `json:"promotion"`
}

type postgresFailoverThresholds struct {
	RPOMaxRows int64 `json:"rpo_max_rows"`
	RTOMaxMS   int64 `json:"rto_max_ms"`
}

type postgresFailoverMeasurement struct {
	LastCommittedSequence int64  `json:"last_committed_sequence"`
	RecoveredSequence     int64  `json:"recovered_sequence"`
	RPORows               int64  `json:"rpo_rows"`
	RPOSeconds            int64  `json:"rpo_seconds"`
	UnreadyDetectionMS    int64  `json:"unready_detection_ms"`
	PromotionMS           int64  `json:"promotion_ms"`
	RTOMS                 int64  `json:"rto_ms"`
	CommitLSN             string `json:"commit_lsn,omitempty"`
}

type postgresFailoverCheck struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

type postgresFailoverEvent struct {
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
}

func newPostgresFailoverEvidence(started time.Time, rpoMaxRows, rtoMaxMS int64) *postgresFailoverEvidence {
	return &postgresFailoverEvidence{
		SchemaVersion: postgresFailoverSchema,
		RunID:         postgresFailoverEnvOr("ASTRONOMER_POSTGRES_FAILOVER_RUN_ID", "local"),
		Status:        "fail",
		Timestamps: postgresFailoverTimestamps{
			StartedAt: started.UTC().Format(time.RFC3339Nano),
		},
		Provenance: postgresFailoverProvenance{
			SourceCommit:       postgresFailoverEnvOr("ASTRONOMER_POSTGRES_FAILOVER_SOURCE_COMMIT", "unknown"),
			SourceRepository:   postgresFailoverEnvOr("ASTRONOMER_POSTGRES_FAILOVER_SOURCE_REPOSITORY", "local"),
			SourceRef:          postgresFailoverEnvOr("ASTRONOMER_POSTGRES_FAILOVER_SOURCE_REF", "local"),
			Workflow:           postgresFailoverEnvOr("ASTRONOMER_POSTGRES_FAILOVER_WORKFLOW", "local"),
			WorkflowRunID:      postgresFailoverEnvOr("ASTRONOMER_POSTGRES_FAILOVER_WORKFLOW_RUN_ID", "local"),
			WorkflowRunAttempt: postgresFailoverEnvOr("ASTRONOMER_POSTGRES_FAILOVER_WORKFLOW_RUN_ATTEMPT", "local"),
			GoVersion:          runtime.Version(),
			PostgresImage:      os.Getenv("ASTRONOMER_POSTGRES_FAILOVER_IMAGE"),
			PostgresImageID:    os.Getenv("ASTRONOMER_POSTGRES_FAILOVER_IMAGE_ID"),
		},
		Topology: postgresFailoverTopology{
			Replication:         "postgresql_physical_streaming",
			CommitMode:          "synchronous_commit_remote_flush",
			ApplicationEndpoint: "stable_tcp_writer_endpoint",
			FailureInjection:    "primary_sigkill",
			Promotion:           "pg_ctl_promote",
		},
		Thresholds: postgresFailoverThresholds{RPOMaxRows: rpoMaxRows, RTOMaxMS: rtoMaxMS},
		ResidualScope: []string{
			"The drill certifies PostgreSQL and Astronomer pool/readiness recovery; managed-provider control-plane and DNS detection time are not included.",
			"The writer-endpoint switch is deterministic and process-local; production endpoint ownership remains the external HA provider's responsibility.",
		},
	}
}

func (e *postgresFailoverEvidence) check(id, detail string) {
	e.Checks = append(e.Checks, postgresFailoverCheck{ID: id, Status: "pass", Detail: detail})
}

func (e *postgresFailoverEvidence) event(id string, at time.Time) {
	e.Events = append(e.Events, postgresFailoverEvent{ID: id, Timestamp: at.UTC().Format(time.RFC3339Nano)})
}

func (e *postgresFailoverEvidence) validate() error {
	if e.SchemaVersion != postgresFailoverSchema || e.RunID == "" {
		return errors.New("missing evidence identity")
	}
	if e.Thresholds.RPOMaxRows < 0 || e.Thresholds.RTOMaxMS <= 0 {
		return errors.New("invalid failover thresholds")
	}
	if e.Timestamps.StartedAt == "" || e.Timestamps.CompletedAt == "" || e.Timestamps.GeneratedAt == "" {
		return errors.New("incomplete evidence timestamps")
	}
	if e.Provenance.SourceCommit == "" || e.Provenance.PostgresImage == "" || e.Provenance.PostgresImageID == "" {
		return errors.New("incomplete evidence provenance")
	}
	if e.Status != "pass" && e.Status != "fail" {
		return errors.New("invalid evidence status")
	}
	if e.Status == "pass" {
		if e.Measurements.RPORows > e.Thresholds.RPOMaxRows {
			return fmt.Errorf("RPO %d rows exceeds threshold %d", e.Measurements.RPORows, e.Thresholds.RPOMaxRows)
		}
		if e.Measurements.RTOMS <= 0 || e.Measurements.RTOMS > e.Thresholds.RTOMaxMS {
			return fmt.Errorf("RTO %dms is outside threshold 1..%d", e.Measurements.RTOMS, e.Thresholds.RTOMaxMS)
		}
		required := map[string]bool{
			"synchronous_replica": false, "pre_failover_readiness": false,
			"outage_readiness_fail_closed": false, "replica_promoted": false,
			"post_failover_persistence": false, "rpo_threshold": false, "rto_threshold": false,
		}
		for _, check := range e.Checks {
			if _, ok := required[check.ID]; ok && check.Status == "pass" {
				required[check.ID] = true
			}
		}
		for id, passed := range required {
			if !passed {
				return fmt.Errorf("required check %q did not pass", id)
			}
		}
	}
	return nil
}

func writePostgresFailoverEvidence(path string, evidence *postgresFailoverEvidence) error {
	if path == "" {
		return errors.New("evidence path is required")
	}
	if err := evidence.validate(); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, payload, 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

func TestPostgresFailoverEvidenceValidation(t *testing.T) {
	started := time.Date(2026, 8, 24, 1, 2, 3, 0, time.UTC)
	evidence := newPostgresFailoverEvidence(started, 0, 30_000)
	evidence.Provenance.PostgresImage = "postgres:test"
	evidence.Provenance.PostgresImageID = "sha256:test"
	evidence.Status = "pass"
	evidence.Timestamps.FaultAt = started.Add(time.Second).Format(time.RFC3339Nano)
	evidence.Timestamps.PromotedAt = started.Add(2 * time.Second).Format(time.RFC3339Nano)
	evidence.Timestamps.RecoveredAt = started.Add(3 * time.Second).Format(time.RFC3339Nano)
	evidence.Timestamps.CompletedAt = started.Add(3 * time.Second).Format(time.RFC3339Nano)
	evidence.Timestamps.GeneratedAt = started.Add(3 * time.Second).Format(time.RFC3339Nano)
	evidence.Measurements.RTOMS = 2_000
	for _, id := range []string{"synchronous_replica", "pre_failover_readiness", "outage_readiness_fail_closed", "replica_promoted", "post_failover_persistence", "rpo_threshold", "rto_threshold"} {
		evidence.check(id, "")
	}
	if err := evidence.validate(); err != nil {
		t.Fatal(err)
	}
	evidence.Measurements.RPORows = 1
	if err := evidence.validate(); err == nil || !strings.Contains(err.Error(), "RPO") {
		t.Fatalf("RPO threshold validation error = %v", err)
	}
}

// TestPostgresFailoverRecoveryCertification is intentionally opt-in. Its
// companion script creates the labelled PostgreSQL primary/physical replica
// and retains all logs plus the evidence document.
func TestPostgresFailoverRecoveryCertification(t *testing.T) {
	primaryURL := os.Getenv("ASTRONOMER_POSTGRES_FAILOVER_PRIMARY_URL")
	replicaURL := os.Getenv("ASTRONOMER_POSTGRES_FAILOVER_REPLICA_URL")
	primaryContainer := os.Getenv("ASTRONOMER_POSTGRES_FAILOVER_PRIMARY_CONTAINER")
	replicaContainer := os.Getenv("ASTRONOMER_POSTGRES_FAILOVER_REPLICA_CONTAINER")
	evidencePath := os.Getenv("ASTRONOMER_POSTGRES_FAILOVER_EVIDENCE_FILE")
	if primaryURL == "" || replicaURL == "" || primaryContainer == "" || replicaContainer == "" || evidencePath == "" {
		t.Skip("run scripts/test-postgres-failover-certification.sh")
	}
	if os.Getenv("ASTRONOMER_POSTGRES_FAILOVER_DEDICATED") != "1" ||
		!strings.HasPrefix(primaryContainer, "astronomer-postgres-failover-primary-") ||
		!strings.HasPrefix(replicaContainer, "astronomer-postgres-failover-replica-") {
		t.Fatal("refusing failover injection without the dedicated dependency guard")
	}
	assertPostgresFailoverContainer(t, primaryContainer)
	assertPostgresFailoverContainer(t, replicaContainer)

	rpoMaxRows := postgresFailoverEnvInt64(t, "ASTRONOMER_POSTGRES_FAILOVER_RPO_MAX_ROWS")
	rtoMaxSeconds := postgresFailoverEnvInt64(t, "ASTRONOMER_POSTGRES_FAILOVER_RTO_MAX_SECONDS")
	if rpoMaxRows < 0 || rtoMaxSeconds <= 0 {
		t.Fatal("RPO must be non-negative and RTO must be positive")
	}
	started := time.Now().UTC()
	evidence := newPostgresFailoverEvidence(started, rpoMaxRows, rtoMaxSeconds*1000)
	defer func() {
		now := time.Now().UTC().Format(time.RFC3339Nano)
		evidence.Timestamps.CompletedAt = now
		evidence.Timestamps.GeneratedAt = now
		if !t.Failed() {
			evidence.Status = "pass"
		}
		if err := writePostgresFailoverEvidence(evidencePath, evidence); err != nil {
			t.Errorf("write failover evidence: %v", err)
		}
	}()

	primaryAddress := postgresFailoverURLAddress(t, primaryURL)
	replicaAddress := postgresFailoverURLAddress(t, replicaURL)
	proxy := newPostgresFailoverProxy(t, primaryAddress)
	defer proxy.Close()
	applicationURL := postgresFailoverProxyURL(t, primaryURL, proxy.Address())

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	database, err := db.ConnectWithConfig(ctx, applicationURL, db.PoolConfig{
		MaxConns: 8, MinConns: 2, HealthCheckPeriod: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	queries := sqlc.New(database.Pool())

	var postgresVersion string
	if err := database.Pool().QueryRow(ctx, `SHOW server_version`).Scan(&postgresVersion); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(postgresVersion, "16.") {
		t.Fatalf("PostgreSQL version = %q, want 16.x", postgresVersion)
	}
	evidence.Provenance.PostgresVersion = postgresVersion

	replica, err := pgx.Connect(ctx, replicaURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = replica.Close(context.Background()) }()
	var inRecovery, synchronous bool
	if err := replica.QueryRow(ctx, `SELECT pg_is_in_recovery()`).Scan(&inRecovery); err != nil {
		t.Fatal(err)
	}
	if err := database.Pool().QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM pg_stat_replication WHERE state = 'streaming' AND sync_state = 'sync'
	)`).Scan(&synchronous); err != nil {
		t.Fatal(err)
	}
	if !inRecovery || !synchronous {
		t.Fatalf("replication precondition recovery=%t synchronous=%t", inRecovery, synchronous)
	}
	evidence.check("synchronous_replica", "physical standby is streaming with synchronous acknowledgement")

	readyServer := httptest.NewServer(newReadinessHandler(database, postgresFailoverQueue{}, postgresFailoverHub{}))
	defer readyServer.Close()
	client := &http.Client{Timeout: 3 * time.Second}
	if code, body := postgresFailoverReadiness(t, client, readyServer.URL+"/readyz"); code != http.StatusOK || !strings.Contains(body, `"database":{"ok":true`) {
		t.Fatalf("initial readiness = %d %s", code, body)
	}
	evidence.check("pre_failover_readiness", "Astronomer readiness reports the database healthy")

	const committedCanaries int64 = 24
	// The disposable database is unique to this run, so compact keys preserve
	// the production platform_settings VARCHAR(64) boundary without collisions.
	keyPrefix := "qualification.pgfo.canary."
	for sequence := int64(1); sequence <= committedCanaries; sequence++ {
		if _, err := queries.UpsertPlatformSetting(ctx, sqlc.UpsertPlatformSettingParams{
			Key: keyPrefix + strconv.FormatInt(sequence, 10), Value: json.RawMessage(strconv.FormatInt(sequence, 10)),
			Description: "PostgreSQL failover certification canary",
		}); err != nil {
			t.Fatalf("commit canary %d: %v", sequence, err)
		}
	}
	evidence.Measurements.LastCommittedSequence = committedCanaries
	var commitLSN string
	if err := database.Pool().QueryRow(ctx, `SELECT pg_current_wal_lsn()::text`).Scan(&commitLSN); err != nil {
		t.Fatal(err)
	}
	evidence.Measurements.CommitLSN = commitLSN
	waitPostgresFailoverReplay(t, ctx, replica, keyPrefix, commitLSN, committedCanaries)
	evidence.check("pre_failover_replay_barrier", "all acknowledged canaries and the commit LSN are visible on the standby")

	faultAt := time.Now().UTC()
	evidence.Timestamps.FaultAt = faultAt.Format(time.RFC3339Nano)
	evidence.event("primary_sigkill_started", faultAt)
	postgresFailoverDocker(t, "kill", "--signal", "KILL", primaryContainer)
	evidence.event("primary_sigkill_completed", time.Now().UTC())

	unreadyAt := waitPostgresFailoverReadiness(t, client, readyServer.URL+"/readyz", http.StatusServiceUnavailable, faultAt.Add(15*time.Second))
	evidence.Measurements.UnreadyDetectionMS = unreadyAt.Sub(faultAt).Milliseconds()
	evidence.check("outage_readiness_fail_closed", "Astronomer returned 503 while no writable primary was available")

	promotionStarted := time.Now().UTC()
	evidence.event("replica_promotion_started", promotionStarted)
	postgresFailoverDocker(t, "exec", "--user", "postgres", replicaContainer,
		"pg_ctl", "-D", "/var/lib/postgresql/data", "promote", "-w", "-t", "30")
	promotedAt := time.Now().UTC()
	evidence.Timestamps.PromotedAt = promotedAt.Format(time.RFC3339Nano)
	evidence.Measurements.PromotionMS = promotedAt.Sub(promotionStarted).Milliseconds()
	evidence.event("replica_promotion_completed", promotedAt)

	if err := replica.QueryRow(ctx, `SELECT pg_is_in_recovery()`).Scan(&inRecovery); err != nil {
		t.Fatal(err)
	}
	if inRecovery {
		t.Fatal("replica still reports recovery after promotion")
	}
	evidence.check("replica_promoted", "the physical standby is now a writable primary")
	proxy.Switch(replicaAddress)
	evidence.event("writer_endpoint_switched", time.Now().UTC())

	recoveryDeadline := faultAt.Add(time.Duration(rtoMaxSeconds) * time.Second)
	recoveredAt := waitPostgresFailoverPersistence(t, ctx, client, readyServer.URL+"/readyz", queries, recoveryDeadline)
	evidence.Timestamps.RecoveredAt = recoveredAt.Format(time.RFC3339Nano)
	evidence.Measurements.RTOMS = recoveredAt.Sub(faultAt).Milliseconds()
	evidence.event("astronomer_persistence_recovered", recoveredAt)
	evidence.check("post_failover_persistence", "the existing pool recovered; readiness and a durable write/read succeeded")

	recoveredSequence := postgresFailoverMaxSequence(t, ctx, replica, keyPrefix)
	evidence.Measurements.RecoveredSequence = recoveredSequence
	evidence.Measurements.RPORows = committedCanaries - recoveredSequence
	if evidence.Measurements.RPORows < 0 {
		t.Fatalf("recovered sequence %d exceeds committed sequence %d", recoveredSequence, committedCanaries)
	}
	// With no acknowledged rows lost, the observed data-loss window is zero.
	// Row-based RPO remains the primary exact measurement in this workload.
	evidence.Measurements.RPOSeconds = 0
	if evidence.Measurements.RPORows > rpoMaxRows {
		t.Fatalf("RPO = %d rows, threshold = %d", evidence.Measurements.RPORows, rpoMaxRows)
	}
	evidence.check("rpo_threshold", fmt.Sprintf("observed RPO %d rows <= %d", evidence.Measurements.RPORows, rpoMaxRows))
	if evidence.Measurements.RTOMS > rtoMaxSeconds*1000 {
		t.Fatalf("RTO = %dms, threshold = %dms", evidence.Measurements.RTOMS, rtoMaxSeconds*1000)
	}
	evidence.check("rto_threshold", fmt.Sprintf("observed RTO %dms <= %dms", evidence.Measurements.RTOMS, rtoMaxSeconds*1000))
}

type postgresFailoverQueue struct{}

func (postgresFailoverQueue) Ping() error { return nil }

type postgresFailoverHub struct{}

func (postgresFailoverHub) ConnectedClusters() []string { return nil }

func postgresFailoverReadiness(t *testing.T, client *http.Client, endpoint string) (int, string) {
	t.Helper()
	response, err := client.Get(endpoint) // #nosec G107 -- fixed loopback httptest endpoint.
	if err != nil {
		return 0, err.Error()
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, 16<<10))
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, string(body)
}

func waitPostgresFailoverReadiness(t *testing.T, client *http.Client, endpoint string, want int, deadline time.Time) time.Time {
	t.Helper()
	for time.Now().Before(deadline) {
		code, _ := postgresFailoverReadiness(t, client, endpoint)
		if code == want {
			return time.Now().UTC()
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("readiness did not reach HTTP %d before %s", want, deadline.Format(time.RFC3339Nano))
	return time.Time{}
}

func waitPostgresFailoverPersistence(t *testing.T, parent context.Context, client *http.Client, endpoint string, queries *sqlc.Queries, deadline time.Time) time.Time {
	t.Helper()
	key := "qualification.pgfo.recovered"
	for time.Now().Before(deadline) {
		code, _ := postgresFailoverReadiness(t, client, endpoint)
		if code == http.StatusOK {
			attemptCtx, cancel := context.WithTimeout(parent, 2*time.Second)
			_, writeErr := queries.UpsertPlatformSetting(attemptCtx, sqlc.UpsertPlatformSettingParams{
				Key: key, Value: json.RawMessage(`true`), Description: "PostgreSQL failover recovered canary",
			})
			row, readErr := queries.GetPlatformSetting(attemptCtx, key)
			cancel()
			if writeErr == nil && readErr == nil && string(row.Value) == "true" {
				return time.Now().UTC()
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("Astronomer persistence did not recover before %s", deadline.Format(time.RFC3339Nano))
	return time.Time{}
}

func waitPostgresFailoverReplay(t *testing.T, ctx context.Context, replica *pgx.Conn, keyPrefix, lsn string, want int64) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var sequence int64
		var replayed bool
		err := replica.QueryRow(ctx, `SELECT
			COALESCE(MAX((value #>> '{}')::bigint), 0),
			COALESCE(pg_last_wal_replay_lsn() >= $2::pg_lsn, false)
		FROM platform_settings WHERE left(key, length($1)) = $1`, keyPrefix, lsn).Scan(&sequence, &replayed)
		if err == nil && sequence == want && replayed {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("standby did not replay sequence %d through LSN %s", want, lsn)
}

func postgresFailoverMaxSequence(t *testing.T, ctx context.Context, connection *pgx.Conn, keyPrefix string) int64 {
	t.Helper()
	var sequence int64
	if err := connection.QueryRow(ctx, `SELECT COALESCE(MAX((value #>> '{}')::bigint), 0)
		FROM platform_settings WHERE left(key, length($1)) = $1`, keyPrefix).Scan(&sequence); err != nil {
		t.Fatal(err)
	}
	return sequence
}

func postgresFailoverEnvInt64(t *testing.T, name string) int64 {
	t.Helper()
	value, err := strconv.ParseInt(os.Getenv(name), 10, 64)
	if err != nil {
		t.Fatalf("%s must be an integer: %v", name, err)
	}
	return value
}

func assertPostgresFailoverContainer(t *testing.T, container string) {
	t.Helper()
	label := strings.TrimSpace(postgresFailoverDocker(t, "inspect", "--format", `{{ index .Config.Labels "astronomer.qualification" }}`, container))
	if label != "postgres-failover" {
		t.Fatalf("container %q label = %q, want %q", container, label, postgresFailoverLabel)
	}
}

func postgresFailoverDocker(t *testing.T, args ...string) string {
	t.Helper()
	command := exec.Command("docker", args...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("docker %s failed: %v: %s", args[0], err, strings.TrimSpace(string(output)))
	}
	return string(output)
}

func postgresFailoverURLAddress(t *testing.T, rawURL string) string {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" {
		t.Fatalf("invalid PostgreSQL URL: %v", err)
	}
	return parsed.Host
}

func postgresFailoverProxyURL(t *testing.T, rawURL, address string) string {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	parsed.Host = address
	return parsed.String()
}

type postgresFailoverProxy struct {
	listener net.Listener
	backend  atomic.Value
	mu       sync.Mutex
	pairs    map[net.Conn]net.Conn
	closed   chan struct{}
}

func newPostgresFailoverProxy(t *testing.T, backend string) *postgresFailoverProxy {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	proxy := &postgresFailoverProxy{listener: listener, pairs: make(map[net.Conn]net.Conn), closed: make(chan struct{})}
	proxy.backend.Store(backend)
	go proxy.serve()
	return proxy
}

func (p *postgresFailoverProxy) Address() string { return p.listener.Addr().String() }

func (p *postgresFailoverProxy) Switch(backend string) {
	p.backend.Store(backend)
	p.closePairs()
}

func (p *postgresFailoverProxy) Close() {
	select {
	case <-p.closed:
		return
	default:
		close(p.closed)
	}
	_ = p.listener.Close()
	p.closePairs()
}

func (p *postgresFailoverProxy) closePairs() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for client, upstream := range p.pairs {
		_ = client.Close()
		_ = upstream.Close()
		delete(p.pairs, client)
	}
}

func (p *postgresFailoverProxy) serve() {
	for {
		client, err := p.listener.Accept()
		if err != nil {
			return
		}
		backend, _ := p.backend.Load().(string)
		upstream, err := net.DialTimeout("tcp", backend, 2*time.Second)
		if err != nil {
			_ = client.Close()
			continue
		}
		p.mu.Lock()
		p.pairs[client] = upstream
		p.mu.Unlock()
		go p.forward(client, upstream)
	}
}

func (p *postgresFailoverProxy) forward(client, upstream net.Conn) {
	done := make(chan struct{}, 2)
	copyConn := func(destination, source net.Conn) {
		_, _ = io.Copy(destination, source)
		done <- struct{}{}
	}
	go copyConn(upstream, client)
	go copyConn(client, upstream)
	<-done
	_ = client.Close()
	_ = upstream.Close()
	p.mu.Lock()
	delete(p.pairs, client)
	p.mu.Unlock()
}

func postgresFailoverEnvOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
