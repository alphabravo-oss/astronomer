package tunnel

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	dto "github.com/prometheus/client_model/go"
)

// recordingPublisher captures every Publish call so tests can assert what
// the hub fanned out onto the SSE bus.
type recordingPublisher struct {
	mu     sync.Mutex
	events []recordedEvent
}

type recordedEvent struct {
	Type string
	Data any
}

type recordingValidator struct {
	mu                 sync.Mutex
	tokenClusterID     string
	tokenErr           error
	clusterAgentToken  sqlc.ClusterAgentToken
	clusterAgentErr    error
	upsertArgs         []sqlc.UpsertClusterHealthStatusParams
	upsertErr          error
	updateHeartbeatErr error
	createConnArgs     []sqlc.CreateAgentConnectionParams
	updateConnArgs     []sqlc.UpdateAgentConnectionStatusParams
	pingIDs            []uuid.UUID
	upsertAgentArgs    []sqlc.UpsertClusterAgentTokenParams
	touchedAgentIDs    []uuid.UUID
	disconnectClusters []uuid.UUID
	pendingOp          *sqlc.AgentLifecycleOperation
	claimErr           error
	completedOps       []sqlc.CompleteAgentLifecycleOperationParams
	markSucceededArgs  []sqlc.MarkRunningAgentUpgradeSucceededByVersionParams
	auditRows          []sqlc.CreateAuditLogV1Params
}

func (r *recordingValidator) GetRegistrationTokenByToken(context.Context, string) (sqlc.ClusterRegistrationToken, error) {
	if r.tokenErr != nil {
		return sqlc.ClusterRegistrationToken{}, r.tokenErr
	}
	if r.tokenClusterID == "" {
		return sqlc.ClusterRegistrationToken{}, errors.New("not implemented")
	}
	clusterID, err := uuid.Parse(r.tokenClusterID)
	if err != nil {
		return sqlc.ClusterRegistrationToken{}, err
	}
	return sqlc.ClusterRegistrationToken{ID: uuid.New(), ClusterID: clusterID}, nil
}

func (r *recordingValidator) MarkRegistrationTokenUsed(context.Context, uuid.UUID) error {
	return nil
}

func (r *recordingValidator) GetClusterAgentTokenByClusterID(_ context.Context, clusterID uuid.UUID) (sqlc.ClusterAgentToken, error) {
	if r.clusterAgentErr != nil {
		return sqlc.ClusterAgentToken{}, r.clusterAgentErr
	}
	if r.clusterAgentToken.ClusterID == clusterID && r.clusterAgentToken.Token != "" {
		return r.clusterAgentToken, nil
	}
	return sqlc.ClusterAgentToken{}, errors.New("not found")
}

func (r *recordingValidator) GetClusterAgentTokenByToken(_ context.Context, token string) (sqlc.ClusterAgentToken, error) {
	if r.clusterAgentErr != nil {
		return sqlc.ClusterAgentToken{}, r.clusterAgentErr
	}
	if token != "" && (r.clusterAgentToken.Token == token || r.clusterAgentToken.TokenHash == auth.HashOpaqueToken(token)) {
		return r.clusterAgentToken, nil
	}
	return sqlc.ClusterAgentToken{}, errors.New("not found")
}

func (r *recordingValidator) UpsertClusterAgentToken(_ context.Context, arg sqlc.UpsertClusterAgentTokenParams) (sqlc.ClusterAgentToken, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.upsertAgentArgs = append(r.upsertAgentArgs, arg)
	r.clusterAgentToken = sqlc.ClusterAgentToken{ID: uuid.New(), ClusterID: arg.ClusterID, Token: arg.Token, TokenHash: arg.TokenHash}
	return r.clusterAgentToken, nil
}

func (r *recordingValidator) TouchClusterAgentToken(_ context.Context, id uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.touchedAgentIDs = append(r.touchedAgentIDs, id)
	return nil
}

func (r *recordingValidator) UpdateClusterHeartbeat(context.Context, sqlc.UpdateClusterHeartbeatParams) error {
	return r.updateHeartbeatErr
}

