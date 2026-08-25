package worker

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/alphabravocompany/astronomer-go/internal/apisvr/allowlist/providers"
	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/charlie"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/deferredreplay"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/placement"
	deliveryresolver "github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
	deliveryrollout "github.com/alphabravocompany/astronomer-go/internal/delivery/rollout"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/systemrollout"
	"github.com/alphabravocompany/astronomer-go/internal/email"
	"github.com/alphabravocompany/astronomer-go/internal/gatekeeperpolicy"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
	"github.com/alphabravocompany/astronomer-go/internal/kubectl"
	"github.com/alphabravocompany/astronomer-go/internal/maintenance"
	"github.com/alphabravocompany/astronomer-go/internal/webhook"
	"github.com/alphabravocompany/astronomer-go/internal/worker/leader"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

const (
	workerIntegrationDatabaseEnv = "ASTRONOMER_WORKER_INTEGRATION_DATABASE_URL"
	workerIntegrationRedisEnv    = "ASTRONOMER_WORKER_INTEGRATION_REDIS_URL"
	workerIntegrationHelperEnv   = "ASTRONOMER_WORKER_INTEGRATION_HELPER"
	workerIntegrationSchemaEnv   = "ASTRONOMER_WORKER_INTEGRATION_SCHEMA"
	workerIntegrationCrashEnv    = "ASTRONOMER_WORKER_INTEGRATION_CRASH_AT_CHECKPOINT"
)

type workerIntegrationPayload struct {
	RunID       string `json:"run_id"`
	OperationID string `json:"operation_id"`
}

type workerIntegrationCase struct {
	name     string
	taskType string
}

// TestWorkerPostgresRedisIntegration is intentionally opt-in. The companion
// script provisions disposable PostgreSQL and Redis containers and is the
// supported one-command entrypoint.
func TestWorkerPostgresRedisIntegration(t *testing.T) {
	databaseURL := os.Getenv(workerIntegrationDatabaseEnv)
	redisURL := os.Getenv(workerIntegrationRedisEnv)
	if databaseURL == "" || redisURL == "" {
		t.Skip("real PostgreSQL and Redis integration endpoints are not configured")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	schema := "worker_runtime_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if _, err := admin.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = admin.Exec(context.Background(), `DROP SCHEMA IF EXISTS `+schema+` CASCADE`)
	}()

	pool := openWorkerIntegrationPool(t, ctx, databaseURL, schema)
	defer pool.Close()
	if _, err := pool.Exec(ctx, workerIntegrationDDL); err != nil {
		t.Fatal(err)
	}

	redisOpt, err := asynq.ParseRedisURI(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	client := asynq.NewClient(redisOpt)
	defer func() { _ = client.Close() }()
	inspector := asynq.NewInspector(redisOpt)
	defer func() { _ = inspector.Close() }()

	runID := uuid.NewString()
	cases := []workerIntegrationCase{
		{name: "webhook", taskType: TypeWebhookDispatch},
		{name: "siem", taskType: TypeSIEMDispatch},
		{name: "gitops", taskType: tasks.GitOpsSyncType},
		{name: "cluster_registry", taskType: TypeClusterApplyRegistrySecret},
		{name: "cloud_credential", taskType: TypeCloudCredentialMaterialize},
		{name: "network_policy", taskType: TypeNetworkPolicyApply},
		{name: "cluster_snapshot", taskType: TypeClusterSnapshotApplyOperation},
		{name: "kubectl_session", taskType: TypeKubectlSessionReap},
	}

	byOwner := map[TaskOwner][]TaskDescriptor{
		TaskOwnerWorker: {},
		TaskOwnerTunnel: {},
	}
	for _, tc := range cases {
		descriptor := integrationDescriptor(t, tc.taskType)
		descriptor.Handler = integrationEffectHandler(pool, false)
		byOwner[descriptor.Owner] = append(byOwner[descriptor.Owner], descriptor)
		insertWorkerIntegrationIntent(t, ctx, pool, runID, tc.name, descriptor)
	}

	standalone := newWorkerIntegrationConsumer(t, redisURL, byOwner[TaskOwnerWorker], map[string]int{"default": 1})
	tunnel := newWorkerIntegrationConsumer(t, redisURL, byOwner[TaskOwnerTunnel], map[string]int{TunnelQueueName: 1})
	standalone.RegisterHandlers()
	tunnel.RegisterTunnelHandlers()
	if err := standalone.Start(); err != nil {
		t.Fatal(err)
	}
	defer standalone.Shutdown()
	if err := tunnel.Start(); err != nil {
		t.Fatal(err)
	}
	defer tunnel.Shutdown()

	for _, tc := range cases {
		descriptor := integrationDescriptor(t, tc.taskType)
		_ = enqueueWorkerIntegrationTask(t, client, tc.taskType, descriptor.Queue, runID, tc.name)
	}
	waitForWorkerIntegration(t, ctx, pool, func() (bool, error) {
		var completed int
		err := pool.QueryRow(ctx, `SELECT count(*) FROM operations WHERE run_id=$1 AND phase='completed'`, runID).Scan(&completed)
		return completed == len(cases), err
	})
	assertWorkerIntegrationFamilies(t, ctx, pool, runID, cases)

	standalone.Shutdown()
	tunnel.Shutdown()
	testWorkerProductionBusinessHandlers(t, ctx, admin, redisURL, client, inspector)
	testWorkerCrashReplacement(t, ctx, databaseURL, redisURL, schema, pool, client, inspector, runID)
}

// TestWorkerIntegrationProcessHelper runs an actual Asynq consumer in a child
// process so the parent can terminate it at the committed execution checkpoint.
func TestWorkerIntegrationProcessHelper(t *testing.T) {
	if os.Getenv(workerIntegrationHelperEnv) != "1" {
		t.Skip("subprocess helper")
	}
	databaseURL := os.Getenv(workerIntegrationDatabaseEnv)
	redisURL := os.Getenv(workerIntegrationRedisEnv)
	schema := os.Getenv(workerIntegrationSchemaEnv)
	ctx := context.Background()
	pool := openWorkerIntegrationPool(t, ctx, databaseURL, schema)
	defer pool.Close()
	descriptor := integrationDescriptor(t, TypeTaskOutboxDispatch)
	descriptor.Handler = integrationEffectHandler(pool, os.Getenv(workerIntegrationCrashEnv) == "1")
	consumer := newWorkerIntegrationConsumer(t, redisURL, []TaskDescriptor{descriptor}, map[string]int{descriptor.Queue: 1})
	consumer.RegisterHandlers()
	if err := consumer.Start(); err != nil {
		t.Fatal(err)
	}
	select {}
}

func testWorkerCrashReplacement(t *testing.T, ctx context.Context, databaseURL, redisURL, schema string, pool *pgxpool.Pool, client *asynq.Client, inspector *asynq.Inspector, runID string) {
	t.Helper()
	descriptor := integrationDescriptor(t, TypeTaskOutboxDispatch)
	operationID := "crash_recovery"
	insertWorkerIntegrationIntent(t, ctx, pool, runID, operationID, descriptor)

	first := startWorkerIntegrationProcess(t, databaseURL, redisURL, schema, true)
	taskID := enqueueWorkerIntegrationTask(t, client, descriptor.Type, descriptor.Queue, runID, operationID)
	waitForWorkerIntegration(t, ctx, pool, func() (bool, error) {
		var phase string
		var attempts int
		var effects int
		err := pool.QueryRow(ctx, `
			SELECT o.phase, o.attempt_count, count(e.operation_id)
			FROM operations o LEFT JOIN external_effects e USING (run_id, operation_id)
			WHERE o.run_id=$1 AND o.operation_id=$2 GROUP BY o.phase, o.attempt_count`, runID, operationID).Scan(&phase, &attempts, &effects)
		return phase == "execution_checkpoint" && attempts == 1 && effects == 0, err
	})
	assertWorkerIntegrationTaskState(t, inspector, descriptor.Queue, taskID, asynq.TaskStateActive, 0)
	if err := first.Process.Kill(); err != nil {
		t.Fatalf("terminate checkpointed worker: %v", err)
	}
	_ = first.Wait()

	replacement := startWorkerIntegrationProcess(t, databaseURL, redisURL, schema, false)
	defer stopWorkerIntegrationProcess(replacement)
	// Do not enqueue recovery work here. The replacement's production Asynq
	// recoverer must observe the original active task's expired lease and move
	// that same task through retry before its handler can run again.
	waitForWorkerIntegrationTaskState(t, ctx, inspector, descriptor.Queue, taskID, asynq.TaskStateRetry, 1)
	waitForWorkerIntegration(t, ctx, pool, func() (bool, error) {
		return workerIntegrationState(ctx, pool, runID, operationID, 2)
	})
	// A duplicate recovery delivery must not duplicate the external effect or
	// its audit events.
	_ = enqueueWorkerIntegrationTask(t, client, descriptor.Type, descriptor.Queue, runID, operationID)
	waitForWorkerIntegration(t, ctx, pool, func() (bool, error) {
		return workerIntegrationState(ctx, pool, runID, operationID, 3)
	})

	var effects, effectAudits, completionAudits int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM external_effects WHERE run_id=$1 AND operation_id=$2`, runID, operationID).Scan(&effects); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE event_type='effect_applied'), count(*) FILTER (WHERE event_type='operation_completed') FROM audit_events WHERE run_id=$1 AND operation_id=$2`, runID, operationID).Scan(&effectAudits, &completionAudits); err != nil {
		t.Fatal(err)
	}
	if effects != 1 || effectAudits != 1 || completionAudits != 1 {
		t.Fatalf("crash recovery invariants: effects=%d effect_audits=%d completion_audits=%d, want 1/1/1", effects, effectAudits, completionAudits)
	}
}

func workerIntegrationState(ctx context.Context, pool *pgxpool.Pool, runID, operationID string, minimumAttempts int) (bool, error) {
	var phase string
	var attempts, effects int
	err := pool.QueryRow(ctx, `
		SELECT o.phase, o.attempt_count, count(e.operation_id)
		FROM operations o LEFT JOIN external_effects e USING (run_id, operation_id)
		WHERE o.run_id=$1 AND o.operation_id=$2 GROUP BY o.phase, o.attempt_count`, runID, operationID).Scan(&phase, &attempts, &effects)
	return phase == "completed" && attempts >= minimumAttempts && effects == 1, err
}

type workerBusinessFixtures struct {
	clusterID                 uuid.UUID
	upgradeID                 uuid.UUID
	taskOutboxID              uuid.UUID
	auditOutboxID             uuid.UUID
	catalogRepoID             uuid.UUID
	emailID                   uuid.UUID
	webhookOKID               uuid.UUID
	webhookRetryID            uuid.UUID
	siemOKID                  uuid.UUID
	siemRetryID               uuid.UUID
	deliveryResolutionID      uuid.UUID
	deliveryRolloutID         uuid.UUID
	deliverySystemRolloutID   uuid.UUID
	charlieDeliveryID         uuid.UUID
	charliePlannedFindingID   uuid.UUID
	managementBackupID        uuid.UUID
	managementOperationID     uuid.UUID
	gitOpsSourceID            uuid.UUID
	gitOpsClusterName         string
	adminQueueOperationID     uuid.UUID
	adminArchivedTaskID       string
	backupCredentialID        uuid.UUID
	registryCredentialID      uuid.UUID
	helmCredentialID          uuid.UUID
	monitoringCredentialID    uuid.UUID
	podDeleteOperationID      uuid.UUID
	resourceOperationID       uuid.UUID
	nodeOperationID           uuid.UUID
	vulnerabilityOperationID  uuid.UUID
	gatekeeperConstraintName  string
	backupExecutionID         uuid.UUID
	restoreSourceBackupID     uuid.UUID
	restoreOperationID        uuid.UUID
	securityIngestScanID      uuid.UUID
	projectReconcileID        uuid.UUID
	projectNamespace          string
	clusterTemplateID         uuid.UUID
	networkPolicyTemplateID   uuid.UUID
	networkPolicyAppID        uuid.UUID
	snapshotApplyID           uuid.UUID
	snapshotPollID            uuid.UUID
	snapshotExpiredID         uuid.UUID
	snapshotScheduleID        uuid.UUID
	controlPlaneApplyID       uuid.UUID
	controlPlaneSweepCluster  uuid.UUID
	kubectlSessionID          uuid.UUID
	kubectlUserID             uuid.UUID
	clusterGroupID            uuid.UUID
	gatekeeperRecoveryName    string
	cloudCredentialID         uuid.UUID
	cloudDirectMaterialID     uuid.UUID
	cloudDriftMaterialID      uuid.UUID
	toolDriftChartID          uuid.UUID
	charlieTriggerEventID     uuid.UUID
	decommissionDirectID      uuid.UUID
	decommissionSweepID       uuid.UUID
	decommissionDirectCluster uuid.UUID
	decommissionSweepCluster  uuid.UUID
	deferredOperationID       uuid.UUID
	deferredWindowID          uuid.UUID
	durableTimestamps         map[string]time.Time
	credentialCiphertexts     map[string]string
	credentialEncryptor       *auth.Encryptor
	managementKubernetes      *fake.Clientset
	managementNamespace       string
	logs                      *bytes.Buffer
	tunnelK8s                 *workerTunnelK8sFixture
	downstreamQueue           string
	downstreamBody            []byte
	outbound                  *workerOutboundFixture
	deliveryHTTP              *workerDeliveryHTTPFixture
	helmStatus                *workerHelmStatusFixture
	decommissionTunnel        *workerDecommissionTunnelFixture
	deferredReplay            *workerDeferredReplayFixture
}

const workerAdminArchivedTargetType = "worker-integration:admin-archived-target"

type workerGitOpsFixture struct {
	repoURL     string
	clusterName string
}

type workerTunnelK8sFixture struct {
	mu                        sync.Mutex
	calls                     map[string]int
	unique                    map[string]map[[32]byte]struct{}
	gatekeeperPolicyClusterID string
}

type workerHelmStatusFixture struct {
	mu     sync.Mutex
	calls  int
	unique map[string]struct{}
}

func (f *workerHelmStatusFixture) Status(_ context.Context, clusterID, releaseName, namespace string) (*protocol.HelmResultPayload, error) {
	f.mu.Lock()
	f.calls++
	f.unique[clusterID+"/"+namespace+"/"+releaseName] = struct{}{}
	f.mu.Unlock()
	return &protocol.HelmResultPayload{Status: "deployed", Revision: 2}, nil
}

func (f *workerHelmStatusFixture) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls, len(f.unique)
}

type workerDecommissionTunnelFixture struct {
	mu          sync.Mutex
	sends       map[string]int
	disconnects map[string]int
}

func (f *workerDecommissionTunnelFixture) SendDecommission(_ context.Context, clusterID string, _ protocol.DecommissionPayload, _ time.Duration) (*protocol.DecommissionAckPayload, bool, error) {
	f.mu.Lock()
	f.sends[clusterID]++
	f.mu.Unlock()
	return &protocol.DecommissionAckPayload{}, true, nil
}

func (f *workerDecommissionTunnelFixture) Disconnect(clusterID string) bool {
	f.mu.Lock()
	f.disconnects[clusterID]++
	f.mu.Unlock()
	return true
}

func (f *workerDecommissionTunnelFixture) counts(clusterID string) (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sends[clusterID], f.disconnects[clusterID]
}

type workerCharlieBridgeFixture struct{}

func (workerCharlieBridgeFixture) CreateInvestigation(context.Context, charlie.BridgeInvestigationRequest, string) (charlie.BridgeSessionReceipt, error) {
	return charlie.BridgeSessionReceipt{}, errors.New("unexpected Charlie bridge call for inactive rule")
}

type workerDeferredReplayFixture struct {
	mu      sync.Mutex
	jwt     *auth.JWTManager
	userID  uuid.UUID
	opID    uuid.UUID
	path    string
	calls   int
	lastErr string
}

