package worker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
	"github.com/alphabravocompany/astronomer-go/internal/tunnel"
	"github.com/alphabravocompany/astronomer-go/internal/worker/leader"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

const (
	processRestartDatabaseEnv = "ASTRONOMER_PROCESS_RESTART_DATABASE_URL"
	processRestartRedisEnv    = "ASTRONOMER_PROCESS_RESTART_REDIS_URL"
	processRestartGuardEnv    = "ASTRONOMER_PROCESS_RESTART_DEDICATED"
	processRestartRoleEnv     = "ASTRONOMER_PROCESS_RESTART_HELPER_ROLE"
	processRestartReadyEnv    = "ASTRONOMER_PROCESS_RESTART_HELPER_READY"
)

// TestProcessRestartQualification is opt-in because it terminates real helper
// processes and flushes its dedicated Redis database. The companion script
// owns disposable PostgreSQL 16 and Redis 7 containers.
func TestProcessRestartQualification(t *testing.T) {
	databaseURL, redisURL := os.Getenv(processRestartDatabaseEnv), os.Getenv(processRestartRedisEnv)
	if databaseURL == "" || redisURL == "" {
		t.Skip("run scripts/test-process-restart-qualification.sh")
	}
	if os.Getenv(processRestartGuardEnv) != "1" {
		t.Fatal("refusing process failure injection without dedicated dependency guard")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var postgresVersion string
	if err := pool.QueryRow(ctx, `SHOW server_version`).Scan(&postgresVersion); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(postgresVersion, "16.") {
		t.Fatalf("PostgreSQL version = %q, want 16.x", postgresVersion)
	}
	redisOptions, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	redisClient := redis.NewClient(redisOptions)
	defer func() { _ = redisClient.Close() }()
	if err := redisClient.FlushDB(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	redisInfo, err := redisClient.Info(ctx, "server").Result()
	if err != nil || !strings.Contains(redisInfo, "redis_version:7.") {
		t.Fatalf("Redis 7 qualification dependency unavailable: %v", err)
	}

	t.Run("server rolling restart preserves tunnel task and CIS progress", func(t *testing.T) {
		testServerRestartPreservesCIS(t, ctx, pool, redisURL)
	})
	if err := redisClient.FlushDB(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	t.Run("worker restart preserves committed task and audit intent", func(t *testing.T) {
		testWorkerRestartPreservesOutboxes(t, ctx, pool, redisURL)
	})
}

func testServerRestartPreservesCIS(t *testing.T, ctx context.Context, pool *pgxpool.Pool, redisURL string) {
	t.Helper()
	queries := sqlc.New(pool)
	clusterID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO clusters (id,name,display_name,status,last_heartbeat)
		VALUES ($1,$2,$2,'connected',now())`, clusterID, "restart-cis-"+clusterID.String()); err != nil {
		t.Fatal(err)
	}
	scanName := "restart-cis-" + uuid.NewString()[:8]
	scan, err := queries.CreateCISScanWithOutbox(ctx, sqlc.CreateCISScanWithOutboxParams{
		ClusterID: clusterID, ScanType: "cis-1.8", ClusterScanName: scanName,
		InitiatedByID: pgtype.UUID{}, AuditID: uuid.New(),
		AuditDedupeKey: "restart-cis-audit-" + uuid.NewString(), AuditActorAuthMethod: "integration",
		AuditHttpMethod: http.MethodPost, AuditPath: "/api/v1/security/scans/",
		AuditRequestID: uuid.NewString(), AuditUserAgent: "restart-qualification",
		AuditDetail: []byte(`{"qualification":"server_restart"}`), AuditCorrelationID: uuid.NewString(),
	})
	if err != nil {
		t.Fatal(err)
	}
	// Production schedules each poll after 30 seconds. This drill advances the
	// persisted due timestamps so the lifecycle can be fault-injected without
	// sleeping; row generations, leases, and dispatcher logic remain untouched.
	if _, err := pool.Exec(ctx, `UPDATE security_scan_results SET next_poll_at=now() WHERE id=$1`, scan.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE task_outbox SET next_attempt_at=now() WHERE task_type=$1 AND convert_from(payload,'UTF8') LIKE '%' || $2::text || '%'`,
		tasks.SecurityIngestType, scan.ID); err != nil {
		t.Fatal(err)
	}

	workDir := t.TempDir()
	serverA := startRestartQualificationProcess(t, "server", pool.Config().ConnString(), redisURL, workDir, "server-a")
	connA := connectRestartQualificationAgent(t, ctx, serverA.address, clusterID)
	asynqOptions, err := asynq.ParseRedisURI(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	client := asynq.NewClient(asynqOptions)
	defer func() { _ = client.Close() }()
	if err := tasks.DispatchTaskOutboxOnce(ctx, tasks.TaskOutboxDispatchDeps{Queries: queries, Enqueuer: client}); err != nil {
		t.Fatal(err)
	}
	respondRestartK8s(t, ctx, connA, "/apis/cis.cattle.io/v1/clusterscans/"+scanName, http.StatusOK, []byte(`{"status":{}}`))
	respondRestartK8s(t, ctx, connA, "/apis/cis.cattle.io/v1/clusterscanreports", http.StatusOK, []byte(`{"items":[]}`))
	waitRestartQualification(t, ctx, func() (bool, error) {
		row, err := queries.GetSecurityScanResultByID(ctx, scan.ID)
		if err != nil {
			return false, err
		}
		return row.Status == "running" && row.PollAttempt == 1 && row.NextPollAt.Valid &&
			row.PollOwner == "" && !row.PollLeaseExpiresAt.Valid, nil
	})
	assertRestartCISTaskRows(t, ctx, pool, scan.ID, 1, 1)

	// Replica A exits only after committing its next durable poll. Replica B
	// must continue that row; no active-task lease expiration is involved.
	stopRestartQualificationProcess(t, serverA)
	if err := connA.Close(websocket.StatusGoingAway, "server rolling restart"); err != nil {
		t.Logf("close replaced server connection: %v", err)
	}
	serverB := startRestartQualificationProcess(t, "server", pool.Config().ConnString(), redisURL, workDir, "server-b")
	defer stopRestartQualificationProcess(t, serverB)
	connB := connectRestartQualificationAgent(t, ctx, serverB.address, clusterID)
	defer func() { _ = connB.Close(websocket.StatusNormalClosure, "qualification complete") }()
	if _, err := pool.Exec(ctx, `UPDATE security_scan_results SET next_poll_at=now() WHERE id=$1`, scan.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE task_outbox SET next_attempt_at=now() WHERE status='pending' AND task_type=$1 AND convert_from(payload,'UTF8') LIKE '%' || $2::text || '%'`,
		tasks.SecurityIngestType, scan.ID); err != nil {
		t.Fatal(err)
	}
	if err := tasks.DispatchTaskOutboxOnce(ctx, tasks.TaskOutboxDispatchDeps{Queries: queries, Enqueuer: client}); err != nil {
		t.Fatal(err)
	}
	respondRestartK8s(t, ctx, connB, "/apis/cis.cattle.io/v1/clusterscans/"+scanName, http.StatusOK,
		[]byte(`{"status":{"reportName":"restart-report"}}`))
	reportJSON := `{"total":1,"pass":1,"fail":0,"warn":0,"skip":0,"results":[]}`
	report, _ := json.Marshal(map[string]any{
		"metadata": map[string]any{"name": "restart-report"},
		"spec":     map[string]any{"reportJSON": reportJSON},
	})
	respondRestartK8s(t, ctx, connB, "/apis/cis.cattle.io/v1/clusterscanreports/restart-report", http.StatusOK, report)
	waitRestartQualification(t, ctx, func() (bool, error) {
		row, err := queries.GetSecurityScanResultByID(ctx, scan.ID)
		return err == nil && row.Status == "completed" && row.PollAttempt == 2 && row.Passed == 1 && row.Failed == 0 &&
			row.UpstreamReportName == "restart-report" && !row.NextPollAt.Valid && row.PollOwner == "" && !row.PollLeaseExpiresAt.Valid, err
	})
	assertRestartCISTaskRows(t, ctx, pool, scan.ID, 2, 0)
	if err := tasks.DispatchAuditOutboxOnce(ctx, tasks.AuditOutboxDispatchDeps{Queries: queries}); err != nil {
		t.Fatal(err)
	}
	var auditRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action='security.scan.create' AND resource_id=$1 AND status_code=201`, scan.ID.String()).Scan(&auditRows); err != nil {
		t.Fatal(err)
	}
	if auditRows != 1 {
		t.Fatalf("CIS audit rows = %d, want one", auditRows)
	}
}

func testWorkerRestartPreservesOutboxes(t *testing.T, ctx context.Context, pool *pgxpool.Pool, redisURL string) {
	t.Helper()
	workDir := t.TempDir()
	workerA := startRestartQualificationProcess(t, "worker", pool.Config().ConnString(), redisURL, workDir, "worker-a")
	receiver := &restartQualificationReceiver{}
	receiver.Server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_, _ = io.Copy(io.Discard, io.LimitReader(request.Body, 64*1024))
		receiver.hits.Add(1)
		response.WriteHeader(http.StatusOK)
	}))
	defer receiver.Close()

	queries := sqlc.New(pool)
	intentKey := "worker-restart-" + uuid.NewString()
	task, err := tasks.NewNotificationSendTask(tasks.NotificationSendPayload{
		Channel: "webhook", Subject: "Worker restart qualification", Body: "durable restart intent",
		Recipients: []string{receiver.URL}, Severity: "info",
	})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	txQueries := queries.WithTx(tx)
	taskRow, err := tasks.EnqueueTaskOutbox(ctx, txQueries, task, tasks.TaskOutboxOptions{
		DedupeKey: intentKey, QueueName: "critical", MaxRetry: 3, Timeout: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	auditRow, err := audit.RecordOutbox(ctx, txQueries, audit.Event{
		Source: "service", ActorAuthMethod: "integration", Action: "qualification.worker_restart",
		ResourceType: "restart_intent", ResourceID: taskRow.ID.String(), ResourceName: "worker restart",
		HTTPMethod: http.MethodPost, Path: "/qualification/worker-restart", StatusCode: http.StatusAccepted,
		RequestID: uuid.NewString(), CorrelationID: uuid.NewString(), Detail: map[string]any{"bounded": true},
	}, "audit-"+intentKey, audit.OutboxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	assertRestartWorkerRows(t, ctx, pool, taskRow.ID, auditRow.ID, "pending", 0, "pending", 0, 0)
	if receiver.hits.Load() != 0 {
		t.Fatal("worker effect occurred before dispatcher wakeup")
	}

	redisOptions, err := asynq.ParseRedisURI(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	client := asynq.NewClient(redisOptions)
	defer func() { _ = client.Close() }()
	// These are scheduler wakeups, not recreations of the business intent.
	// They mature only after worker A has exited and remain in Redis for B.
	for _, taskType := range []string{TypeTaskOutboxDispatch, TypeAuditOutboxDispatch} {
		wake := asynq.NewTask(taskType, nil)
		if _, err := client.EnqueueContext(ctx, wake, asynq.ProcessIn(5*time.Second), asynq.TaskID("restart-wakeup-"+taskType+"-"+uuid.NewString())); err != nil {
			t.Fatal(err)
		}
	}
	stopRestartQualificationProcess(t, workerA)
	workerB := startRestartQualificationProcess(t, "worker", pool.Config().ConnString(), redisURL, workDir, "worker-b")
	defer stopRestartQualificationProcess(t, workerB)
	waitRestartQualification(t, ctx, func() (bool, error) {
		var taskStatus, auditStatus string
		var taskAttempts, auditAttempts, persisted int
		err := pool.QueryRow(ctx, `
			SELECT t.status,t.attempt_count,a.status,a.attempt_count,
			       (SELECT count(*) FROM audit_log WHERE id=a.id AND created_at=a.event_created_at)
			FROM task_outbox t CROSS JOIN audit_outbox a WHERE t.id=$1 AND a.id=$2`, taskRow.ID, auditRow.ID).
			Scan(&taskStatus, &taskAttempts, &auditStatus, &auditAttempts, &persisted)
		return err == nil && taskStatus == "delivered" && taskAttempts == 1 && auditStatus == "delivered" && auditAttempts == 1 && persisted == 1 && receiver.hits.Load() == 1, err
	})
	assertRestartWorkerRows(t, ctx, pool, taskRow.ID, auditRow.ID, "delivered", 1, "delivered", 1, 1)

	// Extra production dispatcher ticks prove delivered rows stay terminal and
	// do not duplicate the handler side effect after replacement.
	for _, taskType := range []string{TypeTaskOutboxDispatch, TypeAuditOutboxDispatch} {
		if _, err := client.EnqueueContext(ctx, asynq.NewTask(taskType, nil), asynq.TaskID("restart-repeat-"+taskType+"-"+uuid.NewString())); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(500 * time.Millisecond)
	if receiver.hits.Load() != 1 {
		t.Fatalf("worker restart receiver calls = %d, want exactly one", receiver.hits.Load())
	}
	assertRestartWorkerRows(t, ctx, pool, taskRow.ID, auditRow.ID, "delivered", 1, "delivered", 1, 1)
}

type restartQualificationReceiver struct {
	*httptest.Server
	hits atomic.Int32
}

type restartQualificationProcess struct {
	cmd     *exec.Cmd
	address string
	stopped bool
}

func startRestartQualificationProcess(t *testing.T, role, databaseURL, redisURL, dir, name string) *restartQualificationProcess {
	t.Helper()
	ready := filepath.Join(dir, name+".ready")
	cmd := exec.Command(os.Args[0], "-test.run=^TestProcessRestartQualificationHelper$", "-test.v")
	cmd.Env = append(os.Environ(), processRestartRoleEnv+"="+role, processRestartReadyEnv+"="+ready,
		processRestartDatabaseEnv+"="+databaseURL, processRestartRedisEnv+"="+redisURL)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	process := &restartQualificationProcess{cmd: cmd}
	t.Cleanup(func() {
		if !process.stopped {
			stopRestartQualificationProcess(t, process)
		}
	})
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(ready); err == nil && len(data) > 0 {
			process.address = string(data)
			return process
		}
		if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	stopRestartQualificationProcess(t, process)
	t.Fatalf("%s restart helper readiness timeout", name)
	return nil
}

func stopRestartQualificationProcess(t *testing.T, process *restartQualificationProcess) {
	t.Helper()
	if process == nil || process.stopped {
		return
	}
	process.stopped = true
	_ = process.cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan error, 1)
	go func() { done <- process.cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Logf("restart helper exited after SIGTERM: %v", err)
		}
	case <-time.After(12 * time.Second):
		_ = process.cmd.Process.Kill()
		<-done
		t.Fatal("restart helper did not stop gracefully")
	}
}

// TestProcessRestartQualificationHelper runs the same production worker
// constructors used by the server-embedded tunnel consumer and worker process.
func TestProcessRestartQualificationHelper(t *testing.T) {
	role := os.Getenv(processRestartRoleEnv)
	if role == "" {
		t.Skip("helper process only")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv(processRestartDatabaseEnv))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	redisURL := os.Getenv(processRestartRedisEnv)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	queries := sqlc.New(pool)
	redisOptions, err := asynq.ParseRedisURI(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	client := asynq.NewClient(redisOptions)
	defer func() { _ = client.Close() }()
	var workerProcess *Worker
	var httpServer *http.Server
	readyValue := role
	switch role {
	case "server":
		hub := tunnelHubForRestartQualification(log)
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		mux := http.NewServeMux()
		mux.HandleFunc("/ws", hub.HandleWebSocket)
		mux.HandleFunc("/agent-ready", func(response http.ResponseWriter, request *http.Request) {
			if hub.GetAgent(request.URL.Query().Get("cluster_id")) == nil {
				http.Error(response, "not ready", http.StatusServiceUnavailable)
				return
			}
			response.WriteHeader(http.StatusNoContent)
		})
		httpServer = &http.Server{Handler: mux, ReadHeaderTimeout: 2 * time.Second}
		go func() { _ = httpServer.Serve(listener) }()
		requester := handler.NewTunnelK8sRequester(hub)
		runtime := testTunnelRuntime()
		taskLeader := leader.New(pool, log)
		runtime.Core.Deps.Queries = queries
		runtime.Core.Deps.Leader = taskLeader
		runtime.Core.Deps.K8s = requester
		runtime.SecurityIngest = tasks.SecurityIngestRuntime{Deps: tasks.SecurityIngestDeps{
			Queries: queries, K8s: requester, Outbox: queries, Log: log, Owner: "restart-" + uuid.NewString(),
		}, Leader: taskLeader}
		workerProcess, err = NewTunnelWorker(redisURL, 1, log, runtime)
		if err != nil {
			t.Fatal(err)
		}
		readyValue = listener.Addr().String()
	case "worker":
		restoreGuard := httpclient.DisableGuardForTest()
		defer restoreGuard()
		runtime := testStandaloneRuntime()
		runtime.Core.Deps.Queries = queries
		runtime.Core.Deps.Leader = leader.New(pool, log)
		runtime.Core.Deps.Enqueuer = client
		runtime.Core.Deps.HTTPClient = http.DefaultClient
		runtime.Dispatch.TaskOutbox = tasks.TaskOutboxDispatchDeps{Queries: queries, Enqueuer: client}
		runtime.Dispatch.AuditOutbox = tasks.AuditOutboxDispatchDeps{Queries: queries}
		workerProcess, err = NewWorker(redisURL, log, runtime)
		if err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unknown restart helper role %q", role)
	}
	if role == "server" {
		workerProcess.RegisterTunnelHandlers()
	} else {
		workerProcess.RegisterHandlers()
	}
	if err := workerProcess.Start(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(os.Getenv(processRestartReadyEnv), []byte(readyValue), 0o600); err != nil {
		t.Fatal(err)
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM)
	<-signals
	workerProcess.Shutdown()
	if httpServer != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}
}

// Kept behind a helper so this file does not expose tunnel implementation
// details to the assertions above.
func tunnelHubForRestartQualification(log *slog.Logger) *tunnel.Hub {
	return tunnel.NewHub(log)
}

func respondRestartK8s(t *testing.T, ctx context.Context, conn *websocket.Conn, expectedPath string, status int, body []byte) {
	t.Helper()
	message := readK8sRequest(t, ctx, conn)
	var request protocol.K8sRequestPayload
	if err := json.Unmarshal(message.Payload, &request); err != nil {
		t.Fatal(err)
	}
	if request.Method != http.MethodGet || request.Path != expectedPath {
		t.Fatalf("tunnel request = %s %s, want GET %s", request.Method, request.Path, expectedPath)
	}
	payload, _ := json.Marshal(protocol.K8sResponsePayload{StatusCode: status, Body: base64.StdEncoding.EncodeToString(body)})
	if err := wsjson.Write(ctx, conn, &protocol.Message{
		Type: protocol.MsgK8sResponse, StreamID: message.StreamID, ClusterID: message.ClusterID, Payload: payload,
	}); err != nil {
		t.Fatal(err)
	}
}

func connectRestartQualificationAgent(t *testing.T, ctx context.Context, address string, clusterID uuid.UUID) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.Dial(ctx, "ws://"+address+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	capabilities := append(protocol.RequiredConnectCapabilities(), "mutate", "watch")
	payload, _ := json.Marshal(protocol.ConnectPayload{
		ClusterID: clusterID.String(), AgentID: "restart-qualification-agent", AgentVersion: "1.0.0",
		TunnelProtocolVersion: protocol.TunnelProtocolVersion, HeartbeatSchemaVersion: protocol.HeartbeatSchemaVersion,
		DeliveryProtocolVersion: protocol.DeliveryProtocolVersion, Capabilities: capabilities, Token: "integration-test-token",
	})
	if err := wsjson.Write(ctx, conn, &protocol.Message{Type: protocol.MsgConnect, Timestamp: time.Now().UTC(), Payload: payload}); err != nil {
		t.Fatal(err)
	}
	var ack protocol.Message
	if err := wsjson.Read(ctx, conn, &ack); err != nil || ack.Type != protocol.MsgConnectAck {
		t.Fatalf("CONNECT_ACK type=%s err=%v", ack.Type, err)
	}
	readyURL := "http://" + address + "/agent-ready?cluster_id=" + clusterID.String()
	for ctx.Err() == nil {
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, readyURL, nil)
		response, readyErr := http.DefaultClient.Do(request)
		if readyErr == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusNoContent {
				return conn
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	_ = conn.Close(websocket.StatusGoingAway, "context cancelled")
	t.Fatalf("agent registration readiness: %v", ctx.Err())
	return nil
}

func assertRestartCISTaskRows(t *testing.T, ctx context.Context, pool *pgxpool.Pool, scanID uuid.UUID, delivered, pending int) {
	t.Helper()
	var total, gotDelivered, gotPending, attemptSum int
	if err := pool.QueryRow(ctx, `
		SELECT count(*),count(*) FILTER (WHERE status='delivered'),count(*) FILTER (WHERE status='pending'),coalesce(sum(attempt_count),0)
		FROM task_outbox WHERE task_type=$1 AND convert_from(payload,'UTF8') LIKE '%' || $2::text || '%'`, tasks.SecurityIngestType, scanID).
		Scan(&total, &gotDelivered, &gotPending, &attemptSum); err != nil {
		t.Fatal(err)
	}
	if total != delivered+pending || gotDelivered != delivered || gotPending != pending || attemptSum != delivered {
		t.Fatalf("CIS task rows = total:%d delivered:%d pending:%d attempts:%d, want %d/%d/%d/%d",
			total, gotDelivered, gotPending, attemptSum, delivered+pending, delivered, pending, delivered)
	}
}

func assertRestartWorkerRows(t *testing.T, ctx context.Context, pool *pgxpool.Pool, taskID, auditID uuid.UUID, taskStatus string, taskAttempts int, auditStatus string, auditAttempts, persisted int) {
	t.Helper()
	var gotTaskStatus, gotAuditStatus string
	var gotTaskAttempts, gotAuditAttempts, gotPersisted int
	if err := pool.QueryRow(ctx, `
		SELECT t.status,t.attempt_count,a.status,a.attempt_count,
		       (SELECT count(*) FROM audit_log WHERE id=a.id AND created_at=a.event_created_at)
		FROM task_outbox t CROSS JOIN audit_outbox a WHERE t.id=$1 AND a.id=$2`, taskID, auditID).
		Scan(&gotTaskStatus, &gotTaskAttempts, &gotAuditStatus, &gotAuditAttempts, &gotPersisted); err != nil {
		t.Fatal(err)
	}
	if gotTaskStatus != taskStatus || gotTaskAttempts != taskAttempts || gotAuditStatus != auditStatus || gotAuditAttempts != auditAttempts || gotPersisted != persisted {
		t.Fatalf("restart rows = task:%s/%d audit:%s/%d persisted:%d, want %s/%d %s/%d %d",
			gotTaskStatus, gotTaskAttempts, gotAuditStatus, gotAuditAttempts, gotPersisted,
			taskStatus, taskAttempts, auditStatus, auditAttempts, persisted)
	}
}

func waitRestartQualification(t *testing.T, ctx context.Context, predicate func() (bool, error)) {
	t.Helper()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		ready, err := predicate()
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			t.Fatal(err)
		}
		if ready {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("restart qualification timed out: %v", ctx.Err())
		case <-ticker.C:
		}
	}
}