func (r *recordingValidator) UpsertClusterHealthStatus(_ context.Context, arg sqlc.UpsertClusterHealthStatusParams) (sqlc.ClusterHealthStatus, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.upsertArgs = append(r.upsertArgs, arg)
	return sqlc.ClusterHealthStatus{}, r.upsertErr
}

func (r *recordingValidator) CreateAgentConnection(_ context.Context, arg sqlc.CreateAgentConnectionParams) (sqlc.AgentConnection, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.createConnArgs = append(r.createConnArgs, arg)
	return sqlc.AgentConnection{ID: uuid.New(), ClusterID: arg.ClusterID, AgentID: arg.AgentID, SessionID: arg.SessionID, Status: arg.Status}, nil
}

func (r *recordingValidator) UpdateAgentConnectionStatus(_ context.Context, arg sqlc.UpdateAgentConnectionStatusParams) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.updateConnArgs = append(r.updateConnArgs, arg)
	return nil
}

func (r *recordingValidator) DisconnectActiveConnectionsByCluster(_ context.Context, clusterID uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.disconnectClusters = append(r.disconnectClusters, clusterID)
	return nil
}

func (r *recordingValidator) UpdateAgentConnectionPing(_ context.Context, id uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pingIDs = append(r.pingIDs, id)
	return nil
}

func (r *recordingValidator) ClaimPendingAgentLifecycleOperation(context.Context, uuid.UUID) (sqlc.AgentLifecycleOperation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.claimErr != nil {
		return sqlc.AgentLifecycleOperation{}, r.claimErr
	}
	if r.pendingOp == nil {
		return sqlc.AgentLifecycleOperation{}, pgx.ErrNoRows
	}
	op := *r.pendingOp
	r.pendingOp = nil
	return op, nil
}

func (r *recordingValidator) CompleteAgentLifecycleOperation(_ context.Context, arg sqlc.CompleteAgentLifecycleOperationParams) (sqlc.AgentLifecycleOperation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.completedOps = append(r.completedOps, arg)
	return sqlc.AgentLifecycleOperation{ID: arg.ID, Status: arg.Status, LastError: arg.LastError}, nil
}

func (r *recordingValidator) MarkRunningAgentUpgradeSucceededByVersion(_ context.Context, arg sqlc.MarkRunningAgentUpgradeSucceededByVersionParams) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.markSucceededArgs = append(r.markSucceededArgs, arg)
	return 0, nil
}

func (r *recordingValidator) CreateAuditLogV1(_ context.Context, arg sqlc.CreateAuditLogV1Params) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.auditRows = append(r.auditRows, arg)
	return nil
}

func (r *recordingValidator) SnapshotUpserts() []sqlc.UpsertClusterHealthStatusParams {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]sqlc.UpsertClusterHealthStatusParams, len(r.upsertArgs))
	copy(out, r.upsertArgs)
	return out
}

func (r *recordingValidator) SnapshotCreates() []sqlc.CreateAgentConnectionParams {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]sqlc.CreateAgentConnectionParams, len(r.createConnArgs))
	copy(out, r.createConnArgs)
	return out
}

func (r *recordingValidator) SnapshotDisconnects() []sqlc.UpdateAgentConnectionStatusParams {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]sqlc.UpdateAgentConnectionStatusParams, len(r.updateConnArgs))
	copy(out, r.updateConnArgs)
	return out
}

func (r *recordingValidator) SnapshotPings() []uuid.UUID {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]uuid.UUID, len(r.pingIDs))
	copy(out, r.pingIDs)
	return out
}

func (r *recordingValidator) SnapshotAgentTokenUpserts() []sqlc.UpsertClusterAgentTokenParams {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]sqlc.UpsertClusterAgentTokenParams, len(r.upsertAgentArgs))
	copy(out, r.upsertAgentArgs)
	return out
}

func (r *recordingValidator) SnapshotDisconnectedClusters() []uuid.UUID {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]uuid.UUID, len(r.disconnectClusters))
	copy(out, r.disconnectClusters)
	return out
}