func (f *workerDeferredReplayFixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	claims, err := f.jwt.ValidateToken(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	switch {
	case err != nil || claims.UserID != f.userID:
		f.lastErr = fmt.Sprintf("replay principal claims=%+v err=%v", claims, err)
	case r.Method != http.MethodPost || r.URL.Path != f.path:
		f.lastErr = "unexpected replay target " + r.Method + " " + r.URL.Path
	case r.Header.Get("Idempotency-Key") != "deferred-replay:"+f.opID.String():
		f.lastErr = "missing stable replay idempotency key"
	case r.Header.Get("X-Correlation-ID") != "deferred-replay:"+f.opID.String():
		f.lastErr = "missing stable replay correlation ID"
	}
	if f.lastErr != "" {
		http.Error(w, f.lastErr, http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (f *workerDeferredReplayFixture) result() (int, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls, f.lastErr
}

type workerProjectK8sAdapter struct{ requester *workerTunnelK8sFixture }

func (a workerProjectK8sAdapter) Do(ctx context.Context, clusterID, method, path string, body []byte, headers map[string]string) (*tasks.ProjectK8sResponse, error) {
	response, err := a.requester.Do(ctx, clusterID, method, path, body, headers)
	if err != nil || response == nil {
		return nil, err
	}
	decoded, err := base64.StdEncoding.DecodeString(response.Body)
	if err != nil {
		return nil, fmt.Errorf("decode tunnel response: %w", err)
	}
	return &tasks.ProjectK8sResponse{StatusCode: response.StatusCode, Body: decoded}, nil
}

func newWorkerTunnelK8sFixture() *workerTunnelK8sFixture {
	return &workerTunnelK8sFixture{calls: make(map[string]int), unique: make(map[string]map[[32]byte]struct{})}
}

func (f *workerTunnelK8sFixture) Do(_ context.Context, clusterID, method, path string, body []byte, _ map[string]string) (*protocol.K8sResponsePayload, error) {
	key := method + " " + clusterID + " " + path
	digest := sha256.Sum256(body)
	f.mu.Lock()
	f.calls[key]++
	if f.unique[key] == nil {
		f.unique[key] = make(map[[32]byte]struct{})
	}
	f.unique[key][digest] = struct{}{}
	gatekeeperPolicyClusterID := f.gatekeeperPolicyClusterID
	f.mu.Unlock()
	if method == http.MethodGet && path == "/apis/templates.gatekeeper.sh/v1/constrainttemplates" && clusterID == gatekeeperPolicyClusterID {
		return &protocol.K8sResponsePayload{StatusCode: http.StatusOK, Body: base64.StdEncoding.EncodeToString([]byte(`{"items":[]}`))}, nil
	}
	if method == http.MethodGet && path == "/apis/aquasecurity.github.io/v1alpha1/vulnerabilityreports" {
		payload := `{"items":[{"metadata":{"name":"worker-report","namespace":"default"}}]}`
		return &protocol.K8sResponsePayload{StatusCode: http.StatusOK, Body: base64.StdEncoding.EncodeToString([]byte(payload))}, nil
	}
	if method == http.MethodGet && path == "/api/v1/namespaces" {
		return &protocol.K8sResponsePayload{StatusCode: http.StatusOK, Body: base64.StdEncoding.EncodeToString([]byte(`{"items":[]}`))}, nil
	}
	if method == http.MethodGet && path == "/apis/cis.cattle.io/v1/clusterscans/worker-cis-scan" {
		payload := `{"status":{"reportName":"worker-cis-report"}}`
		return &protocol.K8sResponsePayload{StatusCode: http.StatusOK, Body: base64.StdEncoding.EncodeToString([]byte(payload))}, nil
	}
	if method == http.MethodGet && path == "/apis/cis.cattle.io/v1/clusterscanreports/worker-cis-report" {
		payload := `{"metadata":{"name":"worker-cis-report"},"spec":{"reportJSON":"{\"total\":2,\"pass\":1,\"fail\":1,\"warn\":0,\"skip\":0,\"results\":[]}"}}`
		return &protocol.K8sResponsePayload{StatusCode: http.StatusOK, Body: base64.StdEncoding.EncodeToString([]byte(payload))}, nil
	}
	if method == http.MethodGet && strings.HasPrefix(path, "/apis/velero.io/v1/namespaces/velero/backups/") {
		payload := `{"status":{"phase":"Completed","startTimestamp":"2026-01-01T00:00:00Z","completionTimestamp":"2026-01-01T00:01:00Z","warnings":0,"errors":0}}`
		return &protocol.K8sResponsePayload{StatusCode: http.StatusOK, Body: base64.StdEncoding.EncodeToString([]byte(payload))}, nil
	}
	if method == http.MethodGet && strings.HasPrefix(path, "/apis/batch/v1/namespaces/kube-system/jobs/cp-snapshot-") {
		payload := `{"status":{"conditions":[{"type":"Complete","status":"True"}]}}`
		return &protocol.K8sResponsePayload{StatusCode: http.StatusOK, Body: base64.StdEncoding.EncodeToString([]byte(payload))}, nil
	}
	if method == http.MethodPost {
		return &protocol.K8sResponsePayload{StatusCode: http.StatusCreated, Body: base64.StdEncoding.EncodeToString([]byte(`{}`))}, nil
	}
	if method == http.MethodPatch {
		payload := `{"metadata":{"resourceVersion":"worker-rv-1"}}`
		return &protocol.K8sResponsePayload{StatusCode: http.StatusOK, Body: base64.StdEncoding.EncodeToString([]byte(payload))}, nil
	}
	return &protocol.K8sResponsePayload{StatusCode: http.StatusNotFound}, nil
}

func (f *workerTunnelK8sFixture) count(method, clusterID, path string) (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := method + " " + clusterID + " " + path
	return f.calls[key], len(f.unique[key])
}

func (f *workerTunnelK8sFixture) prefixCount(method, clusterID, pathPrefix string) (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	prefix := method + " " + clusterID + " " + pathPrefix
	total := 0
	allUnique := make(map[[32]byte]struct{})
	for key, count := range f.calls {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		total += count
		for digest := range f.unique[key] {
			allUnique[digest] = struct{}{}
		}
	}
	return total, len(allUnique)
}

func (f *workerTunnelK8sFixture) containsCount(method, clusterID, pathFragment string) (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	prefix := method + " " + clusterID + " "
	total := 0
	allUnique := make(map[[32]byte]struct{})
	for key, count := range f.calls {
		if !strings.HasPrefix(key, prefix) || !strings.Contains(key, pathFragment) {
			continue
		}
		total += count
		for digest := range f.unique[key] {
			allUnique[digest] = struct{}{}
		}
	}
	return total, len(allUnique)
}

func newWorkerGitOpsFixture(t *testing.T) workerGitOpsFixture {
	t.Helper()
	bare := filepath.Join(t.TempDir(), "bare.git")
	if _, err := git.PlainInit(bare, true); err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(t.TempDir(), "work")
	repo, err := git.PlainInit(work, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateRemote(&gitconfig.RemoteConfig{Name: "origin", URLs: []string{bare}}); err != nil {
		t.Fatal(err)
	}
	clusterName := "worker-gitops-" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	relative := filepath.Join("clusters", clusterName+".yaml")
	full := filepath.Join(work, relative)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "apiVersion: astronomer.alphabravo.io/v1\nkind: ClusterRegistration\nmetadata:\n  name: " + clusterName + "\nspec:\n  labels:\n    environment: integration\n"
	if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	worktree, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := worktree.Add(relative); err != nil {
		t.Fatal(err)
	}
	if _, err := worktree.Commit("worker integration registration", &git.CommitOptions{Author: &object.Signature{
		Name: "worker-integration", Email: "worker@example.test", When: time.Now().UTC(),
	}}); err != nil {
		t.Fatal(err)
	}
	head, err := repo.Head()
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Push(&git.PushOptions{RemoteName: "origin", RefSpecs: []gitconfig.RefSpec{
		gitconfig.RefSpec(head.Name().String() + ":refs/heads/main"),
	}}); err != nil {
		t.Fatal(err)
	}
	return workerGitOpsFixture{repoURL: "file://" + bare, clusterName: clusterName}
}

type workerOutboundFixture struct {
	server *httptest.Server
	smtp   *workerSMTPFixture

	mu       sync.Mutex
	requests map[string]int
	unique   map[string]map[[32]byte]struct{}
}

func newWorkerOutboundFixture(t *testing.T) *workerOutboundFixture {
	t.Helper()
	f := &workerOutboundFixture{
		requests: make(map[string]int),
		unique:   make(map[string]map[[32]byte]struct{}),
		smtp:     newWorkerSMTPFixture(t),
	}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		f.mu.Lock()
		f.requests[r.URL.Path]++
		attempt := f.requests[r.URL.Path]
		logicalBody := body
		if r.URL.Path == "/telemetry" {
			var payload tasks.TelemetryPayload
			if json.Unmarshal(body, &payload) == nil {
				logicalBody = []byte(payload.InstanceID)
			}
		}
		if f.unique[r.URL.Path] == nil {
			f.unique[r.URL.Path] = make(map[[32]byte]struct{})
		}
		f.unique[r.URL.Path][sha256.Sum256(logicalBody)] = struct{}{}
		f.mu.Unlock()

		switch r.URL.Path {
		case "/catalog/index.yaml":
			w.Header().Set("Content-Type", "application/yaml")
			_, _ = io.WriteString(w, "apiVersion: v1\nentries:\n  worker-integration-chart:\n    - apiVersion: v2\n      name: worker-integration-chart\n      version: 1.0.0\n      description: integration chart\ngenerated: 2026-01-01T00:00:00Z\n")
		case "/webhook-recover", "/siem-recover", "/charlie-recover":
			if attempt == 1 {
				http.Error(w, "temporary receiver failure", http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

type workerDeliveryHTTPFixture struct {
	server     *httptest.Server
	url        string
	caBundle   []byte
	policy     deliveryresolver.NetworkPolicy
	chart      []byte
	mu         sync.Mutex
	indexReads int
	chartReads int
}

func newWorkerDeliveryHTTPFixture(t *testing.T) *workerDeliveryHTTPFixture {
	t.Helper()
	ip := workerIntegrationPrivateIP(t)
	caPEM, certificate := workerIntegrationTLSCertificate(t, ip)
	f := &workerDeliveryHTTPFixture{
		caBundle: caPEM,
		chart:    []byte("worker integration immutable chart bytes\n"),
		policy: deliveryresolver.NetworkPolicy{
			AllowedPrivateHosts: []string{ip.String()},
			AllowedPrivateCIDRs: []netip.Prefix{netip.PrefixFrom(ip, ip.BitLen())},
		},
	}
	listener, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	f.server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		switch r.URL.Path {
		case "/delivery/index.yaml":
			f.indexReads++
		case "/delivery/chart.tgz":
			f.chartReads++
		}
		f.mu.Unlock()
		switch r.URL.Path {
		case "/delivery/index.yaml":
			digest := sha256.Sum256(f.chart)
			_, _ = fmt.Fprintf(w, "apiVersion: v1\nentries:\n  worker-delivery-chart:\n    - apiVersion: v2\n      name: worker-delivery-chart\n      version: 1.0.0\n      urls: [chart.tgz]\n      digest: %x\ngenerated: 2026-01-01T00:00:00Z\n", digest)
		case "/delivery/chart.tgz":
			_, _ = w.Write(f.chart)
		default:
			http.NotFound(w, r)
		}
	}))
	f.server.Listener = listener
	f.server.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}
	f.server.StartTLS()
	port := listener.Addr().(*net.TCPAddr).Port
	f.url = "https://" + net.JoinHostPort(ip.String(), strconv.Itoa(port))
	t.Cleanup(f.server.Close)
	return f
}

func (f *workerDeliveryHTTPFixture) counts() (indexReads, chartReads int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.indexReads, f.chartReads
}

func workerIntegrationPrivateIP(t *testing.T) netip.Addr {
	t.Helper()
	interfaces, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	for _, iface := range interfaces {
		addresses, addressErr := iface.Addrs()
		if addressErr != nil {
			continue
		}
		for _, raw := range addresses {
			prefix, parseErr := netip.ParsePrefix(raw.String())
			if parseErr == nil && prefix.Addr().Is4() && prefix.Addr().IsPrivate() && !prefix.Addr().IsLoopback() {
				return prefix.Addr()
			}
		}
	}
	t.Fatal("delivery fixture requires a non-loopback private IPv4 address")
	return netip.Addr{}
}

func workerIntegrationTLSCertificate(t *testing.T, ip netip.Addr) ([]byte, tls.Certificate) {
	t.Helper()
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "worker integration CA"},
		NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), IsCA: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	leafKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: ip.String()},
		NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IPAddresses: []net.IP{net.IP(ip.AsSlice())},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, caTemplate, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	leafPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(leafKey)})
	certificate, err := tls.X509KeyPair(leafPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return caPEM, certificate
}

func (f *workerOutboundFixture) counts(path string) (requests, unique int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[path], len(f.unique[path])
}

type workerSMTPFixture struct {
	listener net.Listener
	mu       sync.Mutex
	messages int
	unique   map[[32]byte]struct{}
	wg       sync.WaitGroup
}

func newWorkerSMTPFixture(t *testing.T) *workerSMTPFixture {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &workerSMTPFixture{listener: listener, unique: make(map[[32]byte]struct{})}
	f.wg.Add(1)
	go func() {
		defer f.wg.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			f.wg.Add(1)
			go func() {
				defer f.wg.Done()
				defer func() { _ = conn.Close() }()
				_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
				reader := bufio.NewReader(conn)
				writer := bufio.NewWriter(conn)
				_, _ = writer.WriteString("220 worker-integration ESMTP\r\n")
				_ = writer.Flush()
				for {
					line, err := reader.ReadString('\n')
					if err != nil {
						return
					}
					command := strings.ToUpper(strings.TrimSpace(line))
					switch {
					case strings.HasPrefix(command, "EHLO"), strings.HasPrefix(command, "HELO"):
						_, _ = writer.WriteString("250-worker-integration\r\n250 PIPELINING\r\n")
					case strings.HasPrefix(command, "MAIL FROM"), strings.HasPrefix(command, "RCPT TO"):
						_, _ = writer.WriteString("250 accepted\r\n")
					case command == "DATA":
						_, _ = writer.WriteString("354 end with dot\r\n")
						_ = writer.Flush()
						var message bytes.Buffer
						for {
							dataLine, readErr := reader.ReadString('\n')
							if readErr != nil {
								return
							}
							if dataLine == ".\r\n" {
								break
							}
							message.WriteString(dataLine)
						}
						f.mu.Lock()
						f.messages++
						f.unique[sha256.Sum256(message.Bytes())] = struct{}{}
						f.mu.Unlock()
						_, _ = writer.WriteString("250 queued\r\n")
					case command == "QUIT":
						_, _ = writer.WriteString("221 bye\r\n")
						_ = writer.Flush()
						return
					default:
						_, _ = writer.WriteString("250 ok\r\n")
					}
					_ = writer.Flush()
				}
			}()
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		f.wg.Wait()
	})
	return f
}

func (f *workerSMTPFixture) address() (string, int) {
	host, portText, _ := net.SplitHostPort(f.listener.Addr().String())
	port, _ := strconv.Atoi(portText)
	return host, port
}

func (f *workerSMTPFixture) counts() (messages, unique int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.messages, len(f.unique)
}

type workerDeliveryDecryptor struct{}

func (workerDeliveryDecryptor) DecryptBytes(string) ([]byte, error) {
	return nil, errors.New("worker integration fixture has no encrypted delivery material")
}

type workerDeliveryPolicyProvider struct {
	policy deliveryresolver.NetworkPolicy
}

func (p workerDeliveryPolicyProvider) NetworkPolicy(context.Context, uuid.UUID, uuid.UUID) (deliveryresolver.NetworkPolicy, error) {
	return p.policy, nil
}

func (workerDeliveryPolicyProvider) ProxyURL(context.Context, uuid.UUID, string) (string, error) {
	return "", nil
}

