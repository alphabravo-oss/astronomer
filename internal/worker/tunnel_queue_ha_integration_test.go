package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
	"github.com/alphabravocompany/astronomer-go/internal/tunnel"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type diagnosticK8sRequester struct{ inner handler.K8sRequester }

func (r diagnosticK8sRequester) Do(ctx context.Context, clusterID, method, path string, body []byte, headers map[string]string) (*protocol.K8sResponsePayload, error) {
	response, err := r.inner.Do(ctx, clusterID, method, path, body, headers)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tunnel HA requester:", err)
	}
	return response, err
}

const (
	haDatabaseEnv = "ASTRONOMER_TUNNEL_HA_TEST_DATABASE_URL"
	haRedisEnv    = "ASTRONOMER_TUNNEL_HA_TEST_REDIS_URL"
)

// TestTunnelQueueHAIntegration is opt-in because it flushes a dedicated Redis
// database. scripts/test-tunnel-queue-ha.sh creates disposable dependencies.
func TestTunnelQueueHAIntegration(t *testing.T) {
	databaseURL, redisURL := os.Getenv(haDatabaseEnv), os.Getenv(haRedisEnv)
	if databaseURL == "" || redisURL == "" {
		t.Skip("run scripts/test-tunnel-queue-ha.sh")
	}
	if os.Getenv("ASTRONOMER_TUNNEL_HA_TEST_ALLOW_DESTRUCTIVE") != "1" {
		t.Fatal("refusing to flush Redis without the dedicated-database guard")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	redisOptions, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	redisClient := redis.NewClient(redisOptions)
	defer func() {
		if closeErr := redisClient.Close(); closeErr != nil {
			t.Errorf("close Redis client: %v", closeErr)
		}
	}()
	if err := redisClient.FlushDB(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "CREATE TABLE IF NOT EXISTS tunnel_ha_test_effects (run_id uuid NOT NULL, operation_id uuid NOT NULL, request_count integer NOT NULL DEFAULT 0, effect_count integer NOT NULL DEFAULT 0, PRIMARY KEY (run_id, operation_id))"); err != nil {
		t.Fatal(err)
	}
	runID, clusterID := uuid.New(), uuid.New()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM tunnel_ha_test_effects WHERE run_id=$1", runID)
	})

	workDir := t.TempDir()
	serverA := startHAHelper(t, ctx, "tunnel", databaseURL, redisURL, workDir, "a")
	serverB := startHAHelper(t, ctx, "tunnel-paused", databaseURL, redisURL, workDir, "b")
	defer stopHAHelper(serverA)
	defer stopHAHelper(serverB)
	connA := connectSyntheticAgent(t, ctx, serverA.address, clusterID)
	operation := createPodDeleteOperation(t, ctx, pool, clusterID, "failover")
	asynqRedis, err := asynq.ParseRedisURI(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	client := asynq.NewClient(asynqRedis)
	defer func() {
		if closeErr := client.Close(); closeErr != nil {
			t.Errorf("close Asynq client: %v", closeErr)
		}
	}()
	task, _ := tasks.NewPodDeleteTask(operation.ID)
	if _, err := client.EnqueueContext(ctx, task, asynq.Queue(TunnelQueueName), asynq.TaskID(operation.ID.String()), asynq.Retention(time.Minute)); err != nil {
		t.Fatal(err)
	}
	first := readK8sRequest(t, ctx, connA)
	recordSyntheticEffect(t, ctx, pool, runID, operation.ID)
	// The effect committed, but its response is deliberately lost with owner A.
	stopHAHelper(serverA)
	if closeErr := connA.Close(websocket.StatusGoingAway, "owner terminated"); closeErr != nil {
		// Owner A has already exited, so a peer-closed socket is expected and
		// must not replace the failover assertions with a cleanup failure.
		t.Logf("close terminated owner websocket: %v", closeErr)
	}
	if _, err := pool.Exec(ctx, "UPDATE workload_operations SET started_at=now()-interval '4 minutes' WHERE id=$1", operation.ID); err != nil {
		t.Fatal(err)
	}
	connB := connectSyntheticAgent(t, ctx, serverB.address, clusterID)
	defer func() {
		if closeErr := connB.Close(websocket.StatusNormalClosure, "done"); closeErr != nil {
			t.Errorf("close retry owner websocket: %v", closeErr)
		}
	}()
	if err := os.WriteFile(serverB.startPath, []byte("start"), 0o600); err != nil {
		t.Fatal(err)
	}
	second := readK8sRequest(t, ctx, connB)
	if first.StreamID == second.StreamID {
		t.Fatal("retry must use a fresh tunnel stream")
	}
	recordSyntheticEffect(t, ctx, pool, runID, operation.ID)
	response, _ := json.Marshal(protocol.K8sResponsePayload{StatusCode: http.StatusNotFound})
	if err := wsjson.Write(ctx, connB, &protocol.Message{Type: protocol.MsgK8sResponse, StreamID: second.StreamID, ClusterID: clusterID.String(), Payload: response}); err != nil {
		t.Fatal(err)
	}
	waitForOperationStatus(t, ctx, pool, operation.ID, "completed")
	var requests, effects int
	if err := pool.QueryRow(ctx, "SELECT request_count,effect_count FROM tunnel_ha_test_effects WHERE run_id=$1 AND operation_id=$2", runID, operation.ID).Scan(&requests, &effects); err != nil {
		t.Fatal(err)
	}
	if requests != 2 || effects != 1 {
		t.Fatalf("requests/effects=%d/%d, want 2/1", requests, effects)
	}

	stopHAHelper(serverB)
	standalone := startHAHelper(t, ctx, "standalone", databaseURL, redisURL, workDir, "worker")
	defer stopHAHelper(standalone)
	pending := createPodDeleteOperation(t, ctx, pool, clusterID, "pending")
	pendingTask, _ := tasks.NewPodDeleteTask(pending.ID)
	if _, err := client.EnqueueContext(ctx, pendingTask, asynq.Queue(TunnelQueueName), asynq.TaskID(pending.ID.String())); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)
	inspector := asynq.NewInspector(asynqRedis)
	defer func() {
		if closeErr := inspector.Close(); closeErr != nil {
			t.Errorf("close Asynq inspector: %v", closeErr)
		}
	}()
	info, err := inspector.GetTaskInfo(TunnelQueueName, pending.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	if info.State != asynq.TaskStatePending {
		t.Fatalf("standalone worker consumed tunnel task: %s", info.State)
	}
	var status string
	if err := pool.QueryRow(ctx, "SELECT status FROM workload_operations WHERE id=$1", pending.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "pending" {
		t.Fatalf("standalone worker mutated tunnel operation: %q", status)
	}
}

type haHelper struct {
	cmd       *exec.Cmd
	address   string
	startPath string
	stopped   bool
}

func startHAHelper(t *testing.T, ctx context.Context, role, databaseURL, redisURL, dir, name string) *haHelper {
	t.Helper()
	ready := filepath.Join(dir, name+".ready")
	start := filepath.Join(dir, name+".start")
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestTunnelQueueHAHelperProcess$", "-test.v")
	cmd.Env = append(os.Environ(), "ASTRONOMER_TUNNEL_HA_HELPER_ROLE="+role, "ASTRONOMER_TUNNEL_HA_HELPER_READY="+ready, "ASTRONOMER_TUNNEL_HA_HELPER_START="+start, haDatabaseEnv+"="+databaseURL, haRedisEnv+"="+redisURL)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	h := &haHelper{cmd: cmd, startPath: start}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(ready); err == nil && len(data) > 0 {
			h.address = string(data)
			return h
		}
		time.Sleep(25 * time.Millisecond)
	}
	stopHAHelper(h)
	t.Fatalf("%s helper readiness timeout", name)
	return nil
}