func (r *recordingValidator) SnapshotCompletedOps() []sqlc.CompleteAgentLifecycleOperationParams {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]sqlc.CompleteAgentLifecycleOperationParams, len(r.completedOps))
	copy(out, r.completedOps)
	return out
}

func (r *recordingValidator) SnapshotMarkSucceededArgs() []sqlc.MarkRunningAgentUpgradeSucceededByVersionParams {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]sqlc.MarkRunningAgentUpgradeSucceededByVersionParams, len(r.markSucceededArgs))
	copy(out, r.markSucceededArgs)
	return out
}

func (r *recordingValidator) SnapshotAuditRows() []sqlc.CreateAuditLogV1Params {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]sqlc.CreateAuditLogV1Params, len(r.auditRows))
	copy(out, r.auditRows)
	return out
}

func (r *recordingPublisher) Publish(eventType string, data any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, recordedEvent{Type: eventType, Data: data})
}

func (r *recordingPublisher) Snapshot() []recordedEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]recordedEvent, len(r.events))
	copy(out, r.events)
	return out
}

func TestHandleHeartbeatClaimsPendingAgentUpgrade(t *testing.T) {
	clusterID := uuid.New()
	opID := uuid.New()
	validator := &recordingValidator{
		pendingOp: &sqlc.AgentLifecycleOperation{
			ID:            opID,
			ClusterID:     clusterID,
			OperationType: "agent_upgrade",
			Status:        "running",
			TargetVersion: "v1.2.3",
			TargetImage:   "example.com/astronomer-agent:v1.2.3",
		},
	}
	h := NewHubWithValidator(slog.Default(), validator)
	conn := &AgentConnection{ClusterID: clusterID.String(), sendCh: make(chan *protocol.Message, 1)}
	h.agents.Set(clusterID.String(), conn)

	body, _ := json.Marshal(protocol.HeartbeatPayload{AgentVersion: "v1.0.0"})
	h.handleHeartbeat(conn, &protocol.Message{Type: protocol.MsgHeartbeat, Payload: body})

	select {
	case msg := <-conn.sendCh:
		if msg.Type != protocol.MsgAgentUpgrade {
			t.Fatalf("message type = %s, want %s", msg.Type, protocol.MsgAgentUpgrade)
		}
		var payload protocol.AgentUpgradePayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			t.Fatalf("decode upgrade payload: %v", err)
		}
		if payload.OperationID != opID.String() || payload.TargetImage != "example.com/astronomer-agent:v1.2.3" {
			t.Fatalf("upgrade payload = %+v", payload)
		}
	default:
		t.Fatal("expected agent upgrade command")
	}
	if got := validator.SnapshotMarkSucceededArgs(); len(got) != 1 || got[0].TargetVersion != "v1.0.0" {
		t.Fatalf("mark succeeded args = %+v", got)
	}
}