// testWorkerProductionBusinessHandlers complements the routing/crash proof
// above with real business handlers. It deliberately includes only handlers
// whose exercised path is backed by disposable local dependencies. Outbound
// handlers use loopback HTTP/SMTP receivers while retaining their production
// sender implementations.
func testWorkerProductionBusinessHandlers(t *testing.T, ctx context.Context, pool *pgxpool.Pool, redisURL string, client *asynq.Client, inspector *asynq.Inspector) {
	t.Helper()
	restoreGuard := httpclient.DisableGuardForTest()
	defer restoreGuard()
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	previousLog := slog.Default()
	slog.SetDefault(log)
	defer slog.SetDefault(previousLog)
	outbound := newWorkerOutboundFixture(t)
	deliveryHTTP := newWorkerDeliveryHTTPFixture(t)
	gitOpsFixture := newWorkerGitOpsFixture(t)
	tunnelK8s := newWorkerTunnelK8sFixture()
	helmStatus := &workerHelmStatusFixture{unique: make(map[string]struct{})}
	decommissionTunnel := &workerDecommissionTunnelFixture{sends: make(map[string]int), disconnects: make(map[string]int)}
	queries := sqlc.New(pool)
	encryptionKey, err := auth.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := auth.NewEncryptor(encryptionKey)
	if err != nil {
		t.Fatal(err)
	}
	managementNamespace := "worker-management-backup"
	managementOperationID := uuid.New()
	managementKubernetes := fake.NewSimpleClientset(&batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name: "management-backup-op-" + managementOperationID.String(), Namespace: managementNamespace,
		},
		Status: batchv1.JobStatus{Succeeded: 1},
	})
	backupExecutor := handler.NewAdminDrillHandler(queries)
	backupExecutor.SetEncryptor(encryptor)
	backupExecutor.SetBackupRuntime("worker.example.test/management-backup:integration", "worker-management-backup")
	backupExecutor.SetKubernetes(managementKubernetes, managementNamespace, "worker-integration")
	core := tasks.CoreRuntime{Deps: tasks.RuntimeDependencies{
		Queries:           queries,
		Leader:            leader.New(pool, log),
		Enqueuer:          client,
		HTTPClient:        outbound.server.Client(),
		Log:               log,
		ManagementBackup:  backupExecutor,
		K8s:               tunnelK8s,
		ResourceDecryptor: encryptor,
	}}
	emailProvider := email.NewSQLSettingsProvider(queries, nil, 0)
	emailSender := email.NewSender(emailProvider, nil, log)
	deliverySourceWorker, err := deliveryresolver.NewPostgresWorker(
		pool, deliveryresolver.New(nil), workerDeliveryDecryptor{},
		workerDeliveryPolicyProvider{policy: deliveryHTTP.policy}, "worker-integration-source-resolver",
	)
	if err != nil {
		t.Fatal(err)
	}
	deliverySourceWorker.SetBaseCABundle(deliveryHTTP.caBundle)
	deliveryRolloutReconciler, err := deliveryrollout.NewPostgresReconciler(
		pool, maintenance.NewEvaluator(queries), "worker-integration-rollout",
	)
	if err != nil {
		t.Fatal(err)
	}
	deliverySystemReconciler, err := systemrollout.New(pool)
	if err != nil {
		t.Fatal(err)
	}
	deliveryRuntime := tasks.DeliveryRuntime{
		SourceResolver: deliverySourceWorker, RolloutReconciler: deliveryRolloutReconciler,
		SystemRolloutReconciler: deliverySystemReconciler,
	}
	charliePlanner, err := charlie.NewFindingAlertPlanner(pool)
	if err != nil {
		t.Fatal(err)
	}
	charlieRuntime := tasks.CharlieAlertRuntime{
		Queries: queries, WriteFence: charlie.NewDistributedWriteFence(pool), Reconciler: charliePlanner,
	}
	dispatch := tasks.DispatchRuntime{
		Email:       tasks.EmailDeps{Queries: queries, Sender: emailSender, Provider: emailProvider},
		Webhook:     tasks.WebhookDeps{Queries: queries, Sender: webhook.NewSender(outbound.server.Client())},
		SIEM:        tasks.SIEMDeps{Queries: queries, HTTPClient: outbound.server.Client()},
		TaskOutbox:  tasks.TaskOutboxDispatchDeps{Queries: queries, Enqueuer: client},
		AuditOutbox: tasks.AuditOutboxDispatchDeps{Queries: queries},
		AdminQueue:  tasks.AdminQueueOperationDeps{Queries: queries, Inspector: inspector},
	}
	maintenance := tasks.MaintenanceRuntime{
		AgentTokens: tasks.AgentTokenRotateDeps{Queries: queries},
		PlaintextCredentials: tasks.PlaintextCredentialMigrationDeps{
			Queries: queries, Encryptor: encryptor,
		},
	}
	gitOpsRuntime := tasks.GitOpsRuntime{Deps: tasks.GitOpsDeps{
		Queries: queries, Enqueuer: client, TaskOutbox: queries, Decryptor: encryptor,
		Log: log, CloneRoot: t.TempDir(), Now: time.Now,
	}}
	meshRuntime := tasks.MeshRuntime{Deps: tasks.MeshDetectDeps{Queries: queries, Requester: tunnelK8s}}
	crdOwnershipRuntime := tasks.CRDOwnershipRuntime{Deps: tasks.CRDOwnershipDriftDeps{
		Queries: queries, Dynamic: dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()),
	}}
	allowlistRegistry := providers.NewRegistry()
	allowlistRegistry.Register(providers.NewSelfManagedProvider())
	allowlists := tasks.ApiserverAllowlistRuntime{Deps: tasks.ApiserverAllowlistReconcileDeps{
		Queries: queries, Registry: allowlistRegistry, ClusterShaper: providers.ClusterFromSQLC,
		AstronomerEgress: []string{"203.0.113.0/24"}, AuditWriter: queries,
	}}
	securityIngestRuntime := tasks.SecurityIngestRuntime{
		Deps: tasks.SecurityIngestDeps{
			Queries: queries, K8s: tunnelK8s, Outbox: queries, Log: log,
			Owner: "worker-integration-security", Now: time.Now,
		},
		Leader: leader.New(pool, log),
	}
	projectRequester := workerProjectK8sAdapter{requester: tunnelK8s}
	projectRuntime := tasks.ProjectRuntime{Deps: tasks.ProjectReconcileDeps{
		Queries: queries, Requester: projectRequester, Encryptor: encryptor,
	}}
	clusterTemplateRuntime := tasks.ClusterTemplateRuntime{
		Deps: tasks.ClusterTemplateApplyDeps{
			Queries: queries, Installer: handler.NewToolHandler(queries),
		},
		RecoveryEnqueuer: client,
	}
	clusterRegistryRuntime := tasks.ClusterRegistryRuntime{Deps: tasks.ClusterRegistryApplyDeps{
		Queries: queries, Requester: projectRequester, Encryptor: encryptor,
	}}
	networkPolicyRuntime := tasks.NetworkPolicyRuntime{Deps: tasks.NetworkPolicyApplyDeps{
		Queries: queries, Requester: tunnelK8s,
	}}
	clusterSnapshotRuntime := tasks.ClusterSnapshotRuntime{Deps: tasks.ClusterSnapshotDeps{
		Queries: queries, Driver: handler.NewVeleroDriverAdapter(tunnelK8s), Log: log,
	}}
	controlPlaneSnapshotHandler := handler.NewControlPlaneSnapshotHandler(queries)
	controlPlaneSnapshotHandler.SetRequester(tunnelK8s)
	controlPlaneSnapshotRuntime := tasks.ControlPlaneSnapshotRuntime{
		Deps:         tasks.ControlPlaneSnapshotSweepDeps{Queries: queries, Log: log},
		Applier:      controlPlaneSnapshotHandler.ApplySnapshotJob,
		StatusReader: controlPlaneSnapshotHandler.ReadSnapshotJobStatus,
	}
	kubectlRuntime := tasks.KubectlSessionReapRuntime{
		Deps: kubectl.Deps{
			Queries: queries, Requester: handler.KubectlK8sRequesterFromHandlerRequester(tunnelK8s), Log: log,
		},
		Leader: leader.New(pool, log), Log: log,
	}
	previousGroupMetricsRefresher := tasks.ClusterGroupMetricsRefresher
	tasks.ClusterGroupMetricsRefresher = func(ctx context.Context) {
		handler.RefreshClusterGroupMetrics(ctx, queries)
	}
	defer func() { tasks.ClusterGroupMetricsRefresher = previousGroupMetricsRefresher }()
	cloudCredentialRuntime := tasks.CloudCredentialRuntime{Deps: tasks.CloudCredentialMaterializeDeps{
		Queries: queries, Requester: projectRequester, Decryptor: encryptor,
	}}
	toolDriftRuntime := tasks.ToolDriftRuntime{Deps: tasks.ToolDriftSweepDeps{Queries: queries, Helm: helmStatus}}
	charlieTriggerDispatcher, err := charlie.NewTriggerDispatcher(
		queries, workerCharlieBridgeFixture{}, charlie.NewEventTriggerLifecyclePublisher(nil),
		charlie.NewDBLifecycleAuditor(queries), func() bool { return true },
	)
	if err != nil {
		t.Fatal(err)
	}
	charlieTriggerRuntime := &tasks.CharlieTriggerRuntime{}
	charlieTriggerRuntime.SetDispatcher(charlieTriggerDispatcher)
	clusterDecommissionRuntime := tasks.ClusterDecommissionRuntime{Deps: tasks.ClusterDecommissionDeps{
		Queries: queries, Tunnel: decommissionTunnel, TunnelWait: time.Second,
	}}
	deferredJWT := auth.MustNewJWTManager("worker-integration-deferred-replay-signing-key", 60)
	deferredReplayFixture := &workerDeferredReplayFixture{jwt: deferredJWT}
	deferredReplayers, err := deferredreplay.NewHTTPReplayers(deferredReplayFixture, deferredJWT, encryptor, queries)
	if err != nil {
		t.Fatal(err)
	}
	deferredRuntime := tasks.DeferredRuntime{Deps: tasks.DeferredDispatchDeps{Queries: queries, Replayers: deferredReplayers}}
	overrides := map[string]asynq.HandlerFunc{
		tasks.DeliverySourceResolutionType:         deliveryRuntime.HandleDeliverySourceResolution,
		tasks.DeliveryRolloutReconcileType:         deliveryRuntime.HandleDeliveryRolloutReconcile,
		tasks.DeliverySystemRolloutReconcileType:   deliveryRuntime.HandleDeliverySystemRolloutReconcile,
		TypeCharlieAlertReconcile:                  charlieRuntime.HandleCharlieAlertReconcile,
		TypeCharlieAlertDispatch:                   charlieRuntime.HandleCharlieAlertDispatch,
		TypeEmailDispatch:                          dispatch.HandleEmailDispatch,
		TypeEmailCleanupOld:                        dispatch.HandleEmailCleanupOld,
		TypeWebhookDispatch:                        dispatch.HandleWebhookDispatch,
		TypeWebhookCleanupOld:                      dispatch.HandleWebhookCleanupOld,
		TypeSIEMDispatch:                           dispatch.HandleSIEMDispatch,
		TypeSIEMCleanupOld:                         dispatch.HandleSIEMCleanupOld,
		TypeTaskOutboxDispatch:                     dispatch.HandleTaskOutboxDispatch,
		TypeAuditOutboxDispatch:                    dispatch.HandleAuditOutboxDispatch,
		TypeAdminQueueOperation:                    dispatch.HandleAdminQueueOperation,
		tasks.AgentTokenRotateSweepType:            maintenance.HandleAgentTokenRotateSweep,
		TypePlaintextCredentialMigration:           maintenance.HandlePlaintextCredentialMigration,
		tasks.GitOpsSyncType:                       gitOpsRuntime.HandleGitOpsSync,
		tasks.MeshDetectType:                       meshRuntime.HandleMeshDetect,
		tasks.CRDOwnershipDriftCheckType:           crdOwnershipRuntime.HandleCRDOwnershipDriftCheck,
		TypeApiserverAllowlistReconcile:            allowlists.HandleApiserverAllowlistReconcile,
		TypeApiserverAllowlistReconcileAll:         allowlists.HandleApiserverAllowlistReconcileAll,
		TypeApiserverAllowlistCleanupSnapshots:     allowlists.HandleApiserverAllowlistCleanupSnapshots,
		TypeSecurityIngest:                         securityIngestRuntime.HandleSecurityIngest,
		TypeSecurityIngestRecovery:                 securityIngestRuntime.HandleSecurityIngestRecovery,
		TypeProjectReconcile:                       projectRuntime.HandleProjectReconcile,
		TypeProjectReconcileAll:                    projectRuntime.HandleProjectReconcileAll,
		TypeClusterTemplateApply:                   clusterTemplateRuntime.HandleClusterTemplateApply,
		TypeClusterTemplateDriftCheck:              clusterTemplateRuntime.HandleClusterTemplateDriftCheck,
		TypeClusterApplyRegistrySecret:             clusterRegistryRuntime.HandleClusterApplyRegistrySecret,
		TypeClusterRegistryDriftReconcile:          clusterRegistryRuntime.HandleClusterRegistryDriftReconcile,
		TypeNetworkPolicyApply:                     networkPolicyRuntime.HandleNetworkPolicyApply,
		TypeNetworkPolicyDriftCheck:                networkPolicyRuntime.HandleNetworkPolicyDriftCheck,
		tasks.ClusterSnapshotApplyOperationType:    clusterSnapshotRuntime.HandleClusterSnapshotApplyOperation,
		tasks.ClusterSnapshotPollType:              clusterSnapshotRuntime.HandleClusterSnapshotPoll,
		tasks.ClusterSnapshotDispatchScheduledType: clusterSnapshotRuntime.HandleClusterSnapshotDispatchScheduled,
		tasks.ClusterSnapshotCleanupExpiredType:    clusterSnapshotRuntime.HandleClusterSnapshotCleanupExpired,
		tasks.ControlPlaneSnapshotApplyType:        controlPlaneSnapshotRuntime.HandleControlPlaneSnapshotApply,
		tasks.ControlPlaneSnapshotSweepType:        controlPlaneSnapshotRuntime.HandleControlPlaneSnapshotSweep,
		tasks.KubectlSessionReapType:               kubectlRuntime.HandleKubectlSessionReap,
		tasks.CloudCredentialMaterializeType:       cloudCredentialRuntime.HandleCloudCredentialMaterialize,
		tasks.CloudCredentialDriftReconcileType:    cloudCredentialRuntime.HandleCloudCredentialDriftReconcile,
		tasks.ToolDriftSweepType:                   toolDriftRuntime.HandleToolDriftSweep,
		tasks.CharlieTriggerDispatchType:           charlieTriggerRuntime.HandleCharlieTriggerDispatch,
		tasks.ClusterDecommissionType:              clusterDecommissionRuntime.HandleClusterDecommission,
		tasks.ClusterDecommissionAllType:           clusterDecommissionRuntime.HandleClusterDecommissionAll,
		tasks.DispatchDeferredType:                 deferredRuntime.HandleDispatchDeferred,
	}
	taskTypes := []string{
		TypeHealthCheck,
		TypeCatalogSync,
		TypeMonitoringReconcile,
		TypeNotificationSend,
		TypeTelemetrySend,
		tasks.ClusterConditionReconcileType,
		TypeAlertEvaluation,
		TypeMetricsAggregation,
		TypeRunScheduledBackups,
		TypeEnforceBackupRetention,
		TypeCleanupExpiredRegistrationTokens,
		TypeCleanupOldAlertEvents,
		TypeEnsureAuditLogPartitions,
		TypeEnforceAuditLogRetention,
		tasks.ApiserverAuditRetentionType,
		tasks.ClusterTombstoneRetentionType,
		tasks.AgentUpgradeStuckSweepType,
		TypeCrdMirrorPruneStale,
		TypeCrdMirrorGaugePopulate,
		TypeAnomalyBaselineRecompute,
		TypeXClusterAnomalyRecompute,
		TypeChartRecommendationsRecompute,
		tasks.RefreshGroupSyncMetricsType,
		TypeEmailCleanupOld,
		TypeEmailDispatch,
		TypeWebhookCleanupOld,
		TypeWebhookDispatch,
		TypeSIEMCleanupOld,
		tasks.AgentTokenRotateSweepType,
		TypeApiserverAllowlistCleanupSnapshots,
		TypeTaskOutboxDispatch,
		TypeAuditOutboxDispatch,
		// Keep SIEM after audit-outbox so the real transactional fanout is
		// included in the same receiver batch and replay proof.
		TypeSIEMDispatch,
		tasks.DeliverySourceResolutionType,
		tasks.DeliveryRolloutReconcileType,
		tasks.DeliverySystemRolloutReconcileType,
		TypeCharlieAlertReconcile,
		TypeCharlieAlertDispatch,
		tasks.ManagementBackupReconcileType,
		tasks.ManagementBackupOperationType,
		tasks.GitOpsSyncType,
		TypePlaintextCredentialMigration,
		TypeAdminQueueOperation,
		tasks.PodDeleteType,
		tasks.ResourceOperationType,
		tasks.NodeOperationType,
		tasks.ImageVulnerabilityRescanType,
		tasks.MeshDetectType,
		tasks.CRDOwnershipDriftCheckType,
		tasks.GatekeeperConstraintReconcileType,
		TypeBackupExecution,
		TypeRunRestore,
		TypeSecurityScan,
		TypeAgentManifest,
		TypeApiserverAllowlistReconcile,
		TypeApiserverAllowlistReconcileAll,
		TypeSecurityIngest,
		TypeSecurityIngestRecovery,
		TypeProjectReconcile,
		TypeProjectReconcileAll,
		TypeClusterTemplateApply,
		TypeClusterTemplateDriftCheck,
		TypeClusterApplyRegistrySecret,
		TypeClusterRegistryDriftReconcile,
		TypeNetworkPolicyApply,
		TypeNetworkPolicyDriftCheck,
		tasks.ClusterSnapshotApplyOperationType,
		tasks.ClusterSnapshotPollType,
		tasks.ClusterSnapshotDispatchScheduledType,
		tasks.ClusterSnapshotCleanupExpiredType,
		tasks.ControlPlaneSnapshotApplyType,
		tasks.ControlPlaneSnapshotSweepType,
		tasks.KubectlSessionReapType,
		tasks.ClusterGroupMetricsRefreshType,
		tasks.GatekeeperConstraintReconcileAllType,
		tasks.CloudCredentialMaterializeType,
		tasks.CloudCredentialDriftReconcileType,
		tasks.GatekeeperPolicyApplyType,
		tasks.ToolDriftSweepType,
		tasks.CharlieTriggerDispatchType,
		tasks.ClusterDecommissionType,
		tasks.ClusterDecommissionAllType,
		tasks.DispatchDeferredType,
	}
	if len(taskTypes) != 83 {
		t.Fatalf("production business handler coverage=%d, want 83", len(taskTypes))
	}
	t.Logf("production business handler coverage=%d/%d descriptors", len(taskTypes), len(TaskDescriptors()))

	descriptors := make([]TaskDescriptor, 0, len(taskTypes))
	seen := make(map[string]struct{}, len(taskTypes))
	descriptorByType := make(map[string]TaskDescriptor, len(taskTypes))
	for _, taskType := range taskTypes {
		if _, duplicate := seen[taskType]; duplicate {
			t.Fatalf("production business handler %s is counted twice", taskType)
		}
		seen[taskType] = struct{}{}
		descriptor := integrationDescriptor(t, taskType)
		expectedQueue := "default"
		if descriptor.Owner == TaskOwnerTunnel {
			expectedQueue = TunnelQueueName
		}
		if (descriptor.Owner != TaskOwnerWorker && descriptor.Owner != TaskOwnerTunnel) || descriptor.Queue != expectedQueue {
			t.Fatalf("production business handler %s routes to %s/%s", taskType, descriptor.Owner, descriptor.Queue)
		}
		handler := descriptor.Handler
		if override := overrides[taskType]; override != nil {
			handler = override
		}
		descriptor.Handler = core.BindHandler(handler)
		descriptors = append(descriptors, descriptor)
		descriptorByType[taskType] = descriptor
	}
	fixtures := seedWorkerBusinessFixtures(t, ctx, pool, outbound, deliveryHTTP, gitOpsFixture, encryptor, managementOperationID, managementKubernetes, managementNamespace, tunnelK8s)
	fixtures.helmStatus = helmStatus
	fixtures.decommissionTunnel = decommissionTunnel
	fixtures.deferredReplay = deferredReplayFixture
	deferredReplayFixture.userID = fixtures.kubectlUserID
	deferredReplayFixture.opID = fixtures.deferredOperationID
	deferredReplayFixture.path = "/api/v1/clusters/" + fixtures.clusterID.String() + "/template"
	fixtures.logs = &logs
	descriptors = append(descriptors, TaskDescriptor{
		Type: workerAdminArchivedTargetType, Queue: "default", Owner: TaskOwnerWorker,
		Handler: func(context.Context, *asynq.Task) error {
			return fmt.Errorf("intentional archived target: %w", asynq.SkipRetry)
		},
	})
	consumer := newWorkerIntegrationConsumer(t, redisURL, descriptors, map[string]int{"default": 4, TunnelQueueName: 4})
	consumer.RegisterHandlers()
	if err := consumer.Start(); err != nil {
		t.Fatal(err)
	}
	defer consumer.Shutdown()
	archiveWorkerAdminTarget(t, ctx, client, inspector, fixtures.adminArchivedTaskID)

	for pass := 1; pass <= 2; pass++ {
		for _, taskType := range taskTypes {
			taskID := uuid.NewString()
			task := workerBusinessTask(t, taskType, fixtures)
			queue := descriptorByType[taskType].Queue
			_, err := client.EnqueueContext(ctx, task,
				asynq.Queue(queue), asynq.MaxRetry(0), asynq.Retention(2*time.Minute), asynq.TaskID(taskID))
			if err != nil {
				t.Fatalf("enqueue production handler %s: %v", taskType, err)
			}
			waitForWorkerBusinessTask(t, ctx, inspector, queue, taskID, taskType, pass)
		}
		assertWorkerBusinessFixtures(t, ctx, pool, inspector, fixtures)
		if pass == 1 {
			// Make the bounded receiver failures eligible for the second
			// production dispatcher tick without sleeping through backoff.
			if _, err := pool.Exec(ctx, `UPDATE webhook_deliveries SET next_attempt_at=now() WHERE id=$1`, fixtures.webhookRetryID); err != nil {
				t.Fatal(err)
			}
		}
	}
	if strings.Contains(logs.String(), "worker-integration-secret-canary") || strings.Contains(logs.String(), "worker-credential-secret-canary") {
		t.Fatal("outbound integration secret canary appeared in worker logs")
	}
}