func stopHAHelper(h *haHelper) {
	if h == nil || h.stopped {
		return
	}
	h.stopped = true
	_ = h.cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan struct{})
	go func() { _ = h.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(12 * time.Second):
		_ = h.cmd.Process.Kill()
		<-done
	}
}

// TestTunnelQueueHAHelperProcess runs production worker constructors in an
// isolated OS process, so owner termination cannot be confused with goroutine
// cancellation in the test process.
func TestTunnelQueueHAHelperProcess(t *testing.T) {
	role := os.Getenv("ASTRONOMER_TUNNEL_HA_HELPER_ROLE")
	if role == "" {
		t.Skip("helper process only")
	}
	ctx := context.Background()
	redisURL := os.Getenv(haRedisEnv)
	var w *Worker
	var httpServer *http.Server
	if role == "tunnel" || role == "tunnel-paused" {
		pool, err := pgxpool.New(ctx, os.Getenv(haDatabaseEnv))
		if err != nil {
			t.Fatal(err)
		}
		defer pool.Close()
		hub := tunnel.NewHub(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
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
		runtime := testTunnelRuntime()
		runtime.Core.Deps.Queries = sqlc.New(pool)
		runtime.Core.Deps.K8s = diagnosticK8sRequester{inner: handler.NewTunnelK8sRequester(hub)}
		w, err = NewTunnelWorker(redisURL, 1, testLogger(), runtime)
		if err != nil {
			t.Fatal(err)
		}
		w.RegisterTunnelHandlers()
		if err := os.WriteFile(os.Getenv("ASTRONOMER_TUNNEL_HA_HELPER_READY"), []byte(listener.Addr().String()), 0o600); err != nil {
			t.Fatal(err)
		}
		if role == "tunnel-paused" {
			for {
				if _, err := os.Stat(os.Getenv("ASTRONOMER_TUNNEL_HA_HELPER_START")); err == nil {
					break
				}
				time.Sleep(25 * time.Millisecond)
			}
		}
	} else if role == "standalone" {
		var err error
		w, err = NewWorker(redisURL, testLogger(), testStandaloneRuntime())
		if err != nil {
			t.Fatal(err)
		}
		w.RegisterHandlers()
		if err := os.WriteFile(os.Getenv("ASTRONOMER_TUNNEL_HA_HELPER_READY"), []byte("standalone"), 0o600); err != nil {
			t.Fatal(err)
		}
	} else {
		t.Fatalf("unknown helper role %q", role)
	}
	go func() {
		if err := w.Start(); err != nil {
			fmt.Fprintln(os.Stderr, "HA helper worker:", err)
		}
	}()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM)
	<-signals
	w.Shutdown()
	if httpServer != nil {
		_ = httpServer.Close()
	}
}