func TestHandleHeartbeatPersistsSchemaVersionAndPublishes(t *testing.T) {
	clusterID := uuid.New()
	validator := &recordingValidator{}
	h := NewHubWithValidator(slog.Default(), validator)
	pub := &recordingPublisher{}
	h.SetPublisher(pub)
	conn := &AgentConnection{ClusterID: clusterID.String(), sendCh: make(chan *protocol.Message, 1)}

	body, _ := json.Marshal(protocol.HeartbeatPayload{
		SchemaVersion:          protocol.HeartbeatSchemaVersion,
		AgentVersion:           "v1.2.3",
		AgentBuildSHA:          "abc123",
		KubernetesVersion:      "v1.30.2",
		NodeCount:              3,
		PodCount:               27,
		PrivilegeProfile:       "operator",
		AvailableAPIs:          []string{"apps/v1", "v1"},
		EnabledFeatures:        []string{"watch", "logs", "exec"},
		DeniedFeatures:         []string{"cluster_admin"},
		LastSuccessfulAction:   "heartbeat.collect",
		LastSuccessfulActionAt: "2026-06-13T12:00:00Z",
		DegradedReasons:        []string{"metrics API unavailable"},
	})
	h.handleHeartbeat(conn, &protocol.Message{Type: protocol.MsgHeartbeat, Payload: body})

	upserts := validator.SnapshotUpserts()
	if len(upserts) != 1 {
		t.Fatalf("health upserts = %d, want 1", len(upserts))
	}
	var conditions map[string]any
	if err := json.Unmarshal(upserts[0].Conditions, &conditions); err != nil {
		t.Fatalf("decode conditions: %v", err)
	}
	if conditions["heartbeat_schema_version"] != float64(protocol.HeartbeatSchemaVersion) {
		t.Fatalf("heartbeat_schema_version condition = %#v", conditions["heartbeat_schema_version"])
	}
	if conditions["agent_build_sha"] != "abc123" || conditions["privilege_profile"] != "operator" {
		t.Fatalf("capability conditions = %#v", conditions)
	}
	if got, _ := conditions["last_successful_action"].(string); got != "heartbeat.collect" {
		t.Fatalf("last_successful_action = %#v", conditions["last_successful_action"])
	}

	events := pub.Snapshot()
	if len(events) != 1 || events[0].Type != "cluster.heartbeat" {
		t.Fatalf("events = %+v, want one cluster.heartbeat", events)
	}
	data, ok := events[0].Data.(map[string]any)
	if !ok {
		t.Fatalf("event data type = %T, want map", events[0].Data)
	}
	if data["heartbeat_schema_version"] != protocol.HeartbeatSchemaVersion {
		t.Fatalf("event heartbeat_schema_version = %#v", data["heartbeat_schema_version"])
	}
	if data["agent_build_sha"] != "abc123" || data["privilege_profile"] != "operator" {
		t.Fatalf("event capability data = %#v", data)
	}
	if got, ok := data["enabled_features"].([]string); !ok || len(got) != 3 {
		t.Fatalf("event enabled_features = %#v", data["enabled_features"])
	}
}

func TestHandleAgentUpgradeResultCompletesOperation(t *testing.T) {
	clusterID := uuid.New()
	opID := uuid.New()
	validator := &recordingValidator{}
	h := NewHubWithValidator(slog.Default(), validator)
	conn := &AgentConnection{ClusterID: clusterID.String()}

	body, _ := json.Marshal(protocol.AgentUpgradeResultPayload{
		OperationID:   opID.String(),
		ClusterID:     clusterID.String(),
		Success:       true,
		ObservedImage: "example.com/astronomer-agent:v1.2.3",
	})
	h.handleAgentUpgradeResult(conn, &protocol.Message{Type: protocol.MsgAgentUpgradeResult, Payload: body})

	completed := validator.SnapshotCompletedOps()
	if len(completed) != 1 {
		t.Fatalf("completed ops = %+v", completed)
	}
	if completed[0].ID != opID || completed[0].Status != "succeeded" || completed[0].LastError != "" {
		t.Fatalf("completion = %+v", completed[0])
	}
}