func workerBusinessTask(t *testing.T, taskType string, fixtures workerBusinessFixtures) *asynq.Task {
	t.Helper()
	switch taskType {
	case TypeCatalogSync:
		task, err := tasks.NewCatalogSyncTask(tasks.CatalogSyncPayload{RepositoryID: fixtures.catalogRepoID.String()})
		if err != nil {
			t.Fatal(err)
		}
		return task
	case TypeMonitoringReconcile:
		task, err := tasks.NewMonitoringReconcileTask(tasks.MonitoringReconcilePayload{ClusterID: fixtures.clusterID.String()})
		if err != nil {
			t.Fatal(err)
		}
		return task
	case TypeNotificationSend:
		task, err := tasks.NewNotificationSendTask(tasks.NotificationSendPayload{
			Channel: tasks.ChannelTypeWebhook, Subject: "worker integration", Body: "bounded receiver proof",
			Recipients: []string{fixtures.outbound.server.URL + "/notification"},
			RuleID:     fixtures.clusterID.String(), FiredAt: "2026-01-01T00:00:00Z",
		})
		if err != nil {
			t.Fatal(err)
		}
		return task
	case tasks.DeliverySourceResolutionType:
		task, err := tasks.NewDeliverySourceResolutionTask(fixtures.deliveryResolutionID)
		if err != nil {
			t.Fatal(err)
		}
		return task
	case tasks.DeliveryRolloutReconcileType:
		task, err := tasks.NewDeliveryRolloutTask(fixtures.deliveryRolloutID)
		if err != nil {
			t.Fatal(err)
		}
		return task
	case tasks.ManagementBackupReconcileType:
		task, err := tasks.NewManagementBackupReconcileTask(fixtures.managementBackupID, 1)
		if err != nil {
			t.Fatal(err)
		}
		return task
	case tasks.ManagementBackupOperationType:
		task, err := tasks.NewManagementBackupOperationTask(fixtures.managementOperationID)
		if err != nil {
			t.Fatal(err)
		}
		return task
	case tasks.GitOpsSyncType:
		task, err := tasks.NewGitOpsSourceSyncTask(fixtures.gitOpsSourceID)
		if err != nil {
			t.Fatal(err)
		}
		return task
	case TypeAdminQueueOperation:
		task, err := tasks.NewAdminQueueOperationTask(fixtures.adminQueueOperationID)
		if err != nil {
			t.Fatal(err)
		}
		return task
	case tasks.PodDeleteType:
		task, err := tasks.NewPodDeleteTask(fixtures.podDeleteOperationID)
		if err != nil {
			t.Fatal(err)
		}
		return task
	case tasks.ResourceOperationType:
		task, err := tasks.NewResourceOperationTask(fixtures.resourceOperationID, 1)
		if err != nil {
			t.Fatal(err)
		}
		return task
	case tasks.NodeOperationType:
		task, err := tasks.NewNodeOperationTask(fixtures.nodeOperationID, 1)
		if err != nil {
			t.Fatal(err)
		}
		return task
	case tasks.ImageVulnerabilityRescanType:
		task, err := tasks.NewImageVulnerabilityRescanTask(fixtures.vulnerabilityOperationID)
		if err != nil {
			t.Fatal(err)
		}
		return task
	case tasks.GatekeeperConstraintReconcileType:
		task, err := tasks.NewGatekeeperConstraintReconcileTask(fixtures.clusterID, fixtures.gatekeeperConstraintName, 1)
		if err != nil {
			t.Fatal(err)
		}
		return task
	case TypeBackupExecution:
		task, err := tasks.NewBackupExecutionTask(tasks.BackupExecutionPayload{
			ClusterID: fixtures.clusterID.String(), BackupID: fixtures.backupExecutionID.String(),
		})
		if err != nil {
			t.Fatal(err)
		}
		return task
	case TypeRunRestore:
		task, err := tasks.NewRunRestoreTask(tasks.RunRestorePayload{RestoreID: fixtures.restoreOperationID.String()})
		if err != nil {
			t.Fatal(err)
		}
		return task
	case TypeSecurityScan:
		return asynq.NewTask(taskType, mustWorkerJSON(t, tasks.SecurityScanPayload{
			ClusterID: fixtures.clusterID.String(), ScanType: "cis",
		}))
	case TypeAgentManifest:
		return asynq.NewTask(taskType, mustWorkerJSON(t, tasks.AgentManifestPayload{
			ClusterID: fixtures.clusterID.String(),
		}))
	case TypeApiserverAllowlistReconcile:
		task, err := tasks.NewApiserverAllowlistReconcileTask(fixtures.clusterID)
		if err != nil {
			t.Fatal(err)
		}
		return task
	case TypeSecurityIngest:
		task, err := tasks.NewSecurityIngestTask(tasks.SecurityScanIngestPayload{
			ScanID: fixtures.securityIngestScanID.String(), Generation: 1,
		})
		if err != nil {
			t.Fatal(err)
		}
		return task
	case TypeProjectReconcile:
		task, err := tasks.NewProjectReconcileTask(tasks.ProjectReconcilePayload{
			ProjectID: fixtures.projectReconcileID.String(), ClusterID: fixtures.clusterID.String(),
			Namespace: fixtures.projectNamespace, Op: "apply",
		})
		if err != nil {
			t.Fatal(err)
		}
		return task
	case TypeProjectReconcileAll:
		task, err := tasks.NewProjectReconcileAllTask()
		if err != nil {
			t.Fatal(err)
		}
		return task
	case TypeClusterTemplateApply:
		task, err := tasks.NewClusterTemplateApplyTask(fixtures.clusterID)
		if err != nil {
			t.Fatal(err)
		}
		return task
	case TypeClusterTemplateDriftCheck:
		task, err := tasks.NewClusterTemplateDriftCheckTask()
		if err != nil {
			t.Fatal(err)
		}
		return task
	case TypeClusterApplyRegistrySecret:
		task, err := tasks.NewClusterApplyRegistrySecretTask(tasks.ClusterApplyRegistrySecretPayload{
			RegistryID: fixtures.registryCredentialID.String(), ClusterID: fixtures.clusterID.String(), Op: "apply",
		})
		if err != nil {
			t.Fatal(err)
		}
		return task
	case TypeClusterRegistryDriftReconcile:
		task, err := tasks.NewClusterRegistryDriftReconcileTask()
		if err != nil {
			t.Fatal(err)
		}
		return task
	case TypeNetworkPolicyApply:
		task, err := tasks.NewNetworkPolicyApplyTask(uuid.Nil)
		if err != nil {
			t.Fatal(err)
		}
		return task
	case TypeNetworkPolicyDriftCheck:
		task, err := tasks.NewNetworkPolicyDriftCheckTask()
		if err != nil {
			t.Fatal(err)
		}
		return task
	case tasks.ClusterSnapshotApplyOperationType:
		task, err := tasks.NewClusterSnapshotOperationTask(tasks.ClusterSnapshotOperationPayload{
			Operation: tasks.ClusterSnapshotOperationCreate, SnapshotID: fixtures.snapshotApplyID.String(),
		})
		if err != nil {
			t.Fatal(err)
		}
		return task
	case tasks.ControlPlaneSnapshotApplyType:
		task, err := tasks.NewControlPlaneSnapshotApplyTask(fixtures.controlPlaneApplyID)
		if err != nil {
			t.Fatal(err)
		}
		return task
	case tasks.ClusterSnapshotPollType:
		task, err := tasks.NewClusterSnapshotPollTask()
		if err != nil {
			t.Fatal(err)
		}
		return task
	case tasks.ClusterSnapshotDispatchScheduledType:
		task, err := tasks.NewClusterSnapshotDispatchScheduledTask()
		if err != nil {
			t.Fatal(err)
		}
		return task
	case tasks.ClusterSnapshotCleanupExpiredType:
		task, err := tasks.NewClusterSnapshotCleanupExpiredTask()
		if err != nil {
			t.Fatal(err)
		}
		return task
	case tasks.ControlPlaneSnapshotSweepType:
		return tasks.NewControlPlaneSnapshotSweepTask()
	case tasks.KubectlSessionReapType:
		return tasks.NewKubectlSessionReapTask()
	case tasks.ClusterGroupMetricsRefreshType:
		return tasks.NewClusterGroupMetricsRefreshTask()
	case tasks.GatekeeperConstraintReconcileAllType:
		return tasks.NewGatekeeperConstraintReconcileAllTask()
	case tasks.CloudCredentialMaterializeType:
		task, err := tasks.NewCloudCredentialMaterializeTask(tasks.CloudCredentialMaterializePayload{
			CredentialID: fixtures.cloudCredentialID.String(), ClusterID: fixtures.clusterID.String(),
			Namespace: "worker-cloud-direct", SecretName: "worker-cloud-credential",
		})
		if err != nil {
			t.Fatal(err)
		}
		return task
	case tasks.CloudCredentialDriftReconcileType:
		task, err := tasks.NewCloudCredentialDriftReconcileTask()
		if err != nil {
			t.Fatal(err)
		}
		return task
	case tasks.GatekeeperPolicyApplyType:
		return tasks.NewGatekeeperPolicyApplyTask()
	case tasks.ToolDriftSweepType:
		return asynq.NewTask(taskType, nil)
	case tasks.CharlieTriggerDispatchType:
		return asynq.NewTask(taskType, mustWorkerJSON(t, map[string]string{"event_id": fixtures.charlieTriggerEventID.String()}))
	case tasks.ClusterDecommissionType:
		task, err := tasks.NewClusterDecommissionTask(fixtures.decommissionDirectID)
		if err != nil {
			t.Fatal(err)
		}
		return task
	case tasks.ClusterDecommissionAllType:
		task, err := tasks.NewClusterDecommissionAllTask()
		if err != nil {
			t.Fatal(err)
		}
		return task
	case tasks.DispatchDeferredType:
		return asynq.NewTask(taskType, nil)
	case TypeCharlieAlertDispatch:
		payload, err := json.Marshal(map[string]string{"delivery_id": fixtures.charlieDeliveryID.String()})
		if err != nil {
			t.Fatal(err)
		}
		return asynq.NewTask(taskType, payload)
	default:
		return asynq.NewTask(taskType, nil)
	}
}

