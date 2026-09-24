package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
)

const (
	redisOutageDatabaseEnv  = "ASTRONOMER_REDIS_OUTAGE_DATABASE_URL"
	redisOutageRedisEnv     = "ASTRONOMER_REDIS_OUTAGE_REDIS_URL"
	redisOutageContainerEnv = "ASTRONOMER_REDIS_OUTAGE_CONTAINER"
	redisOutageGuardEnv     = "ASTRONOMER_REDIS_OUTAGE_DEDICATED"
)

var redisOutageContainerPattern = regexp.MustCompile(`^astronomer-redis-outage-redis-[a-zA-Z0-9_.-]+$`)

// TestRedisOutageRecoveryDurableNotification is opt-in because its companion
// script deliberately starts a dedicated Redis container in the stopped state
// and authorizes this test to restart it. It exercises the production alerting
// transaction, sqlc outboxes, task dispatcher, Asynq consumer, and notification
// handler against real PostgreSQL and Redis processes.
func TestRedisOutageRecoveryDurableNotification(t *testing.T) {
	databaseURL := os.Getenv(redisOutageDatabaseEnv)
	redisURL := os.Getenv(redisOutageRedisEnv)
	redisContainer := os.Getenv(redisOutageContainerEnv)
	if databaseURL == "" || redisURL == "" || redisContainer == "" {
		t.Skip("disposable PostgreSQL and Redis outage endpoints are not configured")
	}
	if os.Getenv(redisOutageGuardEnv) != "1" || !redisOutageContainerPattern.MatchString(redisContainer) {
		t.Fatal("refusing Redis failure injection without the dedicated-container guard")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
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
	if err := redisClient.Ping(ctx).Err(); err == nil {
		t.Fatal("Redis must be unavailable before the durable API mutation")
	}

	receiver := newRedisOutageReceiver(t)
	defer receiver.Close()
	restoreGuard := httpclient.DisableGuardForTest()
	defer restoreGuard()
	queries := sqlc.New(pool)
	channelID := uuid.New()
	configuration, err := json.Marshal(map[string]string{"url": receiver.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO notification_channels (id,name,channel_type,configuration,enabled)
		VALUES ($1,'Redis outage recovery','webhook',$2,true)`, channelID, configuration); err != nil {
		t.Fatal(err)
	}

	alerting := handler.NewAlertingHandler(queries)
	alerting.SetRunTx(func(ctx context.Context, apply func(handler.AlertingMutationTx) error) error {
		tx, err := pool.Begin(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if err := apply(queries.WithTx(tx)); err != nil {
			return err
		}
		return tx.Commit(ctx)
	})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/alerting/channels/"+channelID.String()+"/test/", nil)
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("id", channelID.String())
	request = request.WithContext(context.WithValue(request.Context(), chi.RouteCtxKey, routeContext))
	response := httptest.NewRecorder()
	alerting.TestChannel(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("test notification response = %d: %s", response.Code, response.Body.String())
	}
	var acknowledgement struct {
		Data struct {
			Success bool `json:"success"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &acknowledgement); err != nil || !acknowledgement.Data.Success {
		t.Fatalf("test notification acknowledgement = %s, error=%v", response.Body.String(), err)
	}
	if receiver.hits.Load() != 0 {
		t.Fatal("receiver was called while Redis was unavailable")
	}

	taskRow := assertRedisOutageCommittedIntent(t, ctx, pool, channelID)
	assertRedisOutageAuditState(t, ctx, pool, channelID, "pending", 0, 0)

	asynqOptions, err := asynq.ParseRedisURI(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	outboxClient := asynq.NewClient(asynqOptions)
	outageDispatchTime := time.Now().UTC().Add(time.Second)
	if err := tasks.DispatchTaskOutboxOnce(ctx, tasks.TaskOutboxDispatchDeps{
		Queries: queries, Enqueuer: outboxClient, Now: func() time.Time { return outageDispatchTime },
	}); err != nil {
		t.Fatalf("production task dispatcher isolated outage incorrectly: %v", err)
	}
	failedRow := assertRedisOutageTaskState(t, ctx, pool, taskRow.ID, "failed", 1, false, true)
	databaseEndpoint, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	databasePassword := ""
	if databaseEndpoint.User != nil {
		databasePassword, _ = databaseEndpoint.User.Password()
	}
	if databasePassword == "" {
		t.Fatal("disposable PostgreSQL URL must contain a generated password")
	}
	wantBody := "This is a test notification from Astronomer for channel \"Redis outage recovery\". If you can see this, delivery is working."
	if len(failedRow.LastError) > 2048 || strings.Contains(failedRow.LastError, databasePassword) ||
		strings.Contains(failedRow.LastError, wantBody) || strings.Contains(failedRow.LastError, string(taskRow.Payload)) {
		t.Fatalf("failed outbox error was not bounded and sanitized: length=%d", len(failedRow.LastError))
	}
	if receiver.hits.Load() != 0 {
		t.Fatal("failed Redis enqueue reached the external receiver")
	}
	if err := outboxClient.Close(); err != nil {
		t.Fatal(err)
	}

	// Audit delivery is PostgreSQL-only and must remain durable independent of
	// Redis. Dispatching it during the outage proves the successful API response
	// did not omit or lose the mandatory audit record.
	if err := tasks.DispatchAuditOutboxOnce(ctx, tasks.AuditOutboxDispatchDeps{Queries: queries}); err != nil {
		t.Fatal(err)
	}
	assertRedisOutageAuditState(t, ctx, pool, channelID, "delivered", 1, 1)

	recoveredRedisURL, recoveredRedisClient := restartRedisOutageContainer(t, ctx, redisContainer, redisURL)
	defer func() { _ = recoveredRedisClient.Close() }()
	info, err := recoveredRedisClient.Info(ctx, "server").Result()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(info, "redis_version:7.") {
		t.Fatalf("Redis server is not version 7.x: %s", info)
	}
	recoveredAsynqOptions, err := asynq.ParseRedisURI(recoveredRedisURL)
	if err != nil {
		t.Fatal(err)
	}
	recoveredOutboxClient := asynq.NewClient(recoveredAsynqOptions)
	defer func() { _ = recoveredOutboxClient.Close() }()

	descriptor := integrationDescriptor(t, TypeNotificationSend)
	core := tasks.CoreRuntime{Deps: tasks.RuntimeDependencies{
		HTTPClient: receiver.Client(),
		Log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	}}
	descriptor.Handler = core.BindHandler(descriptor.Handler)
	consumer := newWorkerIntegrationConsumer(t, recoveredRedisURL, []TaskDescriptor{descriptor}, map[string]int{"critical": 1})
	consumer.RegisterHandlers()
	if err := consumer.Start(); err != nil {
		t.Fatal(err)
	}
	defer consumer.Shutdown()

	// Advance the dispatcher's clock beyond its production backoff. The
	// original durable row is retried; no business task is manually re-enqueued.
	recoveryDispatchTime := outageDispatchTime.Add(10 * time.Second)
	if err := tasks.DispatchTaskOutboxOnce(ctx, tasks.TaskOutboxDispatchDeps{
		Queries: queries, Enqueuer: recoveredOutboxClient, Now: func() time.Time { return recoveryDispatchTime },
	}); err != nil {
		t.Fatal(err)
	}
	waitForRedisOutageReceiver(t, ctx, receiver, 1)
	assertRedisOutageTaskState(t, ctx, pool, taskRow.ID, "delivered", 2, true, false)

	// Re-running both production dispatchers must be a no-op for already
	// delivered rows and cannot duplicate either the receiver effect or audit.
	if err := tasks.DispatchTaskOutboxOnce(ctx, tasks.TaskOutboxDispatchDeps{
		Queries: queries, Enqueuer: recoveredOutboxClient, Now: func() time.Time { return recoveryDispatchTime.Add(time.Minute) },
	}); err != nil {
		t.Fatal(err)
	}
	if err := tasks.DispatchAuditOutboxOnce(ctx, tasks.AuditOutboxDispatchDeps{Queries: queries}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(250 * time.Millisecond)
	if receiver.hits.Load() != 1 {
		t.Fatalf("receiver calls = %d, want exactly one", receiver.hits.Load())
	}
	assertRedisOutageTaskState(t, ctx, pool, taskRow.ID, "delivered", 2, true, false)
	assertRedisOutageAuditState(t, ctx, pool, channelID, "delivered", 1, 1)
	receiver.assertExact(t)
}

type redisOutageReceiver struct {
	*httptest.Server
	hits atomic.Int32
	mu   sync.Mutex
	body []byte
}

func newRedisOutageReceiver(t *testing.T) *redisOutageReceiver {
	t.Helper()
	receiver := &redisOutageReceiver{}
	receiver.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, err := io.ReadAll(io.LimitReader(r.Body, 64*1024))
		if err != nil {
			http.Error(w, "read", http.StatusBadRequest)
			return
		}
		receiver.mu.Lock()
		receiver.body = append([]byte(nil), payload...)
		receiver.mu.Unlock()
		receiver.hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	return receiver
}

func (r *redisOutageReceiver) assertExact(t *testing.T) {
	t.Helper()
	r.mu.Lock()
	payload := append([]byte(nil), r.body...)
	r.mu.Unlock()
	var envelope map[string]any
	if err := json.Unmarshal(payload, &envelope); err != nil {
		t.Fatal(err)
	}
	wantSubject := "Astronomer test notification"
	wantBody := "This is a test notification from Astronomer for channel \"Redis outage recovery\". If you can see this, delivery is working."
	if envelope["subject"] != wantSubject || envelope["body"] != wantBody || envelope["text"] != wantBody || envelope["severity"] != "info" {
		t.Fatalf("receiver payload = %v", envelope)
	}
}

func assertRedisOutageCommittedIntent(t *testing.T, ctx context.Context, pool *pgxpool.Pool, channelID uuid.UUID) sqlc.TaskOutbox {
	t.Helper()
	rows, err := sqlc.New(pool).ListTaskOutbox(ctx, sqlc.ListTaskOutboxParams{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("committed task intents = %d, want one", len(rows))
	}
	row := rows[0]
	if row.TaskType != TypeNotificationSend || row.QueueName != "critical" || row.Status != "pending" || row.AttemptCount != 0 || row.DeliveredAt.Valid || row.LastError != "" {
		t.Fatalf("committed task intent = %+v", row)
	}
	var payload tasks.NotificationSendPayload
	if err := json.Unmarshal(row.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Channel != "webhook" || payload.Subject != "Astronomer test notification" || len(payload.Recipients) != 1 {
		t.Fatalf("committed task payload = %+v for channel %s", payload, channelID)
	}
	return row
}

func assertRedisOutageTaskState(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, status string, attempts int32, delivered, hasError bool) sqlc.TaskOutbox {
	t.Helper()
	row, err := sqlc.New(pool).GetTaskOutbox(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if row.Status != status || row.AttemptCount != attempts || row.DeliveredAt.Valid != delivered || (row.LastError != "") != hasError {
		t.Fatalf("task outbox state = status:%s attempts:%d delivered:%t error:%t; want %s/%d/%t/%t",
			row.Status, row.AttemptCount, row.DeliveredAt.Valid, row.LastError != "", status, attempts, delivered, hasError)
	}
	return row
}

func assertRedisOutageAuditState(t *testing.T, ctx context.Context, pool *pgxpool.Pool, channelID uuid.UUID, status string, attempts, auditRows int) {
	t.Helper()
	var count, gotAttempts int
	var gotStatus string
	if err := pool.QueryRow(ctx, `
		SELECT count(*),coalesce(max(status),''),coalesce(max(attempt_count),0)
		FROM audit_outbox WHERE action='alert.channel.test' AND resource_type='notification_channel' AND resource_id=$1`, channelID.String()).
		Scan(&count, &gotStatus, &gotAttempts); err != nil {
		t.Fatal(err)
	}
	if count != 1 || gotStatus != status || gotAttempts != attempts {
		t.Fatalf("audit outbox = count:%d status:%s attempts:%d; want 1/%s/%d", count, gotStatus, gotAttempts, status, attempts)
	}
	var persisted int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM audit_log
		WHERE action='alert.channel.test' AND resource_type='notification_channel' AND resource_id=$1 AND status_code=200`, channelID.String()).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if persisted != auditRows {
		t.Fatalf("canonical audit rows = %d, want %d", persisted, auditRows)
	}
}

func restartRedisOutageContainer(t *testing.T, ctx context.Context, container, originalURL string) (string, *redis.Client) {
	t.Helper()
	output, err := exec.CommandContext(ctx, "docker", "start", container).CombinedOutput()
	if err != nil {
		t.Fatalf("restart disposable Redis: %v: %s", err, output)
	}
	portOutput, err := exec.CommandContext(ctx, "docker", "port", container, "6379/tcp").CombinedOutput()
	if err != nil {
		t.Fatalf("resolve restarted Redis port: %v: %s", err, portOutput)
	}
	recoveredURL, err := redisOutageRecoveryURL(string(portOutput), os.Getenv("DOCKER_TEST_BIND_HOST"), originalURL)
	if err != nil {
		t.Fatal(err)
	}
	options, err := redis.ParseURL(recoveredURL)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(options)
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if err := client.Ping(ctx).Err(); err == nil {
			return recoveredURL, client
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = client.Close()
	t.Fatal("Redis did not recover within 15 seconds")
	return "", nil
}

// A restarted container can receive a new ephemeral port. Validate its binding
// against the companion script's explicit address, then retain the original
// client hostname: Local CI reaches the host through host.docker.internal,
// whereas a directly invoked qualification connects through loopback.
func redisOutageRecoveryURL(portOutput, bindHost, originalURL string) (string, error) {
	expected, err := netip.ParseAddr(bindHost)
	if err != nil || expected.Zone() != "" || (!expected.IsLoopback() && !expected.IsPrivate()) {
		return "", fmt.Errorf("Redis fixture requires an explicit loopback or private bind address")
	}
	endpoint, err := netip.ParseAddrPort(strings.TrimSpace(portOutput))
	if err != nil || endpoint.Port() == 0 || endpoint.Addr().Zone() != "" || endpoint.Addr().Unmap() != expected.Unmap() {
		return "", fmt.Errorf("restarted Redis must publish exactly one endpoint on the configured bind address")
	}
	original, err := url.Parse(originalURL)
	if err != nil || original.Hostname() == "" {
		return "", fmt.Errorf("invalid original Redis fixture URL")
	}
	if _, err := redis.ParseURL(originalURL); err != nil {
		return "", fmt.Errorf("invalid original Redis fixture URL")
	}
	original.Host = net.JoinHostPort(original.Hostname(), strconv.Itoa(int(endpoint.Port())))
	return original.String(), nil
}

func TestRedisOutageRecoveryURL(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, published, bindHost, original, want string
	}{
		{"loopback", "127.0.0.1:32895\n", "127.0.0.1", "redis://127.0.0.1:32894/0", "redis://127.0.0.1:32895/0"},
		{"local CI bridge", "172.17.0.1:32895\n", "172.17.0.1", "redis://host.docker.internal:32894/0", "redis://host.docker.internal:32895/0"},
		{"IPv6 loopback", "[::1]:32895\n", "::1", "redis://[::1]:32894/0", "redis://[::1]:32895/0"},
		{"preserve connection options", "172.17.0.1:32895", "172.17.0.1", "rediss://fixture:password@host.docker.internal:32894/2?protocol=3", "rediss://fixture:password@host.docker.internal:32895/2?protocol=3"},
		{"unexpected address", "172.17.0.2:32895", "172.17.0.1", "redis://host.docker.internal:32894/0", ""},
		{"unexpected bridge", "172.17.0.1:32895", "127.0.0.1", "redis://127.0.0.1:32894/0", ""},
		{"missing bind address", "127.0.0.1:32895", "", "redis://127.0.0.1:32894/0", ""},
		{"wildcard bind", "0.0.0.0:32895", "0.0.0.0", "redis://127.0.0.1:32894/0", ""},
		{"IPv6 wildcard bind", "[::]:32895", "::", "redis://[::1]:32894/0", ""},
		{"public bind", "192.0.2.1:32895", "192.0.2.1", "redis://192.0.2.1:32894/0", ""},
		{"multiple bindings", "127.0.0.1:32895\n0.0.0.0:32895\n", "127.0.0.1", "redis://127.0.0.1:32894/0", ""},
		{"zero port", "127.0.0.1:0", "127.0.0.1", "redis://127.0.0.1:32894/0", ""},
		{"out of range port", "127.0.0.1:65536", "127.0.0.1", "redis://127.0.0.1:32894/0", ""},
		{"nonnumeric port", "127.0.0.1:redis", "127.0.0.1", "redis://127.0.0.1:32894/0", ""},
		{"empty binding", "", "127.0.0.1", "redis://127.0.0.1:32894/0", ""},
		{"invalid URL", "127.0.0.1:32895", "127.0.0.1", "://broken", ""},
		{"wrong protocol", "127.0.0.1:32895", "127.0.0.1", "https://127.0.0.1:32894/0", ""},
		{"missing client hostname", "127.0.0.1:32895", "127.0.0.1", "redis:///0", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := redisOutageRecoveryURL(tt.published, tt.bindHost, tt.original)
			if tt.want == "" {
				if err == nil || got != "" {
					t.Fatalf("unsafe fixture endpoint accepted: result=%q, error=%v", got, err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("recovery URL = %q, error=%v; want %q", got, err, tt.want)
			}
		})
	}
}

func waitForRedisOutageReceiver(t *testing.T, ctx context.Context, receiver *redisOutageReceiver, want int32) {
	t.Helper()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if got := receiver.hits.Load(); got >= want {
			if got != want {
				t.Fatalf("receiver calls = %d, want %d", got, want)
			}
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal(fmt.Errorf("wait for notification receiver: %w", ctx.Err()))
		case <-ticker.C:
		}
	}
}