// TestHandleStateUpdatePublishesK8sChanged verifies a STATE_UPDATE from the
// agent becomes a `cluster.k8s_changed` SSE event with the cluster_id
// stitched in.
func TestHandleStateUpdatePublishesK8sChanged(t *testing.T) {
	tunnelStateUpdatesReceivedTotal.Reset()
	tunnelStateUpdatesHandledTotal.Reset()
	h := NewHub(slog.Default())
	pub := &recordingPublisher{}
	h.SetPublisher(pub)

	conn := &AgentConnection{
		ClusterID: "cluster-xyz",
		Streams:   NewStreamManager(10),
		sendCh:    make(chan *protocol.Message, 10),
	}

	body, _ := json.Marshal(protocol.StateUpdatePayload{
		Op:              protocol.StateUpdateOpAdded,
		Kind:            "Pod",
		APIVersion:      "v1",
		Namespace:       "default",
		Name:            "echo",
		ResourceVersion: "42",
	})
	msg := &protocol.Message{Type: protocol.MsgStateUpdate, Payload: body}

	h.handleMessage(conn, msg)

	events := pub.Snapshot()
	if len(events) != 1 {
		t.Fatalf("expected exactly 1 publish, got %d", len(events))
	}
	if events[0].Type != "cluster.k8s_changed" {
		t.Fatalf("expected cluster.k8s_changed, got %s", events[0].Type)
	}
	data, ok := events[0].Data.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any payload, got %T", events[0].Data)
	}
	if data["cluster_id"] != "cluster-xyz" {
		t.Errorf("expected cluster_id=cluster-xyz, got %v", data["cluster_id"])
	}
	if data["kind"] != "Pod" {
		t.Errorf("expected kind=Pod, got %v", data["kind"])
	}
	if data["op"] != "added" {
		t.Errorf("expected op=added, got %v", data["op"])
	}
	if data["namespace"] != "default" {
		t.Errorf("expected namespace=default, got %v", data["namespace"])
	}
	if data["name"] != "echo" {
		t.Errorf("expected name=echo, got %v", data["name"])
	}
	if got := counterValue(t, tunnelStateUpdatesReceivedTotal.WithLabelValues(observability.MetricValues("Pod")...)); got != 1 {
		t.Fatalf("received_total{kind=Pod} = %v, want 1", got)
	}
	if got := counterValue(t, tunnelStateUpdatesHandledTotal.WithLabelValues(observability.MetricValues("published", "Pod")...)); got != 1 {
		t.Fatalf("handled_total{outcome=published,kind=Pod} = %v, want 1", got)
	}
}

// TestHandleStateUpdateRateLimited verifies the server-side limiter collapses
// bursts on the same (cluster, kind, namespace) tuple. Without this, a
// thousand pod updates inside a Deployment rollout would each fan out.
func TestHandleStateUpdateRateLimited(t *testing.T) {
	tunnelStateUpdatesReceivedTotal.Reset()
	tunnelStateUpdatesHandledTotal.Reset()
	h := NewHub(slog.Default())
	pub := &recordingPublisher{}
	h.SetPublisher(pub)

	conn := &AgentConnection{
		ClusterID: "cluster-rate",
		Streams:   NewStreamManager(10),
		sendCh:    make(chan *protocol.Message, 10),
	}

	for i := 0; i < 10; i++ {
		body, _ := json.Marshal(protocol.StateUpdatePayload{
			Op:        protocol.StateUpdateOpModified,
			Kind:      "Pod",
			Namespace: "default",
			Name:      "echo",
		})
		h.handleMessage(conn, &protocol.Message{Type: protocol.MsgStateUpdate, Payload: body})
	}

	events := pub.Snapshot()
	if len(events) != 1 {
		t.Fatalf("expected the limiter to collapse the burst to 1 event, got %d", len(events))
	}
	if got := counterValue(t, tunnelStateUpdatesHandledTotal.WithLabelValues(observability.MetricValues("rate_limited", "Pod")...)); got != 9 {
		t.Fatalf("handled_total{outcome=rate_limited,kind=Pod} = %v, want 9", got)
	}
}

// TestHandleStateUpdateDistinctKeysPassThrough confirms that distinct
// (cluster, kind, namespace) tuples don't share a budget.
func TestHandleStateUpdateDistinctKeysPassThrough(t *testing.T) {
	h := NewHub(slog.Default())
	pub := &recordingPublisher{}
	h.SetPublisher(pub)

	conn := &AgentConnection{
		ClusterID: "cluster-keys",
		Streams:   NewStreamManager(10),
		sendCh:    make(chan *protocol.Message, 10),
	}

	cases := []protocol.StateUpdatePayload{
		{Op: protocol.StateUpdateOpAdded, Kind: "Pod", Namespace: "ns-a", Name: "a"},
		{Op: protocol.StateUpdateOpAdded, Kind: "Pod", Namespace: "ns-b", Name: "a"},
		{Op: protocol.StateUpdateOpAdded, Kind: "Service", Namespace: "ns-a", Name: "a"},
		{Op: protocol.StateUpdateOpAdded, Kind: "Deployment", Namespace: "ns-a", Name: "a"},
	}
	for _, c := range cases {
		body, _ := json.Marshal(c)
		h.handleMessage(conn, &protocol.Message{Type: protocol.MsgStateUpdate, Payload: body})
	}

	events := pub.Snapshot()
	if len(events) != len(cases) {
		t.Fatalf("expected %d distinct fan-outs, got %d", len(cases), len(events))
	}
}