func connectSyntheticAgent(t *testing.T, ctx context.Context, address string, clusterID uuid.UUID) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.Dial(ctx, "ws://"+address+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	capabilities := append(protocol.RequiredConnectCapabilities(), "mutate")
	payload, _ := json.Marshal(protocol.ConnectPayload{ClusterID: clusterID.String(), AgentID: "ha-synthetic-agent", AgentVersion: "1.0.0", TunnelProtocolVersion: protocol.TunnelProtocolVersion, HeartbeatSchemaVersion: protocol.HeartbeatSchemaVersion, DeliveryProtocolVersion: protocol.DeliveryProtocolVersion, Capabilities: capabilities, Token: "integration-test-token"})
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
			if closeErr := response.Body.Close(); closeErr != nil {
				t.Fatalf("close agent readiness response: %v", closeErr)
			}
			if response.StatusCode == http.StatusNoContent {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if ctx.Err() != nil {
		t.Fatalf("agent registration readiness: %v", ctx.Err())
	}
	return conn
}

func readK8sRequest(t *testing.T, ctx context.Context, conn *websocket.Conn) protocol.Message {
	t.Helper()
	var message protocol.Message
	if err := wsjson.Read(ctx, conn, &message); err != nil {
		t.Fatal(err)
	}
	if message.Type != protocol.MsgK8sRequest || message.StreamID == "" {
		t.Fatalf("unexpected message type=%s stream=%q", message.Type, message.StreamID)
	}
	return message
}

func createPodDeleteOperation(t *testing.T, ctx context.Context, pool *pgxpool.Pool, clusterID uuid.UUID, suffix string) sqlc.WorkloadOperation {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{"clusterId": clusterID.String(), "kind": "Pod", "namespace": "ha-test", "name": "pod-" + suffix})
	operation, err := sqlc.New(pool).CreateWorkloadOperation(ctx, sqlc.CreateWorkloadOperationParams{TargetType: "pod", TargetKey: "ha-test/pod-" + suffix, OperationType: "delete_pod", Payload: payload, Status: "pending", CreatedByID: pgtype.UUID{}})
	if err != nil {
		t.Fatal(err)
	}
	return operation
}

func recordSyntheticEffect(t *testing.T, ctx context.Context, pool *pgxpool.Pool, runID, operationID uuid.UUID) {
	t.Helper()
	_, err := pool.Exec(ctx, "INSERT INTO tunnel_ha_test_effects(run_id,operation_id,request_count,effect_count) VALUES($1,$2,1,1) ON CONFLICT(run_id,operation_id) DO UPDATE SET request_count=tunnel_ha_test_effects.request_count+1", runID, operationID)
	if err != nil {
		t.Fatal(err)
	}
}

func waitForOperationStatus(t *testing.T, ctx context.Context, pool *pgxpool.Pool, operationID uuid.UUID, want string) {
	t.Helper()
	for ctx.Err() == nil {
		var status string
		if err := pool.QueryRow(ctx, "SELECT status FROM workload_operations WHERE id=$1", operationID).Scan(&status); err == nil && status == want {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("operation did not reach %q: %v", want, ctx.Err())
}