func seedWorkerBusinessFixtures(t *testing.T, ctx context.Context, pool *pgxpool.Pool, outbound *workerOutboundFixture, deliveryHTTP *workerDeliveryHTTPFixture, gitOps workerGitOpsFixture, encryptor *auth.Encryptor, managementOperationID uuid.UUID, managementKubernetes *fake.Clientset, managementNamespace string, tunnelK8s *workerTunnelK8sFixture) workerBusinessFixtures {
	t.Helper()
	fixtures := workerBusinessFixtures{
		clusterID:                 uuid.New(),
		upgradeID:                 uuid.New(),
		taskOutboxID:              uuid.New(),
		auditOutboxID:             uuid.New(),
		catalogRepoID:             uuid.New(),
		emailID:                   uuid.New(),
		webhookOKID:               uuid.New(),
		webhookRetryID:            uuid.New(),
		siemOKID:                  uuid.New(),
		siemRetryID:               uuid.New(),
		deliveryResolutionID:      uuid.New(),
		deliveryRolloutID:         uuid.New(),
		deliverySystemRolloutID:   uuid.New(),
		charlieDeliveryID:         uuid.New(),
		charliePlannedFindingID:   uuid.New(),
		managementBackupID:        uuid.New(),
		managementOperationID:     managementOperationID,
		gitOpsSourceID:            uuid.New(),
		gitOpsClusterName:         gitOps.clusterName,
		adminQueueOperationID:     uuid.New(),
		adminArchivedTaskID:       uuid.NewString(),
		backupCredentialID:        uuid.New(),
		registryCredentialID:      uuid.New(),
		helmCredentialID:          uuid.New(),
		monitoringCredentialID:    uuid.New(),
		podDeleteOperationID:      uuid.New(),
		resourceOperationID:       uuid.New(),
		nodeOperationID:           uuid.New(),
		vulnerabilityOperationID:  uuid.New(),
		gatekeeperConstraintName:  "worker-required-label",
		backupExecutionID:         uuid.New(),
		restoreSourceBackupID:     uuid.New(),
		restoreOperationID:        uuid.New(),
		securityIngestScanID:      uuid.New(),
		projectReconcileID:        uuid.New(),
		projectNamespace:          "worker-team",
		clusterTemplateID:         uuid.New(),
		networkPolicyTemplateID:   uuid.New(),
		networkPolicyAppID:        uuid.New(),
		snapshotApplyID:           uuid.New(),
		snapshotPollID:            uuid.New(),
		snapshotExpiredID:         uuid.New(),
		snapshotScheduleID:        uuid.New(),
		controlPlaneApplyID:       uuid.New(),
		controlPlaneSweepCluster:  uuid.New(),
		kubectlSessionID:          uuid.New(),
		kubectlUserID:             uuid.New(),
		clusterGroupID:            uuid.New(),
		gatekeeperRecoveryName:    "worker-required-owner",
		cloudCredentialID:         uuid.New(),
		cloudDirectMaterialID:     uuid.New(),
		cloudDriftMaterialID:      uuid.New(),
		toolDriftChartID:          uuid.New(),
		charlieTriggerEventID:     uuid.New(),
		decommissionDirectID:      uuid.New(),
		decommissionSweepID:       uuid.New(),
		decommissionDirectCluster: uuid.New(),
		decommissionSweepCluster:  uuid.New(),
		deferredOperationID:       uuid.New(),
		deferredWindowID:          uuid.New(),
		durableTimestamps:         make(map[string]time.Time),
		credentialCiphertexts:     make(map[string]string),
		credentialEncryptor:       encryptor,
		managementKubernetes:      managementKubernetes,
		managementNamespace:       managementNamespace,
		tunnelK8s:                 tunnelK8s,
		downstreamQueue:           "worker-business-downstream",
		downstreamBody:            []byte(`{"source":"worker-business-integration"}`),
		outbound:                  outbound,
		deliveryHTTP:              deliveryHTTP,
	}
	tunnelK8s.mu.Lock()
	tunnelK8s.gatekeeperPolicyClusterID = fixtures.clusterID.String()
	tunnelK8s.mu.Unlock()
	smtpHost, smtpPort := outbound.smtp.address()
	webhookOKSub := uuid.New()
	webhookRetrySub := uuid.New()
	siemOKForwarder := uuid.New()
	siemRetryForwarder := uuid.New()
	monitoringBackend := uuid.New()
	projectID := uuid.New()
	deliverySourceID := uuid.New()
	deliveryBundleID := uuid.New()
	deliveryBundleVersionID := uuid.New()
	deliveryTargetID := uuid.New()
	deliverySystemReleaseID := uuid.New()
	charlieConnectionID := uuid.New()
	charlieTriggerRuleID := uuid.New()
	charlieDirectFindingID := uuid.New()
	charlieChannelID := uuid.New()
	managementCredentials, err := encryptor.Encrypt(`{"access_key":"worker-access","secret_key":"worker-credential-secret-canary"}`)
	if err != nil {
		t.Fatal(err)
	}
	nodeParametersEncrypted, err := encryptor.Encrypt(`{}`)
	if err != nil {
		t.Fatal(err)
	}
	cloudCredentialEncrypted, err := encryptor.Encrypt(`{"access_key_id":"worker-cloud-access","secret_access_key":"worker-cloud-secret"}`)
	if err != nil {
		t.Fatal(err)
	}
	deferredSpec, err := json.Marshal(handler.DeferredOpSpec{
		Method: http.MethodPost, Path: "/api/v1/clusters/" + fixtures.clusterID.String() + "/template",
		Body: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	deferredCiphertext, err := encryptor.Encrypt(string(deferredSpec))
	if err != nil {
		t.Fatal(err)
	}
	deferredEnvelope := mustWorkerJSON(t, handler.EncryptedDeferredOpSpec{SchemaVersion: 1, Ciphertext: deferredCiphertext})
	podDeletePayload := mustWorkerJSON(t, map[string]string{
		"clusterId": fixtures.clusterID.String(), "kind": "Pod", "namespace": "default", "name": "worker-pod",
	})
	constraintYAML := `apiVersion: constraints.gatekeeper.sh/v1beta1
kind: K8sRequiredLabels
metadata:
  name: worker-required-label
spec:
  enforcementAction: dryrun
`
	recoveryConstraintYAML := `apiVersion: constraints.gatekeeper.sh/v1beta1
kind: K8sRequiredLabels
metadata:
  name: worker-required-owner
spec:
  enforcementAction: dryrun
`
	clusterTemplateSpec := mustWorkerJSON(t, map[string]any{
		"environment": "production",
		"labels":      map[string]string{"worker-template": "applied"},
		"default_project": map[string]any{
			"name": "worker-template-default", "pod_security_profile": "baseline",
			"network_policy_mode": "isolated",
		},
		"registration_policy": map[string]any{"token_rotation_days": 30},
	})
	networkPolicySpec := `apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: {{.PolicyName}}
  namespace: {{.Namespace}}
  labels:
    app.kubernetes.io/managed-by: astronomer
    astronomer.io/template: worker-isolation
spec:
  podSelector: {}
  policyTypes: [Ingress]
`
	digestA := "sha256:" + strings.Repeat("a", 64)
	digestB := "sha256:" + strings.Repeat("b", 64)
	chartSum := sha256.Sum256(deliveryHTTP.chart)
	chartDigest := fmt.Sprintf("sha256:%x", chartSum)
	deliveryTrust := model.TrustPolicy{AllowUnsigned: true}
	deliverySourceSpec, err := json.Marshal(model.ResolvedSourceSpec{
		SourceID: deliverySourceID, Type: model.SourceHelmHTTP, URL: deliveryHTTP.url + "/delivery",
		AuthMode: model.AuthNone, Trust: deliveryTrust,
		Revision: model.ImmutableRevision{Kind: model.RevisionHelmChart, Value: "1.0.0", ArtifactDigest: model.Digest(chartDigest)},
	})
	if err != nil {
		t.Fatal(err)
	}
	rendererSpec, err := json.Marshal(model.RendererSpec{Kind: model.RendererHelm, Helm: &model.HelmSpec{
		Chart: "worker-delivery-chart", ChartVersion: "1.0.0", ReleaseName: "worker-delivery", TargetNamespace: "worker-delivery",
	}})
	if err != nil {
		t.Fatal(err)
	}
	reconciliationPolicy, err := json.Marshal(model.ReconciliationPolicy{
		Interval: model.Duration(5 * time.Minute), RetryInterval: model.Duration(time.Minute),
		Timeout: model.Duration(10 * time.Minute), Prune: true, Wait: true, Drift: model.DriftDetect,
	})
	if err != nil {
		t.Fatal(err)
	}
	systemStrategy := model.RolloutStrategy{
		Type: model.StrategyRolling, MaxConcurrent: 1,
		MaxUnavailable:   model.Amount{Type: model.AmountCount, Value: 1},
		ProgressDeadline: model.Duration(30 * time.Minute),
		FailureThreshold: model.Amount{Type: model.AmountCount, Value: 1}, OnFailure: model.FailurePause,
	}
	systemStrategyJSON, err := json.Marshal(systemStrategy)
	if err != nil {
		t.Fatal(err)
	}
	systemStrategyDigest, err := systemStrategy.CanonicalDigest()
	if err != nil {
		t.Fatal(err)
	}
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO cluster_groups (id,name,slug,description) VALUES ($1,'Worker Group','worker-group','integration')`, []any{fixtures.clusterGroupID}},
		{`INSERT INTO users (id,email,username) VALUES ($1,$2,$3)`, []any{fixtures.kubectlUserID, "worker-kubectl-" + fixtures.kubectlUserID.String() + "@example.test", "worker-kubectl-" + fixtures.kubectlUserID.String()}},
		{`INSERT INTO maintenance_windows (id,name,mode,cron_open,duration_minutes,timezone,on_block,enabled) VALUES ($1,'Worker Deferred','blackout','0 0 * * *',60,'UTC','defer',true)`, []any{fixtures.deferredWindowID}},
		{`INSERT INTO clusters (id,name,display_name,status,provider,last_heartbeat) VALUES ($1,$2,$2,'active','self_managed',now())`, []any{fixtures.clusterID, "worker-business-" + fixtures.clusterID.String()}},
		{`INSERT INTO deferred_operations (id,window_id,operation_type,operation_spec,target_cluster_id,status,deferred_until,expires_at,requested_by) VALUES ($1,$2,$3,$4,$5,'pending',now()-interval '1 minute',now()+interval '1 hour',$6)`, []any{fixtures.deferredOperationID, fixtures.deferredWindowID, maintenance.OpClusterTemplateApply, deferredEnvelope, fixtures.clusterID, fixtures.kubectlUserID}},
		{`INSERT INTO clusters (id,name,display_name,status,provider,distribution,last_heartbeat) VALUES ($1,$2,$2,'active','self_managed','k3s',now())`, []any{fixtures.controlPlaneSweepCluster, "worker-cp-sweep-" + fixtures.controlPlaneSweepCluster.String()}},
		{`INSERT INTO clusters (id,name,display_name,status,provider,last_heartbeat) VALUES ($1,$2,$2,'active','self_managed',now()),($3,$4,$4,'active','self_managed',now())`, []any{fixtures.decommissionDirectCluster, "worker-decommission-direct-" + fixtures.decommissionDirectCluster.String(), fixtures.decommissionSweepCluster, "worker-decommission-sweep-" + fixtures.decommissionSweepCluster.String()}},
		{`INSERT INTO cluster_decommissions (id,cluster_id,status,cluster_name,force) VALUES ($1,$2,'pending',$3,true),($4,$5,'pending',$6,true)`, []any{fixtures.decommissionDirectID, fixtures.decommissionDirectCluster, "worker-decommission-direct", fixtures.decommissionSweepID, fixtures.decommissionSweepCluster, "worker-decommission-sweep"}},
		{`INSERT INTO cluster_agent_tokens (cluster_id,token) VALUES ($1,$2),($3,$4)`, []any{fixtures.decommissionDirectCluster, "worker-decommission-token-direct", fixtures.decommissionSweepCluster, "worker-decommission-token-sweep"}},
		{`INSERT INTO agent_connections (cluster_id,agent_id,session_id,status,last_ping) VALUES ($1,$2,$3,'connected',now())`, []any{fixtures.clusterID, "worker-agent-" + fixtures.clusterID.String(), "worker-session-" + fixtures.clusterID.String()}},
		{`UPDATE clusters SET managed_by='crd',external_ref_api_version='astronomer.alphabravo.io/v1alpha1',external_ref_kind='Cluster',external_ref_namespace='worker-system',external_ref_name='missing-worker-cluster',distribution='k3s',group_id=$2 WHERE id=$1`, []any{fixtures.clusterID, fixtures.clusterGroupID}},
		{`INSERT INTO workload_operations (id,target_type,target_key,operation_type,payload,status) VALUES ($1,'pod','worker-pod','delete_pod',$2,'pending'),($3,'cluster',$4,'vulnerability_rescan','{}','pending')`, []any{fixtures.podDeleteOperationID, podDeletePayload, fixtures.vulnerabilityOperationID, fixtures.clusterID.String()}},
		{`INSERT INTO resource_operations (id,idempotency_scope,idempotency_key,request_digest,cluster_id,resource_type,namespace,resource_name,action,required_verb,api_path) VALUES ($1,'worker-integration',$2,$3,$4,'configmap','default','worker-resource','delete','delete','/api/v1/namespaces/default/configmaps/worker-resource')`, []any{fixtures.resourceOperationID, fixtures.resourceOperationID.String(), strings.Repeat("c", 64), fixtures.clusterID}},
		{`INSERT INTO node_operations (id,idempotency_scope,idempotency_key,request_digest,cluster_id,node_name,action,parameters_encrypted) VALUES ($1,'worker-integration',$2,$3,$4,'worker-node','cordon',$5)`, []any{fixtures.nodeOperationID, fixtures.nodeOperationID.String(), strings.Repeat("d", 64), fixtures.clusterID, nodeParametersEncrypted}},
		{`INSERT INTO authored_constraints (cluster_id,name,kind,api_version,yaml,desired_state,sync_status,generation,observed_generation) VALUES ($1,$2,'K8sRequiredLabels','constraints.gatekeeper.sh/v1beta1',$3,'present','pending',1,0)`, []any{fixtures.clusterID, fixtures.gatekeeperConstraintName, constraintYAML}},
		{`INSERT INTO authored_constraints (cluster_id,name,kind,api_version,yaml,desired_state,sync_status,generation,observed_generation) VALUES ($1,$2,'K8sRequiredLabels','constraints.gatekeeper.sh/v1beta1',$3,'present','pending',1,0)`, []any{fixtures.clusterID, fixtures.gatekeeperRecoveryName, recoveryConstraintYAML}},
		{`INSERT INTO cluster_snapshots (id,cluster_id,velero_name,velero_namespace,source,spec,phase) VALUES ($1,$3,'worker-snapshot-apply','velero','manual','{}','New'),($2,$3,'worker-snapshot-poll','velero','manual','{}','InProgress')`, []any{fixtures.snapshotApplyID, fixtures.snapshotPollID, fixtures.clusterID}},
		{`INSERT INTO cluster_snapshots (id,cluster_id,velero_name,velero_namespace,source,spec,phase,expires_at) VALUES ($1,$2,'worker-snapshot-expired','velero','manual','{}','Completed',now()-interval '1 hour')`, []any{fixtures.snapshotExpiredID, fixtures.clusterID}},
		{`INSERT INTO cluster_snapshot_schedules (id,cluster_id,name,cron_schedule,spec,enabled,created_at) VALUES ($1,$2,'worker-scheduled','* * * * *','{}',true,now()-interval '2 hours')`, []any{fixtures.snapshotScheduleID, fixtures.clusterID}},
		{`INSERT INTO control_plane_snapshots (id,cluster_id,name,status,location) VALUES ($1,$2,'worker-control-plane-apply','pending','local')`, []any{fixtures.controlPlaneApplyID, fixtures.clusterID}},
		{`INSERT INTO platform_settings (key,value,description) VALUES ('feature.control_plane_snapshots','true','integration') ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value`, nil},
		{`INSERT INTO kubectl_sessions (id,user_id,cluster_id,sa_namespace,sa_name,pod_namespace,pod_name,status,last_input_at,expires_at) VALUES ($1,$2,$3,'kube-system','astro-shell-worker','kube-system','astro-shell-worker','active',now()-interval '1 hour',now()+interval '1 hour')`, []any{fixtures.kubectlSessionID, fixtures.kubectlUserID, fixtures.clusterID}},
		{`INSERT INTO backup_storage_configs (id,name,storage_type,bucket,prefix,region,access_key,secret_key,is_default) VALUES ($1,$2,'s3','worker-bucket','worker-prefix','us-east-1','worker-access','worker-credential-secret-canary',false)`, []any{fixtures.backupCredentialID, "worker-credential-" + fixtures.backupCredentialID.String()}},
		{`INSERT INTO backups (id,name,storage_id,backup_type,status,cluster_id,velero_backup_name) VALUES ($1,'worker-execution',$3,'full','pending',$4,'worker-execution'),($2,'worker-restore-source',$3,'full','completed',$4,'worker-restore-source')`, []any{fixtures.backupExecutionID, fixtures.restoreSourceBackupID, fixtures.backupCredentialID, fixtures.clusterID}},
		{`INSERT INTO restore_operations (id,backup_id,status,cluster_id,velero_restore_name) VALUES ($1,$2,'pending',$3,'worker-restore')`, []any{fixtures.restoreOperationID, fixtures.restoreSourceBackupID, fixtures.clusterID}},
		{`INSERT INTO apiserver_allowlists (cluster_id,cidrs,mode) VALUES ($1,'["10.44.0.0/16"]','monitor')`, []any{fixtures.clusterID}},
		{`INSERT INTO security_scan_results (id,cluster_id,scan_type,status,cluster_scan_name,poll_generation,poll_deadline) VALUES ($1,$2,'cis','running','worker-cis-scan',1,now()+interval '10 minutes')`, []any{fixtures.securityIngestScanID, fixtures.clusterID}},
		{`INSERT INTO cluster_registry_configs (id,cluster_id,private_registry_url,registry_username,registry_password,namespaces,inject_default_sa,secret_name) VALUES ($1,$2,'registry.example.test','worker-user','worker-credential-secret-canary',$3,false,'worker-cluster-registry')`, []any{fixtures.registryCredentialID, fixtures.clusterID, mustWorkerJSON(t, []string{fixtures.projectNamespace})}},
		{`INSERT INTO cluster_registration_tokens (cluster_id,token,token_hash,expires_at) VALUES ($1,$2,$3,now()-interval '1 hour'),($1,$4,$5,now()+interval '1 hour')`, []any{fixtures.clusterID, "expired-" + fixtures.clusterID.String(), "expired-hash-" + fixtures.clusterID.String(), "fresh-" + fixtures.clusterID.String(), "fresh-hash-" + fixtures.clusterID.String()}},
		{`INSERT INTO apiserver_audit_events (cluster_id,audit_id,stage,verb,username,resource,namespace,status_code,event_time,raw) VALUES ($1,'old','ResponseComplete','get','integration','pods','default',200,now()-interval '31 days','{}'),($1,'fresh','ResponseComplete','get','integration','pods','default',200,now(),'{}')`, []any{fixtures.clusterID}},
		{`INSERT INTO mirrored_ingress_classes (cluster_id,name,last_seen_at) VALUES ($1,'stale',now()-interval '2 hours'),($1,'fresh',now())`, []any{fixtures.clusterID}},
		{`INSERT INTO agent_lifecycle_operations (id,cluster_id,operation_type,status,target_version,target_image,started_at) VALUES ($1,$2,'agent_upgrade','running','integration','integration',now()-interval '31 minutes')`, []any{fixtures.upgradeID, fixtures.clusterID}},
		{`INSERT INTO task_outbox (id,dedupe_key,task_type,payload,queue_name,max_retry) VALUES ($1,$2,'worker-business:downstream',$3,$4,0)`, []any{fixtures.taskOutboxID, "worker-business:" + fixtures.taskOutboxID.String(), fixtures.downstreamBody, fixtures.downstreamQueue}},
		{`INSERT INTO audit_outbox (id,dedupe_key,action,resource_type,resource_id,resource_name,source,action_class,detail) VALUES ($1,$2,'worker.integration.completed','worker_integration',$3,'worker business integration','worker','system','{"bounded":true}')`, []any{fixtures.auditOutboxID, "worker-business:" + fixtures.auditOutboxID.String(), fixtures.auditOutboxID.String()}},
		{`INSERT INTO projects (id,name,display_name,cluster_id,pod_security_profile,network_policy_mode) VALUES ($1,'worker-reconcile','Worker Reconcile',$2,'baseline','isolated')`, []any{fixtures.projectReconcileID, fixtures.clusterID}},
		{`INSERT INTO project_namespaces (project_id,cluster_id,namespace) VALUES ($1,$2,$3)`, []any{fixtures.projectReconcileID, fixtures.clusterID, fixtures.projectNamespace}},
		{`INSERT INTO cloud_credentials (id,project_id,name,provider,data_encrypted,target_refs) VALUES ($1,$2,'worker-cloud','aws',$3,'[]')`, []any{fixtures.cloudCredentialID, fixtures.projectReconcileID, cloudCredentialEncrypted}},
		{`INSERT INTO cloud_credential_materializations (id,credential_id,cluster_id,namespace,secret_name,status) VALUES ($1,$3,$4,'worker-cloud-direct','worker-cloud-credential','pending'),($2,$3,$4,'worker-cloud-drift','worker-cloud-credential','pending')`, []any{fixtures.cloudDirectMaterialID, fixtures.cloudDriftMaterialID, fixtures.cloudCredentialID, fixtures.clusterID}},
		{`INSERT INTO installed_charts (id,cluster_id,release_name,namespace,status,revision,tool_slug) VALUES ($1,$2,'worker-drift-release','worker-tools','installed',1,'worker-drift')`, []any{fixtures.toolDriftChartID, fixtures.clusterID}},
		{`INSERT INTO cluster_templates (id,name,description,spec) VALUES ($1,'worker-template','integration',$2)`, []any{fixtures.clusterTemplateID, clusterTemplateSpec}},
		{`INSERT INTO cluster_template_applications (cluster_id,template_id,status,spec_snapshot) VALUES ($1,$2,'pending',$3)`, []any{fixtures.clusterID, fixtures.clusterTemplateID, clusterTemplateSpec}},
		{`INSERT INTO network_policy_templates (id,slug,name,description,kind,spec_template,enabled) VALUES ($1,'worker-isolation','Worker Isolation','integration','custom',$2,true)`, []any{fixtures.networkPolicyTemplateID, networkPolicySpec}},
		{`INSERT INTO network_policy_applications (id,template_id,cluster_id,namespace,policy_name,status) VALUES ($1,$2,$3,$4,'astronomer-np-worker-isolation','pending')`, []any{fixtures.networkPolicyAppID, fixtures.networkPolicyTemplateID, fixtures.clusterID, fixtures.projectNamespace}},
		{`INSERT INTO helm_repositories (id,name,url,repo_type,description,is_default,auth_type,auth_config,auth_config_encrypted,enabled) VALUES ($1,$2,$3,'helm','integration',false,'none','{}','',true)`, []any{fixtures.catalogRepoID, "worker-integration-" + fixtures.catalogRepoID.String(), outbound.server.URL + "/catalog"}},
		{`INSERT INTO helm_repositories (id,name,url,repo_type,description,is_default,auth_type,auth_config,auth_config_encrypted,enabled) VALUES ($1,$2,'https://charts.example.test','helm','credential migration',false,'basic','{"username":"worker","password":"worker-credential-secret-canary"}','',false)`, []any{fixtures.helmCredentialID, "worker-credential-" + fixtures.helmCredentialID.String()}},
		{`INSERT INTO monitoring_backends (id,name,backend_type,query_url,alertmanager_url,tenant_id,auth_type,auth_config,auth_config_encrypted,default_step_seconds,timeout_seconds,is_default) VALUES ($1,'default','prometheus',$2,'','','none','{}','',30,2,true) ON CONFLICT (name) DO UPDATE SET query_url=EXCLUDED.query_url,is_default=true`, []any{monitoringBackend, outbound.server.URL}},
		{`INSERT INTO monitoring_backends (id,name,backend_type,query_url,alertmanager_url,tenant_id,auth_type,auth_config,auth_config_encrypted,default_step_seconds,timeout_seconds,is_default) VALUES ($1,$2,'prometheus','https://prometheus.example.test','','','bearer','{"token":"worker-credential-secret-canary"}','',30,2,false)`, []any{fixtures.monitoringCredentialID, "worker-credential-" + fixtures.monitoringCredentialID.String()}},
		{`INSERT INTO cluster_monitoring_configs (cluster_id,backend_id,cluster_label,cluster_label_value,status) VALUES ($1,$2,'cluster_id',$3,'pending')`, []any{fixtures.clusterID, monitoringBackend, fixtures.clusterID.String()}},
		{`INSERT INTO platform_settings (key,value,description) VALUES ('telemetry.enabled','true','integration'),('telemetry.endpoint',to_jsonb($1::text),'integration') ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value`, []any{outbound.server.URL + "/telemetry"}},
		{`INSERT INTO smtp_settings (id,enabled,host,port,from_address,from_name,auth_mechanism,encryption,require_tls,timeout_seconds) VALUES ($1,true,$2,$3,'sender@example.test','Worker Integration','none','none',false,5)`, []any{email.SingletonSettingsID, smtpHost, smtpPort}},
		{`INSERT INTO email_messages (id,to_address,subject,template,body_text,body_html,status) VALUES ($1,'receiver@example.test','worker integration','alert_fired','bounded body','<p>bounded body</p>','queued')`, []any{fixtures.emailID}},
		{`INSERT INTO webhook_subscriptions (id,name,url,secret_encrypted,event_filters,extra_headers,enabled,max_retries,timeout_seconds) VALUES ($1,'worker-ok',$2,'','[]','{"X-Integration-Secret":"worker-integration-secret-canary"}',true,3,2),($3,'worker-recover',$4,'','[]','{}',true,3,2)`, []any{webhookOKSub, outbound.server.URL + "/webhook-ok", webhookRetrySub, outbound.server.URL + "/webhook-recover"}},
		{`INSERT INTO webhook_deliveries (id,subscription_id,event_name,event_id,payload,status,next_attempt_at) VALUES ($1,$2,'worker.integration','event-ok','{"event_name":"worker.integration","event_id":"event-ok","detail":{"bounded":true}}','queued',now()),($3,$4,'worker.integration','event-recover','{"event_name":"worker.integration","event_id":"event-recover","detail":{"bounded":true}}','queued',now())`, []any{fixtures.webhookOKID, webhookOKSub, fixtures.webhookRetryID, webhookRetrySub}},
		{`INSERT INTO siem_forwarders (id,name,transport,endpoint,event_filters,format,batch_size,timeout_seconds,enabled) VALUES ($1,'worker-ok','ndjson_https',$2,'[]','ndjson',10,2,true),($3,'worker-recover','ndjson_https',$4,'[]','ndjson',10,2,true)`, []any{siemOKForwarder, outbound.server.URL + "/siem-ok", siemRetryForwarder, outbound.server.URL + "/siem-recover"}},
		{`INSERT INTO siem_forward_queue (id,forwarder_id,event_name,payload,severity,dedupe_key) VALUES (nextval('siem_forward_queue_id_seq'),$1,'worker.integration','{"event_id":"siem-ok","detail":{"bounded":true}}','info',$2),(nextval('siem_forward_queue_id_seq'),$3,'worker.integration','{"event_id":"siem-recover","detail":{"bounded":true}}','info',$4)`, []any{siemOKForwarder, fixtures.siemOKID.String(), siemRetryForwarder, fixtures.siemRetryID.String()}},
		{`INSERT INTO projects (id,name,display_name,cluster_id) VALUES ($1,$2,$2,$3)`, []any{projectID, "worker-delivery-" + projectID.String(), fixtures.clusterID}},
		{`INSERT INTO delivery_sources (id,project_id,name,source_type,url,auth_mode,trust_policy,status) VALUES ($1,$2,$3,'helm_http',$4,'none',$5,'pending')`, []any{deliverySourceID, projectID, "worker-delivery-source", deliveryHTTP.url + "/delivery", mustWorkerJSON(t, deliveryTrust)}},
		{`INSERT INTO delivery_source_resolutions (id,source_id,requested_revision,chart_name) VALUES ($1,$2,'1.0.0','worker-delivery-chart')`, []any{fixtures.deliveryResolutionID, deliverySourceID}},
		{`INSERT INTO component_bundles (id,project_id,name) VALUES ($1,$2,'worker-delivery-bundle')`, []any{deliveryBundleID, projectID}},
		{`INSERT INTO component_bundle_versions (id,bundle_id,source_id,version,renderer,scope,requested_revision,resolved_revision,artifact_digest,source_spec,renderer_spec,reconciliation_policy,requirements,dependency_bundle_ids,spec_digest,verification_status,state) VALUES ($1,$2,$3,'1.0.0','helm','namespace','1.0.0','1.0.0',$4,$5,$6,$7,'[]','[]',$8,'unsigned','ready')`, []any{deliveryBundleVersionID, deliveryBundleID, deliverySourceID, chartDigest, deliverySourceSpec, rendererSpec, reconciliationPolicy, digestA}},
		{`INSERT INTO delivery_targets (id,project_id,name,bundle_version_id,placement,rollout_policy,reconciliation_policy,maintenance_window_policy) VALUES ($1,$2,'worker-delivery-target',$3,'{"all_clusters":true}','{}','{}','{}')`, []any{deliveryTargetID, projectID, deliveryBundleVersionID}},
		{`INSERT INTO delivery_system_releases (id,version,artifact_url,artifact_digest,distribution_digest,agent_version,agent_image,minimum_kubernetes,maximum_kubernetes,crd_storage_version,verification_policy,spec_digest,state) VALUES ($1,'1.0.0','https://release.example.test/manifest',$2,$3,'1.0.0',$4,'1.27.0','1.35.0','v1','{}',$5,'draft')`, []any{deliverySystemReleaseID, digestA, digestB, "registry.example.test/agent@" + digestB, digestA}},
		{`INSERT INTO delivery_system_rollouts (id,release_id,strategy,strategy_digest,state,total_clusters,idempotency_key,progress_deadline) VALUES ($1,$2,$3,$4,'queued',1,$5,now()+interval '30 minutes')`, []any{fixtures.deliverySystemRolloutID, deliverySystemReleaseID, systemStrategyJSON, systemStrategyDigest.String(), "worker-system-" + fixtures.deliverySystemRolloutID.String()}},
		{`INSERT INTO delivery_system_cluster_assignments (cluster_id,desired_release_id,rollout_id,generation,cohort,release_order,phase) VALUES ($1,$2,$3,1,0,0,'pending')`, []any{fixtures.clusterID, deliverySystemReleaseID, fixtures.deliverySystemRolloutID}},
		{`INSERT INTO charlie_connections (id,installation_id,product_id,product_slug,deployment_id,route_id,central_url,central_ca_fingerprint,signing_key_id,signing_key_fingerprint,onboarding_schema_version,central_api_version,agent_protocol_version,chart_version,chart_digest,image_digest,logical_agent_id,bridge_service_name,mcp_service_name,agent_secret_name,onboarding_package_id,onboarding_package_digest,onboarding_package_expires_at,enrollment_credentials_expires_at,artifact_credential_expires_at,certificate_expires_at,onboarding_state,requested_mode,verified_mode,health_state,active,chart_reference,image_reference) VALUES ($1,$2,'worker-product','astronomer','worker-deployment','worker-route','https://charlie.example.test','worker-ca','worker-key','worker-fingerprint','v1','v1','v1','1.0.0',$3,$4,'worker-logical','worker-bridge','worker-mcp','worker-secret','worker-package',$3,now()+interval '1 day',now()+interval '1 day',now()+interval '1 day',now()+interval '1 day','active','read_only','read_only','ready',true,'oci://registry.example.test/charlie',$5)`, []any{charlieConnectionID, uuid.New(), digestA, digestB, "registry.example.test/charlie@" + digestB}},
		{`INSERT INTO charlie_trigger_rules (id,connection_id,name,rule_type,category,enabled,minimum_severity,selectors,thresholds,window_seconds,cooldown_seconds,service_identity_id,mode_ceiling) VALUES ($1,$2,'worker-inactive','event','integration',false,'warning','{}','{}',60,0,$3,'read_only')`, []any{charlieTriggerRuleID, charlieConnectionID, fixtures.kubectlUserID}},
		{`INSERT INTO charlie_trigger_events (id,rule_id,source,event_type,resource_type,resource_id,fingerprint,state,first_occurred_at,last_occurred_at) VALUES ($1,$2,'worker','worker.inactive','cluster',$3,$4,'pending',now(),now())`, []any{fixtures.charlieTriggerEventID, charlieTriggerRuleID, fixtures.clusterID.String(), "worker-trigger-" + fixtures.charlieTriggerEventID.String()}},
		{`INSERT INTO notification_channels (id,name,channel_type,configuration,enabled) VALUES ($1,'worker-charlie','webhook',$2,true)`, []any{charlieChannelID, mustWorkerJSON(t, map[string]string{"url": outbound.server.URL + "/charlie-recover"})}},
		{`INSERT INTO charlie_alert_policies (connection_id,enabled,minimum_severity,dedupe_window_seconds,escalation_after_seconds) VALUES ($1,true,'medium',300,0)`, []any{charlieConnectionID}},
		{`INSERT INTO charlie_alert_policy_channels (connection_id,notification_channel_id) VALUES ($1,$2)`, []any{charlieConnectionID, charlieChannelID}},
		{`INSERT INTO charlie_findings (id,connection_id,charlie_finding_id,source,severity,status,effective_mode,dedupe_fingerprint,title,summary) VALUES ($1,$3,'worker-direct','system','high','open','read_only',$4,'worker direct','bounded'),($2,$3,'worker-planned','system','high','open','read_only',$5,'worker planned','bounded')`, []any{charlieDirectFindingID, fixtures.charliePlannedFindingID, charlieConnectionID, "worker-direct-" + charlieDirectFindingID.String(), "worker-planned-" + fixtures.charliePlannedFindingID.String()}},
		{`INSERT INTO charlie_finding_resources (finding_id,resource_type,resource_id,required_verb) VALUES ($1,'installation','worker-direct','get'),($2,'installation','worker-planned','get')`, []any{charlieDirectFindingID, fixtures.charliePlannedFindingID}},
		{`INSERT INTO charlie_alert_deliveries (id,connection_id,finding_id,notification_channel_id,policy_revision,delivery_kind,dedupe_bucket,severity,next_attempt_at,deep_link,subject,body) VALUES ($1,$2,$3,$4,1,'initial',1,'high',now(),$5,'worker Charlie delivery','bounded receiver proof')`, []any{fixtures.charlieDeliveryID, charlieConnectionID, charlieDirectFindingID, charlieChannelID, "/dashboard/charlie?tab=findings&finding=" + charlieDirectFindingID.String()}},
		{`INSERT INTO management_backup_destinations (id,name,bucket,prefix,region,encrypted_credentials,schedule,enabled,keep_daily,keep_weekly,keep_monthly) VALUES ($1,$2,'worker-management-bucket','worker-management-prefix','us-east-1',$3,'0 3 * * *',true,7,4,2)`, []any{fixtures.managementBackupID, "worker-management-" + fixtures.managementBackupID.String(), managementCredentials}},
		{`INSERT INTO workload_operations (id,target_type,target_key,operation_type,status) VALUES ($1,'management_backup_destination',$2,'management_backup_run','pending')`, []any{fixtures.managementOperationID, fixtures.managementBackupID.String()}},
		{`INSERT INTO gitops_registration_sources (id,name,repo_url,branch,path_prefix,auth_mode,sync_mode,sync_interval_seconds,on_delete,enabled) VALUES ($1,$2,$3,'main','clusters','none','interval',60,'log',true)`, []any{fixtures.gitOpsSourceID, "worker-gitops-" + fixtures.gitOpsSourceID.String(), gitOps.repoURL}},
		{`INSERT INTO admin_queue_operations (id,action,queue_name,task_id,requested_by) VALUES ($1,'discard','default',$2,$3)`, []any{fixtures.adminQueueOperationID, fixtures.adminArchivedTaskID, uuid.New()}},
	}
	for _, statement := range statements {
		if _, err := pool.Exec(ctx, statement.query, statement.args...); err != nil {
			t.Fatalf("seed production business handler fixture: %v", err)
		}
	}
	desired := deliveryrollout.VersionIdentity{
		BundleVersionID: deliveryBundleVersionID, SpecDigest: model.Digest(digestA),
		Source: model.ResolvedSourceSpec{
			SourceID: deliverySourceID, Type: model.SourceHelmHTTP, URL: deliveryHTTP.url + "/delivery",
			AuthMode: model.AuthNone, Trust: deliveryTrust,
			Revision: model.ImmutableRevision{Kind: model.RevisionHelmChart, Value: "1.0.0", ArtifactDigest: model.Digest(chartDigest)},
		},
	}
	placementRequest := placement.Request{
		Placement: model.Placement{AllClusters: true}, AllowedProjectIDs: []uuid.UUID{projectID},
		Candidates: []placement.Candidate{{
			ID: fixtures.clusterID, ProjectID: projectID, Name: "worker-delivery-cluster",
			Connected: true, Compatibility: placement.CompatibilityCompatible,
		}},
		Identity: placement.SnapshotIdentity{
			TargetGeneration: 1, BundleVersionID: deliveryBundleVersionID,
			BundleSpecDigest: model.Digest(digestA), ResolvedRevision: desired.Source.Revision,
		},
	}
	preview, err := placement.Evaluate(placementRequest)
	if err != nil {
		t.Fatal(err)
	}
	seedStore := &workerPlanningSeedStore{snapshot: deliveryrollout.PlanningSnapshot{
		TargetID: deliveryTargetID, ProjectID: projectID, TargetGeneration: 1,
		Desired: desired, PlacementRequest: placementRequest, PreviousByCluster: map[uuid.UUID]deliveryrollout.PreviousDeployment{},
	}}
	planner, err := deliveryrollout.NewPlanner(seedStore, time.Now, func() uuid.UUID { return fixtures.deliveryRolloutID })
	if err != nil {
		t.Fatal(err)
	}
	plan, err := planner.Create(ctx, deliveryrollout.CreateRequest{
		TargetID: deliveryTargetID, ExpectedTargetGeneration: 1, PreviewDigest: preview.PreviewDigest,
		ConfirmAllClusters: true, Strategy: systemStrategy, Actor: "worker-integration",
		IdempotencyKey: "worker-rollout-" + fixtures.deliveryRolloutID.String(),
	})
	if err != nil {
		t.Fatal(err)
	}
	postgresPlanningStore, err := deliveryrollout.NewPostgresPlanningStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	if err := postgresPlanningStore.InTransaction(ctx, func(tx deliveryrollout.PlanningTransaction) error {
		if err := tx.InsertRollout(ctx, plan); err != nil {
			return err
		}
		return tx.AppendRolloutCreated(ctx, plan)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE delivery_rollouts SET state='progressing' WHERE id=$1`, fixtures.deliveryRolloutID); err != nil {
		t.Fatal(err)
	}
	return fixtures
}

func mustWorkerJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

type workerPlanningSeedStore struct {
	snapshot deliveryrollout.PlanningSnapshot
	plan     deliveryrollout.FrozenRollout
}

func (s *workerPlanningSeedStore) InTransaction(ctx context.Context, fn func(deliveryrollout.PlanningTransaction) error) error {
	return fn(s)
}

func (s *workerPlanningSeedStore) FindByIdempotency(context.Context, uuid.UUID, string) (deliveryrollout.FrozenRollout, bool, error) {
	return deliveryrollout.FrozenRollout{}, false, nil
}

func (s *workerPlanningSeedStore) LoadSnapshotForUpdate(context.Context, uuid.UUID) (deliveryrollout.PlanningSnapshot, error) {
	return s.snapshot, nil
}

func (s *workerPlanningSeedStore) InsertRollout(_ context.Context, plan deliveryrollout.FrozenRollout) error {
	s.plan = plan
	return nil
}

func (*workerPlanningSeedStore) AppendRolloutCreated(context.Context, deliveryrollout.FrozenRollout) error {
	return nil
}

func (*workerPlanningSeedStore) EnqueueRollout(context.Context, uuid.UUID) error { return nil }

func (*workerPlanningSeedStore) RecordAuditIntent(context.Context, audit.Intent) error { return nil }

func archiveWorkerAdminTarget(t *testing.T, ctx context.Context, client *asynq.Client, inspector *asynq.Inspector, taskID string) {
	t.Helper()
	if _, err := client.EnqueueContext(ctx, asynq.NewTask(workerAdminArchivedTargetType, nil),
		asynq.Queue("default"), asynq.MaxRetry(0), asynq.TaskID(taskID)); err != nil {
		t.Fatal(err)
	}
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		info, err := inspector.GetTaskInfo("default", taskID)
		if err == nil && info.State == asynq.TaskStateArchived {
			if info.Retried != 0 {
				t.Fatalf("admin target retried %d times, want 0", info.Retried)
			}
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("timed out archiving admin queue target: %v", ctx.Err())
		case <-ticker.C:
		}
	}
}