// TestHandleStateUpdateCoalesceKeyRespected verifies the hub honors the
// caller-provided coalescing hint instead of collapsing solely on kind /
// namespace or name.
func TestHandleStateUpdateCoalesceKeyRespected(t *testing.T) {
	h := NewHub(slog.Default())
	pub := &recordingPublisher{}
	h.SetPublisher(pub)

	conn := &AgentConnection{
		ClusterID: "cluster-coalesce",
		Streams:   NewStreamManager(10),
		sendCh:    make(chan *protocol.Message, 10),
	}

	cases := []protocol.StateUpdatePayload{
		{
			Op:          protocol.StateUpdateOpModified,
			Kind:        "Pod",
			Namespace:   "default",
			Name:        "frontend-1",
			CoalesceKey: "Deployment|default|frontend",
		},
		{
			Op:          protocol.StateUpdateOpModified,
			Kind:        "Pod",
			Namespace:   "default",
			Name:        "frontend-2",
			CoalesceKey: "Deployment|default|frontend",
		},
		{
			Op:          protocol.StateUpdateOpModified,
			Kind:        "Pod",
			Namespace:   "default",
			Name:        "api-1",
			CoalesceKey: "Deployment|default|api",
		},
	}
	for _, c := range cases {
		body, _ := json.Marshal(c)
		h.handleMessage(conn, &protocol.Message{Type: protocol.MsgStateUpdate, Payload: body})
	}

	events := pub.Snapshot()
	if len(events) != 2 {
		t.Fatalf("expected 2 publishes after coalescing, got %d", len(events))
	}
}

// TestStateUpdateLimiterAllow exercises the limiter's tick gating directly.
// Independent of the hub so a regression here doesn't depend on the
// publisher plumbing.
func TestStateUpdateLimiterAllow(t *testing.T) {
	r := newStateUpdateLimiter(500 * time.Millisecond)
	now := time.Unix(0, 0)
	r.now = func() time.Time { return now }

	if !r.allow("k") {
		t.Fatal("first allow must pass")
	}
	if r.allow("k") {
		t.Fatal("second allow within window must be rejected")
	}
	now = now.Add(time.Second)
	if !r.allow("k") {
		t.Fatal("allow after window must pass")
	}
}

// TestHandleStateUpdateInvalidPayload makes sure malformed payloads don't
// panic and don't publish anything.
func TestHandleStateUpdateInvalidPayload(t *testing.T) {
	tunnelStateUpdatesReceivedTotal.Reset()
	tunnelStateUpdatesHandledTotal.Reset()
	h := NewHub(slog.Default())
	pub := &recordingPublisher{}
	h.SetPublisher(pub)

	conn := &AgentConnection{
		ClusterID: "cluster-bad",
		Streams:   NewStreamManager(10),
		sendCh:    make(chan *protocol.Message, 10),
	}

	h.handleMessage(conn, &protocol.Message{
		Type:    protocol.MsgStateUpdate,
		Payload: []byte("{not-json"),
	})

	if got := len(pub.Snapshot()); got != 0 {
		t.Fatalf("expected 0 publishes for invalid payload, got %d", got)
	}
	if got := counterValue(t, tunnelStateUpdatesHandledTotal.WithLabelValues(observability.MetricValues("invalid", "unknown")...)); got != 1 {
		t.Fatalf("handled_total{outcome=invalid,kind=unknown} = %v, want 1", got)
	}
}