func waitForWorkerBusinessTask(t *testing.T, ctx context.Context, inspector *asynq.Inspector, queue, taskID, taskType string, pass int) {
	t.Helper()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		info, err := inspector.GetTaskInfo(queue, taskID)
		if err == nil {
			switch info.State {
			case asynq.TaskStateCompleted:
				if info.Retried != 0 {
					t.Fatalf("production handler %s retried %d times", taskType, info.Retried)
				}
				return
			case asynq.TaskStateArchived:
				if taskType == TypeCharlieAlertDispatch && pass == 1 && info.Retried == 0 && strings.Contains(info.LastErr, "delivery_failed") {
					return
				}
				retiredMessage := map[string]string{
					TypeSecurityScan:  "security:scan is retired; use security:ingest_scan_results",
					TypeAgentManifest: "agent:generate_manifest is retired; use the synchronous registration manifest API",
				}[taskType]
				if retiredMessage != "" && info.Retried == 0 && strings.Contains(info.LastErr, retiredMessage) {
					return
				}
				t.Fatalf("production handler %s archived: %s", taskType, info.LastErr)
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for production handler %s: %v", taskType, ctx.Err())
		case <-ticker.C:
		}
	}
}

func assertWorkerBusinessFixtures(t *testing.T, ctx context.Context, pool *pgxpool.Pool, inspector *asynq.Inspector, fixtures workerBusinessFixtures) {
	t.Helper()
	assertCount := func(label, query string, want int, args ...any) {
		t.Helper()
		var got int
		if err := pool.QueryRow(ctx, query, args...).Scan(&got); err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		if got != want {
			t.Fatalf("%s count=%d, want %d", label, got, want)
		}
	}
	assertCount("registration-token cleanup", `SELECT count(*) FROM cluster_registration_tokens WHERE cluster_id=$1`, 1, fixtures.clusterID)
	assertCount("apiserver-audit retention", `SELECT count(*) FROM apiserver_audit_events WHERE cluster_id=$1`, 1, fixtures.clusterID)
	assertCount("CRD stale-row prune", `SELECT count(*) FROM mirrored_ingress_classes WHERE cluster_id=$1`, 1, fixtures.clusterID)
	assertCount("canonical audit effect", `SELECT count(*) FROM audit_log WHERE id=$1`, 1, fixtures.auditOutboxID)
	assertCount("catalog chart upsert", `SELECT count(*) FROM helm_charts WHERE repository_id=$1`, 1, fixtures.catalogRepoID)
	assertCount("catalog version upsert", `SELECT count(*) FROM helm_chart_versions v JOIN helm_charts c ON c.id=v.chart_id WHERE c.repository_id=$1`, 1, fixtures.catalogRepoID)
	assertCount("monitoring config", `SELECT count(*) FROM cluster_monitoring_configs WHERE cluster_id=$1`, 1, fixtures.clusterID)

	pass, notificationUnique := fixtures.outbound.counts("/notification")
	if pass < 1 || pass > 2 || notificationUnique != 1 {
		t.Fatalf("notification receiver requests=%d unique_effects=%d, want pass 1..2/1", pass, notificationUnique)
	}
	for _, tc := range []struct {
		path       string
		want       int
		wantUnique int
	}{
		{path: "/catalog/index.yaml", want: pass, wantUnique: 1},
		{path: "/-/healthy", want: pass, wantUnique: 1},
		{path: "/telemetry", want: pass, wantUnique: 1},
		{path: "/webhook-ok", want: 1, wantUnique: 1},
		{path: "/webhook-recover", want: pass, wantUnique: 1},
		{path: "/siem-ok", want: 1, wantUnique: 1},
		{path: "/siem-recover", want: pass, wantUnique: 1},
	} {
		got, unique := fixtures.outbound.counts(tc.path)
		if got != tc.want || unique != tc.wantUnique {
			t.Fatalf("outbound receiver %s requests=%d unique_effects=%d, want %d/%d", tc.path, got, unique, tc.want, tc.wantUnique)
		}
	}
	smtpMessages, smtpUnique := fixtures.outbound.smtp.counts()
	if smtpMessages != 1 || smtpUnique != 1 {
		t.Fatalf("smtp receiver messages=%d unique_effects=%d, want 1/1", smtpMessages, smtpUnique)
	}

	var emailStatus string
	var emailAttempts int
	if err := pool.QueryRow(ctx, `SELECT status,attempts FROM email_messages WHERE id=$1`, fixtures.emailID).Scan(&emailStatus, &emailAttempts); err != nil {
		t.Fatal(err)
	}
	if emailStatus != "sent" || emailAttempts != 1 {
		t.Fatalf("email outcome=%s/%d, want sent/1", emailStatus, emailAttempts)
	}
	for _, delivery := range []struct {
		id           uuid.UUID
		wantStatus   string
		wantAttempts int
	}{
		{id: fixtures.webhookOKID, wantStatus: "delivered", wantAttempts: 1},
		{id: fixtures.webhookRetryID, wantStatus: map[bool]string{true: "failed", false: "delivered"}[pass == 1], wantAttempts: pass},
	} {
		var status string
		var attempts int
		if err := pool.QueryRow(ctx, `SELECT status,attempts FROM webhook_deliveries WHERE id=$1`, delivery.id).Scan(&status, &attempts); err != nil {
			t.Fatal(err)
		}
		if status != delivery.wantStatus || attempts != delivery.wantAttempts {
			t.Fatalf("webhook delivery %s outcome=%s/%d, want %s/%d", delivery.id, status, attempts, delivery.wantStatus, delivery.wantAttempts)
		}
	}
	var siemQueued, siemAttempts int
	if err := pool.QueryRow(ctx, `SELECT count(*),COALESCE(max(attempts),0) FROM siem_forward_queue`).Scan(&siemQueued, &siemAttempts); err != nil {
		t.Fatal(err)
	}
	if pass == 1 && (siemQueued != 2 || siemAttempts != 1) {
		t.Fatalf("siem recovery queue=%d attempts=%d, want 2/1", siemQueued, siemAttempts)
	}
	if pass == 2 && siemQueued != 0 {
		t.Fatalf("siem recovery queue=%d, want 0", siemQueued)
	}
	indexReads, chartReads := fixtures.deliveryHTTP.counts()
	if indexReads != 1 || chartReads != 1 {
		t.Fatalf("delivery source receiver index/chart reads=%d/%d, want 1/1", indexReads, chartReads)
	}
	var resolutionStatus, verificationStatus string
	var resolutionAttempts int
	if err := pool.QueryRow(ctx, `SELECT status,verification_status,resolution_attempt FROM delivery_source_resolutions WHERE id=$1`, fixtures.deliveryResolutionID).Scan(&resolutionStatus, &verificationStatus, &resolutionAttempts); err != nil {
		t.Fatal(err)
	}
	if resolutionStatus != "succeeded" || verificationStatus != "unsigned" || resolutionAttempts != 1 {
		t.Fatalf("delivery source outcome=%s/%s/%d, want succeeded/unsigned/1", resolutionStatus, verificationStatus, resolutionAttempts)
	}
	assertCount("delivery rollout assignment", `SELECT count(*) FROM delivery_rollout_clusters WHERE rollout_id=$1 AND state='released'`, 1, fixtures.deliveryRolloutID)
	assertCount("delivery desired deployment", `SELECT count(*) FROM cluster_deployments WHERE current_rollout_id=$1`, 1, fixtures.deliveryRolloutID)
	assertCount("delivery release event", `SELECT count(*) FROM delivery_rollout_events WHERE rollout_id=$1 AND event_type='cluster_released'`, 1, fixtures.deliveryRolloutID)
	assertCount("system rollout assignment", `SELECT count(*) FROM delivery_system_cluster_assignments WHERE rollout_id=$1 AND phase='released'`, 1, fixtures.deliverySystemRolloutID)
	assertCount("system rollout release event", `SELECT count(*) FROM delivery_system_events WHERE rollout_id=$1 AND event_type='cohort_released'`, 1, fixtures.deliverySystemRolloutID)
	assertCount("Charlie reconciled delivery", `SELECT count(*) FROM charlie_alert_deliveries WHERE finding_id=$1`, 1, fixtures.charliePlannedFindingID)
	assertCount("Charlie reconciled outbox", `SELECT count(*) FROM task_outbox WHERE dedupe_key LIKE $1`, 1, "charlie-alert:%")
	charlieRequests, charlieUnique := fixtures.outbound.counts("/charlie-recover")
	if charlieRequests != pass || charlieUnique != 1 {
		t.Fatalf("Charlie receiver requests=%d unique_effects=%d, want %d/1", charlieRequests, charlieUnique, pass)
	}
	var charlieStatus, charlieError string
	var charlieAttempts int
	if err := pool.QueryRow(ctx, `SELECT status,attempt_count,last_error_code FROM charlie_alert_deliveries WHERE id=$1`, fixtures.charlieDeliveryID).Scan(&charlieStatus, &charlieAttempts, &charlieError); err != nil {
		t.Fatal(err)
	}
	if pass == 1 && (charlieStatus != "retry" || charlieAttempts != 1 || charlieError != "delivery_failed") {
		t.Fatalf("Charlie recovery outcome=%s/%d/%s, want retry/1/delivery_failed", charlieStatus, charlieAttempts, charlieError)
	}
	if pass == 2 && (charlieStatus != "delivered" || charlieAttempts != 2 || charlieError != "") {
		t.Fatalf("Charlie recovery outcome=%s/%d/%s, want delivered/2/empty", charlieStatus, charlieAttempts, charlieError)
	}

	var managementStatus, managementError string
	var desiredGeneration, appliedGeneration int64
	if err := pool.QueryRow(ctx, `SELECT reconcile_status,last_error,desired_generation,applied_generation FROM management_backup_destinations WHERE id=$1`, fixtures.managementBackupID).
		Scan(&managementStatus, &managementError, &desiredGeneration, &appliedGeneration); err != nil {
		t.Fatal(err)
	}
	if managementStatus != "ready" || managementError != "" || desiredGeneration != 1 || appliedGeneration != 1 {
		t.Fatalf("management backup reconcile=%s/%s/%d/%d, want ready/empty/1/1", managementStatus, managementError, desiredGeneration, appliedGeneration)
	}
	cronJobs, err := fixtures.managementKubernetes.BatchV1().CronJobs(fixtures.managementNamespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	secrets, err := fixtures.managementKubernetes.CoreV1().Secrets(fixtures.managementNamespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(cronJobs.Items) != 1 || len(secrets.Items) != 1 {
		t.Fatalf("management backup external resources cronjobs/secrets=%d/%d, want 1/1", len(cronJobs.Items), len(secrets.Items))
	}
	var managementOperationStatus, managementOperationError string
	var managementOperationAttempts int
	if err := pool.QueryRow(ctx, `SELECT status,attempt_count,error_message FROM workload_operations WHERE id=$1`, fixtures.managementOperationID).
		Scan(&managementOperationStatus, &managementOperationAttempts, &managementOperationError); err != nil {
		t.Fatal(err)
	}
	if managementOperationStatus != "completed" || managementOperationAttempts != 1 || managementOperationError != "" {
		t.Fatalf("management backup operation=%s/%d/%s, want completed/1/empty", managementOperationStatus, managementOperationAttempts, managementOperationError)
	}

	var gitClusterCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM clusters WHERE name=$1`, fixtures.gitOpsClusterName).Scan(&gitClusterCount); err != nil {
		t.Fatal(err)
	}
	if gitClusterCount != 1 {
		t.Fatalf("GitOps registered cluster count=%d, want 1; logs=%s", gitClusterCount, fixtures.logs.String())
	}
	assertCount("GitOps source link", `SELECT count(*) FROM gitops_registered_clusters WHERE source_id=$1 AND status='active'`, 1, fixtures.gitOpsSourceID)
	assertCount("GitOps registration audit", `SELECT count(*) FROM audit_log WHERE action='gitops.cluster.registered' AND resource_name=$1`, 1, fixtures.gitOpsClusterName)
	var gitHead, gitError string
	var gitSynced bool
	if err := pool.QueryRow(ctx, `SELECT last_synced_at IS NOT NULL,last_synced_sha,last_error FROM gitops_registration_sources WHERE id=$1`, fixtures.gitOpsSourceID).
		Scan(&gitSynced, &gitHead, &gitError); err != nil {
		t.Fatal(err)
	}
	if !gitSynced || len(gitHead) != 40 || gitError != "" {
		t.Fatalf("GitOps source outcome synced/head/error=%t/%q/%q", gitSynced, gitHead, gitError)
	}

	rememberCiphertext := func(label, ciphertext string) {
		t.Helper()
		if ciphertext == "" || strings.Contains(ciphertext, "worker-credential-secret-canary") {
			t.Fatalf("%s ciphertext is empty or leaked plaintext", label)
		}
		if pass == 1 {
			fixtures.credentialCiphertexts[label] = ciphertext
		} else if ciphertext != fixtures.credentialCiphertexts[label] {
			t.Fatalf("%s ciphertext changed on idempotent second sweep", label)
		}
	}
	var backupAccess, backupSecret, backupCiphertext string
	if err := pool.QueryRow(ctx, `SELECT access_key,secret_key,encrypted_credentials FROM backup_storage_configs WHERE id=$1`, fixtures.backupCredentialID).
		Scan(&backupAccess, &backupSecret, &backupCiphertext); err != nil {
		t.Fatal(err)
	}
	if backupAccess != "" || backupSecret != "" {
		t.Fatal("backup plaintext credentials survived migration")
	}
	rememberCiphertext("backup", backupCiphertext)
	backupPlaintext, err := fixtures.credentialEncryptor.Decrypt(backupCiphertext)
	if err != nil || !strings.Contains(backupPlaintext, "worker-credential-secret-canary") {
		t.Fatalf("backup ciphertext is not decryptable to the seeded credential: %v", err)
	}
	var registryPassword, registryCiphertext string
	if err := pool.QueryRow(ctx, `SELECT registry_password,registry_password_encrypted FROM cluster_registry_configs WHERE id=$1`, fixtures.registryCredentialID).
		Scan(&registryPassword, &registryCiphertext); err != nil {
		t.Fatal(err)
	}
	if registryPassword != "" {
		t.Fatal("registry plaintext credential survived migration")
	}
	rememberCiphertext("registry", registryCiphertext)
	registryPlaintext, err := fixtures.credentialEncryptor.Decrypt(registryCiphertext)
	if err != nil || registryPlaintext != "worker-credential-secret-canary" {
		t.Fatalf("registry ciphertext is not decryptable to the seeded credential: %v", err)
	}
	for _, credential := range []struct {
		label string
		id    uuid.UUID
		table string
	}{
		{label: "helm", id: fixtures.helmCredentialID, table: "helm_repositories"},
		{label: "monitoring", id: fixtures.monitoringCredentialID, table: "monitoring_backends"},
	} {
		var publicJSON, ciphertext string
		query := `SELECT auth_config::text,auth_config_encrypted FROM ` + credential.table + ` WHERE id=$1`
		if err := pool.QueryRow(ctx, query, credential.id).Scan(&publicJSON, &ciphertext); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(publicJSON, "worker-credential-secret-canary") {
			t.Fatalf("%s public auth config retained plaintext secret", credential.label)
		}
		rememberCiphertext(credential.label, ciphertext)
		plaintext, err := fixtures.credentialEncryptor.Decrypt(ciphertext)
		if err != nil || !strings.Contains(plaintext, "worker-credential-secret-canary") {
			t.Fatalf("%s ciphertext is not decryptable to the seeded credential: %v", credential.label, err)
		}
	}

	var adminStatus, adminError string
	var adminAttempts int
	var adminEffectStarted, adminCompleted bool
	if err := pool.QueryRow(ctx, `SELECT status,attempt_count,last_error,effect_started_at IS NOT NULL,completed_at IS NOT NULL FROM admin_queue_operations WHERE id=$1`, fixtures.adminQueueOperationID).
		Scan(&adminStatus, &adminAttempts, &adminError, &adminEffectStarted, &adminCompleted); err != nil {
		t.Fatal(err)
	}
	if adminStatus != "succeeded" || adminAttempts != 1 || adminError != "" || !adminEffectStarted || !adminCompleted {
		t.Fatalf("admin archived operation=%s/%d/%s/%t/%t, want succeeded/1/empty/true/true", adminStatus, adminAttempts, adminError, adminEffectStarted, adminCompleted)
	}
	if _, err := inspector.GetTaskInfo("default", fixtures.adminArchivedTaskID); !errors.Is(err, asynq.ErrTaskNotFound) {
		t.Fatalf("admin archived target still exists after discard: %v", err)
	}

	for _, operation := range []struct {
		label string
		id    uuid.UUID
	}{
		{label: "pod delete", id: fixtures.podDeleteOperationID},
		{label: "vulnerability rescan", id: fixtures.vulnerabilityOperationID},
	} {
		var status, operationError string
		var attempts int
		if err := pool.QueryRow(ctx, `SELECT status,attempt_count,error_message FROM workload_operations WHERE id=$1`, operation.id).
			Scan(&status, &attempts, &operationError); err != nil {
			t.Fatal(err)
		}
		if status != "completed" || attempts != 1 || operationError != "" {
			t.Fatalf("%s operation=%s/%d/%s, want completed/1/empty", operation.label, status, attempts, operationError)
		}
		assertCount(operation.label+" event", `SELECT count(*) FROM workload_operation_events WHERE operation_id=$1 AND stage='completed'`, 1, operation.id)
	}
	var resourceStatus, resourceError, resourceVersion string
	var resourceAttempts int
	var resourceGeneration int64
	var resourceStatusCode int
	if err := pool.QueryRow(ctx, `SELECT status,attempt_count,error_code,observed_generation,COALESCE(observed_status_code,0),observed_resource_version FROM resource_operations WHERE id=$1`, fixtures.resourceOperationID).
		Scan(&resourceStatus, &resourceAttempts, &resourceError, &resourceGeneration, &resourceStatusCode, &resourceVersion); err != nil {
		t.Fatal(err)
	}
	if resourceStatus != "succeeded" || resourceAttempts != 1 || resourceError != "" || resourceGeneration != 1 || resourceStatusCode != http.StatusNoContent || resourceVersion != "" {
		t.Fatalf("resource operation=%s/%d/%s/%d/%d/%s", resourceStatus, resourceAttempts, resourceError, resourceGeneration, resourceStatusCode, resourceVersion)
	}
	var nodeStatus, nodeError string
	var nodeAttempts int
	var nodeGeneration int64
	if err := pool.QueryRow(ctx, `SELECT status,attempt_count,error_code,observed_generation FROM node_operations WHERE id=$1`, fixtures.nodeOperationID).
		Scan(&nodeStatus, &nodeAttempts, &nodeError, &nodeGeneration); err != nil {
		t.Fatal(err)
	}
	if nodeStatus != "succeeded" || nodeAttempts != 1 || nodeError != "" || nodeGeneration != 1 {
		t.Fatalf("node operation=%s/%d/%s/%d, want succeeded/1/empty/1", nodeStatus, nodeAttempts, nodeError, nodeGeneration)
	}
	assertCount("mesh durable observation", `SELECT count(*) FROM cluster_service_mesh WHERE cluster_id=$1 AND detected_mesh='none' AND last_error=''`, 1, fixtures.clusterID)
	assertCount("CRD ownership durable condition", `SELECT count(*) FROM cluster_conditions WHERE cluster_id=$1 AND type='CRDOwnershipSynced' AND status='False' AND reason='ExternalRefMissing'`, 1, fixtures.clusterID)
	assertCount("Gatekeeper durable convergence", `SELECT count(*) FROM authored_constraints WHERE cluster_id=$1 AND name=$2 AND sync_status='synced' AND observed_generation=1 AND last_error=''`, 1, fixtures.clusterID, fixtures.gatekeeperConstraintName)

	rememberTimestamp := func(label string, value time.Time) {
		t.Helper()
		if value.IsZero() {
			t.Fatalf("%s durable timestamp is empty", label)
		}
		if pass == 1 {
			fixtures.durableTimestamps[label] = value
		} else if !value.Equal(fixtures.durableTimestamps[label]) {
			t.Fatalf("%s timestamp changed on idempotent second delivery: %s -> %s", label, fixtures.durableTimestamps[label], value)
		}
	}
	var backupStatus string
	var backupStarted time.Time
	if err := pool.QueryRow(ctx, `SELECT status,started_at FROM backups WHERE id=$1`, fixtures.backupExecutionID).Scan(&backupStatus, &backupStarted); err != nil {
		t.Fatal(err)
	}
	if backupStatus != "running" {
		t.Fatalf("backup execution status=%s, want running", backupStatus)
	}
	rememberTimestamp("backup execution", backupStarted)
	var restoreStatus string
	var restoreStarted time.Time
	if err := pool.QueryRow(ctx, `SELECT status,started_at FROM restore_operations WHERE id=$1`, fixtures.restoreOperationID).Scan(&restoreStatus, &restoreStarted); err != nil {
		t.Fatal(err)
	}
	if restoreStatus != "running" {
		t.Fatalf("restore operation status=%s, want running", restoreStatus)
	}
	rememberTimestamp("restore operation", restoreStarted)

	var allowlistProvider, allowlistStatus, allowlistError, allowlistEffective string
	if err := pool.QueryRow(ctx, `SELECT detected_provider,sync_status,last_error,effective_cidrs::text FROM apiserver_allowlists WHERE cluster_id=$1`, fixtures.clusterID).
		Scan(&allowlistProvider, &allowlistStatus, &allowlistError, &allowlistEffective); err != nil {
		t.Fatal(err)
	}
	if allowlistProvider != providers.ProviderSelfManaged || allowlistStatus != "drifting" || allowlistError != "" || allowlistEffective != "[]" {
		t.Fatalf("allow-list outcome=%s/%s/%q/%s, want self_managed/drifting/empty/[]", allowlistProvider, allowlistStatus, allowlistError, allowlistEffective)
	}
	assertCount("allow-list immutable observations", `SELECT count(*) FROM apiserver_allowlist_snapshots WHERE cluster_id=$1 AND drift=true AND effective_cidrs='[]'::jsonb`, pass*2, fixtures.clusterID)

	var scanStatus, upstreamReport, terminalReason string
	var scanAttempt, scanPassed, scanFailed, scanWarned, scanSkipped int
	var scanCompleted time.Time
	if err := pool.QueryRow(ctx, `SELECT status,poll_attempt,passed,failed,warned,skipped,upstream_report_name,terminal_reason,completed_at FROM security_scan_results WHERE id=$1`, fixtures.securityIngestScanID).
		Scan(&scanStatus, &scanAttempt, &scanPassed, &scanFailed, &scanWarned, &scanSkipped, &upstreamReport, &terminalReason, &scanCompleted); err != nil {
		t.Fatal(err)
	}
	if scanStatus != "completed" || scanAttempt != 1 || scanPassed != 1 || scanFailed != 1 || scanWarned != 0 || scanSkipped != 0 || upstreamReport != "worker-cis-report" || terminalReason != "" {
		t.Fatalf("security ingest outcome=%s attempts=%d counts=%d/%d/%d/%d report=%s reason=%q", scanStatus, scanAttempt, scanPassed, scanFailed, scanWarned, scanSkipped, upstreamReport, terminalReason)
	}
	rememberTimestamp("security scan completion", scanCompleted)
	assertCount("security ingest reschedule outbox", `SELECT count(*) FROM task_outbox WHERE task_type='security:ingest_scan_results'`, 0)

	assertCount("project reconcile convergence", `SELECT count(*) FROM project_namespaces WHERE project_id=$1 AND cluster_id=$2 AND namespace=$3 AND last_reconciled_at IS NOT NULL AND last_reconcile_error='' AND locked_until IS NULL`, 1, fixtures.projectReconcileID, fixtures.clusterID, fixtures.projectNamespace)
	assertCount("cluster template convergence", `SELECT count(*) FROM cluster_template_applications WHERE cluster_id=$1 AND template_id=$2 AND status='applied' AND last_error='' AND applied_at IS NOT NULL`, 1, fixtures.clusterID, fixtures.clusterTemplateID)
	assertCount("cluster template default project", `SELECT count(*) FROM projects WHERE cluster_id=$1 AND name='worker-template-default' AND pod_security_profile='baseline' AND network_policy_mode='isolated'`, 1, fixtures.clusterID)
	assertCount("cluster template registration policy", `SELECT count(*) FROM cluster_registration_policies WHERE cluster_id=$1 AND source_template_id=$2 AND token_rotation_days=30`, 1, fixtures.clusterID, fixtures.clusterTemplateID)
	assertCount("cluster template mutations", `SELECT count(*) FROM clusters WHERE id=$1 AND environment='production' AND labels @> '{"worker-template":"applied"}'::jsonb`, 1, fixtures.clusterID)
	assertCount("cluster registry convergence", `SELECT count(*) FROM cluster_registry_configs WHERE id=$1 AND last_applied_at IS NOT NULL AND last_apply_error=''`, 1, fixtures.registryCredentialID)
	assertCount("network policy drift state", `SELECT count(*) FROM network_policy_applications WHERE id=$1 AND status='drifting' AND last_error='drift detected; will reapply on next tick' AND last_applied_at IS NOT NULL`, 1, fixtures.networkPolicyAppID)
	assertCount("network policy reconcile audit", `SELECT count(*) FROM audit_log WHERE action='networkpolicy.reconciled' AND resource_id=$1`, pass, fixtures.networkPolicyAppID.String())
	assertCount("network policy drift audit", `SELECT count(*) FROM audit_log WHERE action='networkpolicy.drift_detected' AND resource_id=$1`, pass, fixtures.networkPolicyAppID.String())
	assertCount("cluster snapshot retained rows", `SELECT count(*) FROM cluster_snapshots WHERE cluster_id=$1`, 3, fixtures.clusterID)
	assertCount("cluster snapshot terminal convergence", `SELECT count(*) FROM cluster_snapshots WHERE id IN ($1,$2) AND phase='Completed' AND completion_time='2026-01-01T00:01:00Z'`, 2, fixtures.snapshotApplyID, fixtures.snapshotPollID)
	assertCount("expired cluster snapshot cleanup", `SELECT count(*) FROM cluster_snapshots WHERE id=$1`, 0, fixtures.snapshotExpiredID)
	assertCount("scheduled snapshot singleton", `SELECT count(*) FROM cluster_snapshots WHERE cluster_id=$1 AND source='scheduled'`, 1, fixtures.clusterID)
	assertCount("snapshot schedule dispatch fence", `SELECT count(*) FROM cluster_snapshot_schedules WHERE id=$1 AND last_run_at IS NOT NULL AND last_run_status='fired'`, 1, fixtures.snapshotScheduleID)
	if pass == 2 {
		assertCount("scheduled snapshot poll convergence", `SELECT count(*) FROM cluster_snapshots WHERE cluster_id=$1 AND source='scheduled' AND phase='Completed'`, 1, fixtures.clusterID)
	}
	assertCount("control-plane apply convergence", `SELECT count(*) FROM control_plane_snapshots WHERE id=$1 AND status='succeeded' AND completed_at IS NOT NULL`, 1, fixtures.controlPlaneApplyID)
	wantSweepStatus := "running"
	if pass == 2 {
		wantSweepStatus = "succeeded"
	}
	assertCount("control-plane scheduled singleton", `SELECT count(*) FROM control_plane_snapshots WHERE cluster_id=$1 AND status=$2`, 1, fixtures.controlPlaneSweepCluster, wantSweepStatus)
	assertCount("kubectl session reap", `SELECT count(*) FROM kubectl_sessions WHERE id=$1 AND status='expired' AND closed_at IS NOT NULL`, 1, fixtures.kubectlSessionID)
	assertCount("kubectl session expiry audit", `SELECT count(*) FROM audit_log WHERE action='cluster.shell.session.expired' AND resource_id=$1`, 1, fixtures.kubectlSessionID.String())
	assertCount("cluster group metrics source", `SELECT count(*) FROM cluster_groups g JOIN clusters c ON c.group_id=g.id WHERE g.id=$1 AND g.enabled=true`, 1, fixtures.clusterGroupID)
	assertCount("Gatekeeper recovery sweep convergence", `SELECT count(*) FROM authored_constraints WHERE cluster_id=$1 AND name=$2 AND sync_status='synced' AND observed_generation=1 AND last_error=''`, 1, fixtures.clusterID, fixtures.gatekeeperRecoveryName)
	assertCount("cloud credential convergence", `SELECT count(*) FROM cloud_credential_materializations WHERE credential_id=$1 AND status='applied' AND last_applied_at IS NOT NULL AND last_error=''`, 2, fixtures.cloudCredentialID)
	assertCount("tool drift convergence", `SELECT count(*) FROM installed_charts WHERE id=$1 AND drift_detected=true AND drift_detail='helm revision 2 ahead of recorded revision 1 (out-of-band upgrade)' AND drift_checked_at IS NOT NULL`, 1, fixtures.toolDriftChartID)
	assertCount("Charlie trigger fail-closed convergence", `SELECT count(*) FROM charlie_trigger_events WHERE id=$1 AND state='dead' AND attempt_count=1 AND last_error_code='rule_inactive' AND dead_lettered_at IS NOT NULL`, 1, fixtures.charlieTriggerEventID)
	assertCount("Charlie trigger authority audit", `SELECT count(*) FROM audit_log WHERE action='charlie.trigger.dispatched' AND resource_id=$1`, pass, fixtures.charlieTriggerEventID.String())
	assertCount("deferred authenticated replay convergence", `SELECT count(*) FROM deferred_operations WHERE id=$1 AND status='dispatched' AND dispatched_at IS NOT NULL AND last_error=''`, 1, fixtures.deferredOperationID)
	deferredCalls, deferredErr := fixtures.deferredReplay.result()
	if deferredCalls != 1 || deferredErr != "" {
		t.Fatalf("deferred authenticated replay calls/error=%d/%q, want 1/empty", deferredCalls, deferredErr)
	}
	for _, decommission := range []struct {
		id, clusterID uuid.UUID
	}{
		{id: fixtures.decommissionDirectID, clusterID: fixtures.decommissionDirectCluster},
		{id: fixtures.decommissionSweepID, clusterID: fixtures.decommissionSweepCluster},
	} {
		assertCount("cluster decommission convergence", `SELECT count(*) FROM cluster_decommissions WHERE id=$1 AND status='succeeded' AND completed_at IS NOT NULL AND last_error=''`, 1, decommission.id)
		assertCount("cluster tombstone convergence", `SELECT count(*) FROM clusters WHERE id=$1 AND status='decommissioned' AND decommissioned_at IS NOT NULL`, 1, decommission.clusterID)
		assertCount("cluster agent token revocation", `SELECT count(*) FROM cluster_agent_tokens WHERE cluster_id=$1`, 0, decommission.clusterID)
		sends, disconnects := fixtures.decommissionTunnel.counts(decommission.clusterID.String())
		if sends != 1 || disconnects != 1 {
			t.Fatalf("cluster decommission tunnel %s sends/disconnects=%d/%d, want 1/1", decommission.clusterID, sends, disconnects)
		}
	}
	helmCalls, helmUnique := fixtures.helmStatus.counts()
	if helmCalls != pass || helmUnique != 1 {
		t.Fatalf("tool drift Helm probes/unique=%d/%d, want %d/1", helmCalls, helmUnique, pass)
	}

	clusterID := fixtures.clusterID.String()
	for _, call := range []struct {
		label, method, path string
	}{
		{label: "pod delete", method: http.MethodDelete, path: "/api/v1/namespaces/default/pods/worker-pod"},
		{label: "resource delete", method: http.MethodDelete, path: "/api/v1/namespaces/default/configmaps/worker-resource"},
		{label: "node cordon", method: http.MethodPatch, path: "/api/v1/nodes/worker-node"},
		{label: "vulnerability list", method: http.MethodGet, path: "/apis/aquasecurity.github.io/v1alpha1/vulnerabilityreports"},
		{label: "vulnerability delete", method: http.MethodDelete, path: "/apis/aquasecurity.github.io/v1alpha1/namespaces/default/vulnerabilityreports/worker-report"},
	} {
		calls, unique := fixtures.tunnelK8s.count(call.method, clusterID, call.path)
		if calls != 1 || unique != 1 {
			t.Fatalf("%s tunnel calls/unique=%d/%d, want 1/1", call.label, calls, unique)
		}
	}
	meshCalls, meshUnique := fixtures.tunnelK8s.count(http.MethodGet, clusterID, "/api/v1/namespaces")
	if meshCalls != pass || meshUnique != 1 {
		t.Fatalf("mesh tunnel calls/unique=%d/%d, want %d/1", meshCalls, meshUnique, pass)
	}
	gatekeeperCalls, gatekeeperUnique := fixtures.tunnelK8s.prefixCount(http.MethodPatch, clusterID, "/apis/constraints.gatekeeper.sh/v1beta1/k8srequiredlabels/worker-required-label")
	if gatekeeperCalls != pass || gatekeeperUnique != 1 {
		t.Fatalf("Gatekeeper tunnel calls/unique=%d/%d, want %d/1", gatekeeperCalls, gatekeeperUnique, pass)
	}
	recoveryCalls, recoveryUnique := fixtures.tunnelK8s.prefixCount(http.MethodPatch, clusterID, "/apis/constraints.gatekeeper.sh/v1beta1/k8srequiredlabels/"+fixtures.gatekeeperRecoveryName)
	if recoveryCalls != 1 || recoveryUnique != 1 {
		t.Fatalf("Gatekeeper recovery tunnel calls/unique=%d/%d, want 1/1", recoveryCalls, recoveryUnique)
	}
	cloudDirectCalls, cloudDirectUnique := fixtures.tunnelK8s.prefixCount(http.MethodPatch, clusterID, "/api/v1/namespaces/worker-cloud-direct/secrets/worker-cloud-credential")
	cloudDriftCalls, cloudDriftUnique := fixtures.tunnelK8s.prefixCount(http.MethodPatch, clusterID, "/api/v1/namespaces/worker-cloud-drift/secrets/worker-cloud-credential")
	if cloudDirectCalls != pass || cloudDirectUnique != 1 || cloudDriftCalls != 1 || cloudDriftUnique != 1 {
		t.Fatalf("cloud credential tunnel direct=%d/%d drift=%d/%d, want %d/1 and 1/1", cloudDirectCalls, cloudDirectUnique, cloudDriftCalls, cloudDriftUnique, pass)
	}
	policyProbes, policyProbeUnique := fixtures.tunnelK8s.count(http.MethodGet, clusterID, "/apis/templates.gatekeeper.sh/v1/constrainttemplates")
	policyApplies, policyApplyUnique := fixtures.tunnelK8s.containsCount(http.MethodPatch, clusterID, "fieldManager=astronomer-gatekeeper-policy")
	policyManifests, err := gatekeeperpolicy.Manifests()
	if err != nil {
		t.Fatal(err)
	}
	if policyProbes != pass || policyProbeUnique != 1 || policyApplies != pass*len(policyManifests) || policyApplyUnique != len(policyManifests) {
		t.Fatalf("Gatekeeper policy probe/apply=%d/%d and %d/%d, want %d/1 and %d/%d", policyProbes, policyProbeUnique, policyApplies, policyApplyUnique, pass, pass*len(policyManifests), len(policyManifests))
	}
	veleroPosts, veleroPostUnique := fixtures.tunnelK8s.count(http.MethodPost, clusterID, "/apis/velero.io/v1/namespaces/velero/backups")
	wantVeleroPosts := pass + 1
	if veleroPosts != wantVeleroPosts || veleroPostUnique != 2 {
		t.Fatalf("Velero snapshot POST calls/unique=%d/%d, want %d/2", veleroPosts, veleroPostUnique, wantVeleroPosts)
	}
	veleroGets, veleroGetUnique := fixtures.tunnelK8s.prefixCount(http.MethodGet, clusterID, "/apis/velero.io/v1/namespaces/velero/backups/")
	wantVeleroGets := 2
	if pass == 2 {
		wantVeleroGets = 3
	}
	if veleroGets != wantVeleroGets || veleroGetUnique != 1 {
		t.Fatalf("Velero snapshot GET calls/unique=%d/%d, want %d/1", veleroGets, veleroGetUnique, wantVeleroGets)
	}
	controlPlanePosts, controlPlanePostUnique := fixtures.tunnelK8s.count(http.MethodPost, clusterID, "/apis/batch/v1/namespaces/kube-system/jobs")
	sweepPosts, sweepPostUnique := fixtures.tunnelK8s.count(http.MethodPost, fixtures.controlPlaneSweepCluster.String(), "/apis/batch/v1/namespaces/kube-system/jobs")
	if controlPlanePosts != 1 || controlPlanePostUnique != 1 || sweepPosts != 1 || sweepPostUnique != 1 {
		t.Fatalf("control-plane Job POST main=%d/%d sweep=%d/%d, want 1/1 each", controlPlanePosts, controlPlanePostUnique, sweepPosts, sweepPostUnique)
	}
	controlPlaneGets, _ := fixtures.tunnelK8s.prefixCount(http.MethodGet, clusterID, "/apis/batch/v1/namespaces/kube-system/jobs/cp-snapshot-")
	sweepGets, _ := fixtures.tunnelK8s.prefixCount(http.MethodGet, fixtures.controlPlaneSweepCluster.String(), "/apis/batch/v1/namespaces/kube-system/jobs/cp-snapshot-")
	wantSweepGets := pass - 1
	if controlPlaneGets != 1 || sweepGets != wantSweepGets {
		t.Fatalf("control-plane Job GET main/sweep=%d/%d, want 1/%d", controlPlaneGets, sweepGets, wantSweepGets)
	}
	for _, path := range []string{
		"/api/v1/namespaces/kube-system/pods/astro-shell-worker",
		"/apis/rbac.authorization.k8s.io/v1/clusterrolebindings/astro-shell-worker",
		"/apis/rbac.authorization.k8s.io/v1/clusterroles/astro-shell-worker",
		"/api/v1/namespaces/kube-system/serviceaccounts/astro-shell-worker",
	} {
		calls, unique := fixtures.tunnelK8s.count(http.MethodDelete, clusterID, path)
		if calls != 1 || unique != 1 {
			t.Fatalf("kubectl teardown %s calls/unique=%d/%d, want 1/1", path, calls, unique)
		}
	}
	for _, path := range []string{
		"/apis/cis.cattle.io/v1/clusterscans/worker-cis-scan",
		"/apis/cis.cattle.io/v1/clusterscanreports/worker-cis-report",
	} {
		calls, unique := fixtures.tunnelK8s.count(http.MethodGet, clusterID, path)
		if calls != 1 || unique != 1 {
			t.Fatalf("security ingest tunnel %s calls/unique=%d/%d, want 1/1", path, calls, unique)
		}
	}
	projectReconciles := pass * 2
	for _, call := range []struct {
		label, method, path string
		want                int
	}{
		{label: "project namespace label", method: http.MethodPatch, path: "/api/v1/namespaces/" + fixtures.projectNamespace, want: projectReconciles},
		{label: "project quota delete", method: http.MethodDelete, path: "/api/v1/namespaces/" + fixtures.projectNamespace + "/resourcequotas/astronomer-project-quota", want: projectReconciles},
		{label: "project limit delete", method: http.MethodDelete, path: "/api/v1/namespaces/" + fixtures.projectNamespace + "/limitranges/astronomer-limits", want: projectReconciles},
		{label: "project service account read", method: http.MethodGet, path: "/api/v1/namespaces/" + fixtures.projectNamespace + "/serviceaccounts/default", want: projectReconciles},
		{label: "project service account patch", method: http.MethodPatch, path: "/api/v1/namespaces/" + fixtures.projectNamespace + "/serviceaccounts/default", want: projectReconciles},
		{label: "network policy drift read", method: http.MethodGet, path: "/apis/networking.k8s.io/v1/namespaces/" + fixtures.projectNamespace + "/networkpolicies/astronomer-np-worker-isolation", want: pass},
	} {
		calls, unique := fixtures.tunnelK8s.count(call.method, clusterID, call.path)
		if calls != call.want || unique != 1 {
			t.Fatalf("%s tunnel calls/unique=%d/%d, want %d/1", call.label, calls, unique, call.want)
		}
	}
	for _, call := range []struct {
		label, method, prefix string
		want                  int
	}{
		{label: "project resource quota apply", method: http.MethodPatch, prefix: "/api/v1/namespaces/" + fixtures.projectNamespace + "/resourcequotas/astronomer-quota", want: projectReconciles},
		{label: "project isolation apply", method: http.MethodPatch, prefix: "/apis/networking.k8s.io/v1/namespaces/" + fixtures.projectNamespace + "/networkpolicies/astronomer-isolation", want: projectReconciles},
		{label: "project registry apply", method: http.MethodPatch, prefix: "/api/v1/namespaces/" + fixtures.projectNamespace + "/secrets/astronomer-registry", want: projectReconciles},
		{label: "cluster registry apply", method: http.MethodPatch, prefix: "/api/v1/namespaces/" + fixtures.projectNamespace + "/secrets/worker-cluster-registry", want: pass * 2},
		{label: "network policy apply", method: http.MethodPatch, prefix: "/apis/networking.k8s.io/v1/namespaces/" + fixtures.projectNamespace + "/networkpolicies/astronomer-np-worker-isolation", want: pass},
	} {
		calls, unique := fixtures.tunnelK8s.prefixCount(call.method, clusterID, call.prefix)
		if calls != call.want || unique != 1 {
			t.Fatalf("%s tunnel calls/unique=%d/%d, want %d/1", call.label, calls, unique, call.want)
		}
	}

	var upgradeStatus string
	var upgradeCompleted bool
	if err := pool.QueryRow(ctx, `SELECT status,completed_at IS NOT NULL FROM agent_lifecycle_operations WHERE id=$1`, fixtures.upgradeID).Scan(&upgradeStatus, &upgradeCompleted); err != nil {
		t.Fatal(err)
	}
	if upgradeStatus != "failed" || !upgradeCompleted {
		t.Fatalf("stuck-upgrade outcome=%s completed=%t, want failed/true", upgradeStatus, upgradeCompleted)
	}

	var taskStatus, auditStatus string
	var taskAttempts, auditAttempts int
	if err := pool.QueryRow(ctx, `SELECT status,attempt_count FROM task_outbox WHERE id=$1`, fixtures.taskOutboxID).Scan(&taskStatus, &taskAttempts); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT status,attempt_count FROM audit_outbox WHERE id=$1`, fixtures.auditOutboxID).Scan(&auditStatus, &auditAttempts); err != nil {
		t.Fatal(err)
	}
	if taskStatus != "delivered" || taskAttempts != 1 || auditStatus != "delivered" || auditAttempts != 1 {
		t.Fatalf("outbox outcomes task=%s/%d audit=%s/%d, want delivered/1 each", taskStatus, taskAttempts, auditStatus, auditAttempts)
	}
	info, err := inspector.GetTaskInfo(fixtures.downstreamQueue, fixtures.taskOutboxID.String())
	if err != nil {
		t.Fatal(err)
	}
	if info.State != asynq.TaskStatePending || !bytes.Equal(info.Payload, fixtures.downstreamBody) {
		t.Fatalf("task-outbox effect state=%s payload_match=%t, want pending/true", info.State, bytes.Equal(info.Payload, fixtures.downstreamBody))
	}

	current := time.Now().UTC()
	for _, month := range []time.Time{current, current.AddDate(0, 1, 0)} {
		partition := fmt.Sprintf("audit_log_%04d_%02d", month.Year(), month.Month())
		var exists bool
		if err := pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, partition).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Fatalf("audit partition %s was not ensured", partition)
		}
	}
}

func integrationEffectHandler(pool *pgxpool.Pool, crashAtCheckpoint bool) asynq.HandlerFunc {
	return func(ctx context.Context, task *asynq.Task) error {
		var payload workerIntegrationPayload
		if err := json.Unmarshal(task.Payload(), &payload); err != nil {
			return fmt.Errorf("decode integration task: %w", err)
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err := tx.Exec(ctx, `
			UPDATE operations SET attempt_count=attempt_count+1,
			phase=CASE WHEN phase='completed' THEN phase ELSE 'execution_checkpoint' END,
			checkpoint_committed_at=COALESCE(checkpoint_committed_at, now())
			WHERE run_id=$1 AND operation_id=$2 AND task_type=$3`, payload.RunID, payload.OperationID, task.Type()); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO audit_events (run_id, operation_id, event_type) VALUES ($1,$2,'execution_checkpoint') ON CONFLICT DO NOTHING`, payload.RunID, payload.OperationID); err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		if crashAtCheckpoint {
			select {}
		}

		tx, err = pool.Begin(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err := tx.Exec(ctx, `INSERT INTO external_effects (run_id, operation_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, payload.RunID, payload.OperationID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO audit_events (run_id, operation_id, event_type) VALUES ($1,$2,'effect_applied') ON CONFLICT DO NOTHING`, payload.RunID, payload.OperationID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO audit_events (run_id, operation_id, event_type) VALUES ($1,$2,'operation_completed') ON CONFLICT DO NOTHING`, payload.RunID, payload.OperationID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE operations SET phase='completed', completed_at=COALESCE(completed_at, now()) WHERE run_id=$1 AND operation_id=$2`, payload.RunID, payload.OperationID); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
}

func integrationDescriptor(t *testing.T, taskType string) TaskDescriptor {
	t.Helper()
	for _, descriptor := range TaskDescriptors() {
		if descriptor.Type == taskType {
			return descriptor
		}
	}
	t.Fatalf("task %q is absent from the executable registry", taskType)
	return TaskDescriptor{}
}

func newWorkerIntegrationConsumer(t *testing.T, redisURL string, descriptors []TaskDescriptor, queues map[string]int) *Worker {
	t.Helper()
	redisOpt, err := asynq.ParseRedisURI(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	return &Worker{
		server: asynq.NewServer(redisOpt, asynq.Config{
			Concurrency:       1,
			Queues:            queues,
			StrictPriority:    true,
			TaskCheckInterval: 50 * time.Millisecond,
			RetryDelayFunc:    retryDelay,
			LogLevel:          asynq.ErrorLevel,
		}),
		mux:         asynq.NewServeMux(),
		log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		descriptors: descriptors,
	}
}

func openWorkerIntegrationPool(t *testing.T, ctx context.Context, databaseURL, schema string) *pgxpool.Pool {
	t.Helper()
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	return pool
}

func insertWorkerIntegrationIntent(t *testing.T, ctx context.Context, pool *pgxpool.Pool, runID, operationID string, descriptor TaskDescriptor) {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `INSERT INTO operations (run_id, operation_id, task_type, owner_name, queue_name) VALUES ($1,$2,$3,$4,$5)`, runID, operationID, descriptor.Type, descriptor.Owner, descriptor.Queue); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (run_id, operation_id, event_type) VALUES ($1,$2,'intent_committed')`, runID, operationID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func enqueueWorkerIntegrationTask(t *testing.T, client *asynq.Client, taskType, queue, runID, operationID string) string {
	t.Helper()
	payload, err := json.Marshal(workerIntegrationPayload{RunID: runID, OperationID: operationID})
	if err != nil {
		t.Fatal(err)
	}
	taskID := uuid.NewString()
	_, err = client.Enqueue(asynq.NewTask(taskType, payload), asynq.Queue(queue), asynq.MaxRetry(2), asynq.TaskID(taskID))
	if err != nil {
		t.Fatal(err)
	}
	return taskID
}

func assertWorkerIntegrationTaskState(t *testing.T, inspector *asynq.Inspector, queue, taskID string, state asynq.TaskState, retried int) {
	t.Helper()
	info, err := inspector.GetTaskInfo(queue, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if info.State != state || info.Retried != retried {
		t.Fatalf("task %s state=%s retried=%d, want %s/%d", taskID, info.State, info.Retried, state, retried)
	}
}

func waitForWorkerIntegrationTaskState(t *testing.T, ctx context.Context, inspector *asynq.Inspector, queue, taskID string, state asynq.TaskState, retried int) {
	t.Helper()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		info, err := inspector.GetTaskInfo(queue, taskID)
		if err != nil {
			t.Fatal(err)
		}
		if info.State == state && info.Retried == retried {
			if !strings.Contains(info.LastErr, "lease expired") {
				t.Fatalf("recovered task last error = %q, want lease-expiry classification", info.LastErr)
			}
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for task %s state %s/%d: %v", taskID, state, retried, ctx.Err())
		case <-ticker.C:
		}
	}
}

func assertWorkerIntegrationFamilies(t *testing.T, ctx context.Context, pool *pgxpool.Pool, runID string, cases []workerIntegrationCase) {
	t.Helper()
	for _, tc := range cases {
		descriptor := integrationDescriptor(t, tc.taskType)
		var owner, queue, phase string
		var attempts, effects, audits int
		err := pool.QueryRow(ctx, `
			SELECT o.owner_name, o.queue_name, o.phase, o.attempt_count,
			       count(DISTINCT e.operation_id), count(DISTINCT a.event_type)
			FROM operations o
			LEFT JOIN external_effects e USING (run_id, operation_id)
			LEFT JOIN audit_events a USING (run_id, operation_id)
			WHERE o.run_id=$1 AND o.operation_id=$2
			GROUP BY o.owner_name, o.queue_name, o.phase, o.attempt_count`, runID, tc.name).Scan(&owner, &queue, &phase, &attempts, &effects, &audits)
		if err != nil {
			t.Fatal(err)
		}
		if owner != string(descriptor.Owner) || queue != descriptor.Queue || phase != "completed" || attempts != 1 || effects != 1 || audits != 4 {
			t.Errorf("%s durable receipt = owner:%s queue:%s phase:%s attempts:%d effects:%d audits:%d", tc.name, owner, queue, phase, attempts, effects, audits)
		}
	}
}

func waitForWorkerIntegration(t *testing.T, ctx context.Context, pool *pgxpool.Pool, predicate func() (bool, error)) {
	t.Helper()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		ok, err := predicate()
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for durable worker state: %v", ctx.Err())
		case <-ticker.C:
		}
	}
}

func startWorkerIntegrationProcess(t *testing.T, databaseURL, redisURL, schema string, crash bool) *exec.Cmd {
	t.Helper()
	crashValue := "0"
	if crash {
		crashValue = "1"
	}
	command := exec.Command(os.Args[0], "-test.run=^TestWorkerIntegrationProcessHelper$", "-test.v")
	command.Env = append(os.Environ(),
		workerIntegrationHelperEnv+"=1",
		workerIntegrationDatabaseEnv+"="+databaseURL,
		workerIntegrationRedisEnv+"="+redisURL,
		workerIntegrationSchemaEnv+"="+schema,
		workerIntegrationCrashEnv+"="+crashValue,
	)
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopWorkerIntegrationProcess(command) })
	return command
}