func TestHandleMetricsPublishesClusterMetricsAndPersistsHealth(t *testing.T) {
	validator := &recordingValidator{}
	h := NewHubWithValidator(slog.Default(), validator)
	pub := &recordingPublisher{}
	h.SetPublisher(pub)

	clusterID := uuid.New()
	conn := &AgentConnection{
		ClusterID: clusterID.String(),
		Streams:   NewStreamManager(10),
		sendCh:    make(chan *protocol.Message, 10),
	}

	body, _ := json.Marshal(protocol.MetricsPayload{
		Timestamp:          "2026-05-10T00:05:00Z",
		MetricsAvailable:   true,
		ClusterCPUUsage:    42.5,
		ClusterMemoryUsage: 64.25,
		ClusterPodCount:    17,
		ClusterNodeCount:   3,
		Nodes:              []protocol.NodeMetrics{{Name: "node-a", CPUPercent: 50}},
		Namespaces:         []protocol.NamespaceMetrics{{Name: "default", PodCount: 8}},
	})
	h.handleMessage(conn, &protocol.Message{Type: protocol.MsgMetrics, Payload: body})

	upserts := validator.SnapshotUpserts()
	if len(upserts) != 1 {
		t.Fatalf("expected exactly 1 health upsert, got %d", len(upserts))
	}
	if upserts[0].ClusterID != clusterID {
		t.Fatalf("expected cluster id %s, got %s", clusterID, upserts[0].ClusterID)
	}
	if upserts[0].CpuUsagePercent != 42.5 {
		t.Fatalf("expected cpu 42.5, got %v", upserts[0].CpuUsagePercent)
	}
	if upserts[0].MemoryUsagePercent != 64.25 {
		t.Fatalf("expected memory 64.25, got %v", upserts[0].MemoryUsagePercent)
	}
	if upserts[0].PodCount != 17 || upserts[0].NodeCount != 3 {
		t.Fatalf("expected pod/node counts 17/3, got %d/%d", upserts[0].PodCount, upserts[0].NodeCount)
	}

	events := pub.Snapshot()
	if len(events) != 1 {
		t.Fatalf("expected 1 publish, got %d", len(events))
	}
	if events[0].Type != "cluster.metrics" {
		t.Fatalf("expected cluster.metrics, got %s", events[0].Type)
	}
	data, ok := events[0].Data.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any payload, got %T", events[0].Data)
	}
	if data["cluster_id"] != clusterID.String() {
		t.Fatalf("expected cluster_id=%s, got %v", clusterID.String(), data["cluster_id"])
	}
	if data["metrics_available"] != true {
		t.Fatalf("expected metrics_available=true, got %v", data["metrics_available"])
	}
	if data["pod_count"] != 17 {
		t.Fatalf("expected pod_count=17, got %v", data["pod_count"])
	}
}

func TestHandleMetricsInvalidPayloadDoesNotPersistOrPublish(t *testing.T) {
	validator := &recordingValidator{}
	h := NewHubWithValidator(slog.Default(), validator)
	pub := &recordingPublisher{}
	h.SetPublisher(pub)

	conn := &AgentConnection{
		ClusterID: uuid.New().String(),
		Streams:   NewStreamManager(10),
		sendCh:    make(chan *protocol.Message, 10),
	}

	h.handleMessage(conn, &protocol.Message{Type: protocol.MsgMetrics, Payload: []byte("not-json")})

	if got := len(validator.SnapshotUpserts()); got != 0 {
		t.Fatalf("expected 0 health upserts, got %d", got)
	}
	if got := len(pub.Snapshot()); got != 0 {
		t.Fatalf("expected 0 publishes, got %d", got)
	}
}

func counterValue(t *testing.T, c interface{ Write(*dto.Metric) error }) float64 {
	t.Helper()
	var m dto.Metric
	if err := c.Write(&m); err != nil {
		t.Fatalf("write metric: %v", err)
	}
	if m.Counter == nil || m.Counter.Value == nil {
		t.Fatal("expected counter metric value")
	}
	return *m.Counter.Value
}