func stopWorkerIntegrationProcess(command *exec.Cmd) {
	if command == nil || command.Process == nil || command.ProcessState != nil {
		return
	}
	_ = command.Process.Kill()
	_ = command.Wait()
}

const workerIntegrationDDL = `
CREATE TABLE operations (
 run_id text NOT NULL,
 operation_id text NOT NULL,
 task_type text NOT NULL,
 owner_name text NOT NULL,
 queue_name text NOT NULL,
 phase text NOT NULL DEFAULT 'intent_committed',
 attempt_count integer NOT NULL DEFAULT 0,
 intent_committed_at timestamptz NOT NULL DEFAULT now(),
 checkpoint_committed_at timestamptz,
 completed_at timestamptz,
 PRIMARY KEY (run_id, operation_id)
);
CREATE TABLE external_effects (
 run_id text NOT NULL,
 operation_id text NOT NULL,
 applied_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY (run_id, operation_id),
 FOREIGN KEY (run_id, operation_id) REFERENCES operations (run_id, operation_id)
);
CREATE TABLE audit_events (
 run_id text NOT NULL,
 operation_id text NOT NULL,
 event_type text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY (run_id, operation_id, event_type),
 FOREIGN KEY (run_id, operation_id) REFERENCES operations (run_id, operation_id)
);`
