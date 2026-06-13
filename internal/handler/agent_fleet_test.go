package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	agenttemplate "github.com/alphabravocompany/astronomer-go/deploy/agent"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

type fakeAgentFleetQuerier struct {
	clusters   []sqlc.Cluster
	active     []sqlc.AgentConnection
	history    map[uuid.UUID][]sqlc.AgentConnection
	conditions map[uuid.UUID][]sqlc.ClusterCondition
	operations map[uuid.UUID][]sqlc.AgentLifecycleOperation
	created    []sqlc.AgentLifecycleOperation
}

type fakeAgentFleetLiveRequester struct {
	responses map[string]*protocol.K8sResponsePayload
	calls     []string
}

func (f *fakeAgentFleetLiveRequester) Do(_ context.Context, _ string, method, path string, _ []byte, _ map[string]string) (*protocol.K8sResponsePayload, error) {
	f.calls = append(f.calls, method+" "+path)
	if resp, ok := f.responses[method+" "+path]; ok {
		return resp, nil
	}
	return k8sJSONResponse(http.StatusNotFound, map[string]any{"message": "not found"}), nil
}

func (f *fakeAgentFleetQuerier) CountClusters(context.Context) (int64, error) {
	return int64(len(f.clusters)), nil
}

func (f *fakeAgentFleetQuerier) GetClusterByID(_ context.Context, id uuid.UUID) (sqlc.Cluster, error) {
	for _, cluster := range f.clusters {
		if cluster.ID == id {
			return cluster, nil
		}
	}
	return sqlc.Cluster{}, errString("not found")
}

func (f *fakeAgentFleetQuerier) ListClusters(context.Context, sqlc.ListClustersParams) ([]sqlc.Cluster, error) {
	return f.clusters, nil
}

func (f *fakeAgentFleetQuerier) ListActiveConnections(context.Context) ([]sqlc.AgentConnection, error) {
	return f.active, nil
}

func (f *fakeAgentFleetQuerier) ListConnectionsByCluster(_ context.Context, arg sqlc.ListConnectionsByClusterParams) ([]sqlc.AgentConnection, error) {
	return f.history[arg.ClusterID], nil
}

func (f *fakeAgentFleetQuerier) ListClusterConditions(_ context.Context, clusterID uuid.UUID) ([]sqlc.ClusterCondition, error) {
	return f.conditions[clusterID], nil
}

func (f *fakeAgentFleetQuerier) CreateAgentLifecycleOperation(_ context.Context, arg sqlc.CreateAgentLifecycleOperationParams) (sqlc.AgentLifecycleOperation, error) {
	now := time.Date(2026, 6, 13, 12, 1, 0, 0, time.UTC)
	op := sqlc.AgentLifecycleOperation{
		ID:             uuid.New(),
		ClusterID:      arg.ClusterID,
		OperationType:  arg.OperationType,
		Status:         "pending",
		TargetVersion:  arg.TargetVersion,
		TargetImage:    arg.TargetImage,
		CurrentVersion: arg.CurrentVersion,
		Strategy:       arg.Strategy,
		OperationSpec:  arg.OperationSpec,
		RequestedBy:    arg.RequestedBy,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	f.created = append(f.created, op)
	if f.operations == nil {
		f.operations = map[uuid.UUID][]sqlc.AgentLifecycleOperation{}
	}
	f.operations[arg.ClusterID] = append([]sqlc.AgentLifecycleOperation{op}, f.operations[arg.ClusterID]...)
	return op, nil
}

func (f *fakeAgentFleetQuerier) ListAgentLifecycleOperationsByCluster(_ context.Context, arg sqlc.ListAgentLifecycleOperationsByClusterParams) ([]sqlc.AgentLifecycleOperation, error) {
	items := f.operations[arg.ClusterID]
	if arg.Offset >= int32(len(items)) {
		return []sqlc.AgentLifecycleOperation{}, nil
	}
	end := int(arg.Offset + arg.Limit)
	if end > len(items) {
		end = len(items)
	}
	return items[arg.Offset:end], nil
}

func TestAgentFleetListSummarizesConnectedDegradedDisconnectedAgents(t *testing.T) {
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	connectedID := uuid.New()
	degradedID := uuid.New()
	disconnectedID := uuid.New()
	h := NewAgentFleetHandler(&fakeAgentFleetQuerier{
		clusters: []sqlc.Cluster{
			{
				ID:                connectedID,
				Name:              "connected",
				DisplayName:       "Connected",
				Status:            "active",
				AgentVersion:      "v1.0.0",
				LastHeartbeat:     ts(now.Add(-30 * time.Second)),
				Annotations:       profileAnnotation(agenttemplate.PrivilegeProfileOperator),
				KubernetesVersion: "v1.30.1",
				NodeCount:         3,
			},
			{
				ID:            degradedID,
				Name:          "degraded",
				DisplayName:   "Degraded",
				Status:        "active",
				LastHeartbeat: ts(now.Add(-5 * time.Minute)),
				Annotations:   profileAnnotation(agenttemplate.PrivilegeProfileAdmin),
				NodeCount:     2,
			},
			{
				ID:          disconnectedID,
				Name:        "disconnected",
				DisplayName: "Disconnected",
				Status:      "disconnected",
				Annotations: profileAnnotation(agenttemplate.PrivilegeProfileViewer),
			},
		},
		active: []sqlc.AgentConnection{
			{
				ID:           uuid.New(),
				ClusterID:    connectedID,
				AgentID:      "agent-connected",
				SessionID:    "sess-connected",
				Status:       "connected",
				ConnectedAt:  now.Add(-10 * time.Minute),
				LastPing:     ts(now.Add(-20 * time.Second)),
				AgentVersion: "v1.0.0",
			},
			{
				ID:           uuid.New(),
				ClusterID:    degradedID,
				AgentID:      "agent-degraded",
				SessionID:    "sess-degraded",
				Status:       "connected",
				ConnectedAt:  now.Add(-15 * time.Minute),
				LastPing:     ts(now.Add(-4 * time.Minute)),
				AgentVersion: "v0.9.0",
			},
		},
		history: map[uuid.UUID][]sqlc.AgentConnection{
			disconnectedID: {
				{
					ID:             uuid.New(),
					ClusterID:      disconnectedID,
					AgentID:        "agent-old",
					SessionID:      "sess-old",
					Status:         "disconnected",
					ConnectedAt:    now.Add(-2 * time.Hour),
					DisconnectedAt: ts(now.Add(-30 * time.Minute)),
					AgentVersion:   "v0.8.0",
				},
			},
		},
	})
	h.now = func() time.Time { return now }

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/fleet/", nil)
	h.List(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var envelope struct {
		Data agentFleetResponse `json:"data"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got := envelope.Data
	if got.Summary.Connected != 1 || got.Summary.Degraded != 1 || got.Summary.Disconnected != 1 {
		t.Fatalf("summary = %+v, want connected/degraded/disconnected 1/1/1", got.Summary)
	}
	if len(got.Items) != 3 {
		t.Fatalf("items len = %d, want 3", len(got.Items))
	}
	if got.Items[0].AgentStatus != "connected" {
		t.Fatalf("first status = %q, want connected", got.Items[0].AgentStatus)
	}
	if got.Items[1].AgentStatus != "degraded" || len(got.Items[1].DegradedReasons) == 0 {
		t.Fatalf("second item = %+v, want degraded with reasons", got.Items[1])
	}
	if got.Items[2].AgentStatus != "disconnected" {
		t.Fatalf("third status = %q, want disconnected", got.Items[2].AgentStatus)
	}
}

func TestAgentFleetListWithoutQuerierReturns503(t *testing.T) {
	h := NewAgentFleetHandler(nil)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/fleet/", nil)
	h.List(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
}

func TestAgentFleetDiagnosticsReturnsRedactedTriagePayload(t *testing.T) {
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	clusterID := uuid.New()
	h := NewAgentFleetHandler(&fakeAgentFleetQuerier{
		clusters: []sqlc.Cluster{
			{
				ID:            clusterID,
				Name:          "prod",
				DisplayName:   "Production",
				Status:        "active",
				LastHeartbeat: ts(now.Add(-5 * time.Minute)),
				Annotations:   profileAnnotation(agenttemplate.PrivilegeProfileAdmin),
				AgentVersion:  "v0.9.0",
			},
		},
		history: map[uuid.UUID][]sqlc.AgentConnection{
			clusterID: {
				{
					ID:           uuid.New(),
					ClusterID:    clusterID,
					AgentID:      "agent-prod",
					SessionID:    "sess-prod",
					Status:       "connected",
					ConnectedAt:  now.Add(-20 * time.Minute),
					LastPing:     ts(now.Add(-4 * time.Minute)),
					AgentVersion: "v0.9.0",
					PodName:      "astronomer-agent-abc",
				},
			},
		},
		conditions: map[uuid.UUID][]sqlc.ClusterCondition{
			clusterID: {
				{
					ID:                 uuid.New(),
					ClusterID:          clusterID,
					Type:               "Connected",
					Status:             "False",
					Reason:             "HeartbeatStale",
					Message:            "heartbeat is stale",
					LastTransitionTime: now.Add(-3 * time.Minute),
					LastProbeTime:      now.Add(-1 * time.Minute),
				},
			},
		},
	})
	h.now = func() time.Time { return now }

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("cluster_id", clusterID.String())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/fleet/"+clusterID.String()+"/diagnostics/", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()
	h.Diagnostics(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var envelope struct {
		Data agentDiagnosticsResponse `json:"data"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got := envelope.Data
	if got.Agent.AgentStatus != "degraded" {
		t.Fatalf("agent status = %q, want degraded", got.Agent.AgentStatus)
	}
	if len(got.RecentConnections) != 1 || len(got.Conditions) != 1 {
		t.Fatalf("diagnostics = %+v", got)
	}
	if got.UpgradeRecommendation.Status == "" {
		t.Fatalf("missing upgrade recommendation: %+v", got.UpgradeRecommendation)
	}
	if len(got.Redactions) == 0 {
		t.Fatal("expected redaction notes")
	}
}

func TestAgentFleetDiagnosticsBundleDownloadsRedactedJSON(t *testing.T) {
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	clusterID := uuid.New()
	h := NewAgentFleetHandler(&fakeAgentFleetQuerier{
		clusters: []sqlc.Cluster{
			{
				ID:            clusterID,
				Name:          "prod",
				DisplayName:   "Production",
				Status:        "active",
				LastHeartbeat: ts(now.Add(-30 * time.Second)),
				Annotations:   profileAnnotation(agenttemplate.PrivilegeProfileOperator),
			},
		},
		history: map[uuid.UUID][]sqlc.AgentConnection{
			clusterID: {
				{
					ID:           uuid.New(),
					ClusterID:    clusterID,
					AgentID:      "agent-prod",
					SessionID:    "sess-prod",
					Status:       "connected",
					ConnectedAt:  now.Add(-20 * time.Minute),
					LastPing:     ts(now.Add(-20 * time.Second)),
					AgentVersion: "v1.0.0",
				},
			},
		},
	})
	h.now = func() time.Time { return now }

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("cluster_id", clusterID.String())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/fleet/"+clusterID.String()+"/diagnostics/bundle/", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()
	h.DiagnosticsBundle(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Header().Get("Content-Disposition"), "astronomer-agent-diagnostics-"+clusterID.String()+".json") {
		t.Fatalf("content disposition = %q", rr.Header().Get("Content-Disposition"))
	}
	var bundle agentDiagnosticsBundleResponse
	if err := json.NewDecoder(rr.Body).Decode(&bundle); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if bundle.Version != "v1" || bundle.ClusterID != clusterID.String() {
		t.Fatalf("bundle = %+v", bundle)
	}
	if len(bundle.Diagnostics.Redactions) == 0 || strings.Contains(rr.Body.String(), "registration token") {
		t.Fatalf("expected redacted diagnostics bundle: %s", rr.Body.String())
	}
}

func TestAgentFleetDiagnosticsIncludesLiveAgentSnapshot(t *testing.T) {
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	clusterID := uuid.New()
	h := NewAgentFleetHandler(&fakeAgentFleetQuerier{
		clusters: []sqlc.Cluster{
			{
				ID:          clusterID,
				Name:        "prod",
				DisplayName: "Production",
				Status:      "active",
				Annotations: profileAnnotation(agenttemplate.PrivilegeProfileOperator),
			},
		},
		history: map[uuid.UUID][]sqlc.AgentConnection{
			clusterID: {
				{
					ID:           uuid.New(),
					ClusterID:    clusterID,
					AgentID:      "agent-prod",
					SessionID:    "sess-prod",
					Status:       "connected",
					ConnectedAt:  now.Add(-20 * time.Minute),
					LastPing:     ts(now.Add(-20 * time.Second)),
					AgentVersion: "v1.0.0",
				},
			},
		},
	})
	h.now = func() time.Time { return now }
	selector := url.QueryEscape("app.kubernetes.io/name=astronomer-agent,app.kubernetes.io/component=agent")
	requester := &fakeAgentFleetLiveRequester{responses: map[string]*protocol.K8sResponsePayload{
		"GET /apis/apps/v1/namespaces/astronomer-system/deployments/astronomer-agent": k8sJSONResponse(http.StatusOK, map[string]any{
			"metadata": map[string]any{"name": "astronomer-agent", "namespace": "astronomer-system", "generation": 2},
			"spec":     map[string]any{"replicas": 1},
			"status":   map[string]any{"readyReplicas": 1, "updatedReplicas": 1},
		}),
		"GET /api/v1/namespaces/astronomer-system/pods?labelSelector=" + selector: k8sJSONResponse(http.StatusOK, map[string]any{
			"items": []map[string]any{
				{
					"metadata": map[string]any{"name": "astronomer-agent-abc", "namespace": "astronomer-system"},
					"spec": map[string]any{
						"nodeName":   "node-1",
						"containers": []map[string]any{{"image": "example.com/astronomer-agent:v1.0.0"}},
					},
					"status": map[string]any{
						"phase":             "Running",
						"containerStatuses": []map[string]any{{"ready": true, "restartCount": 1}},
					},
				},
			},
		}),
		"GET /api/v1/namespaces/astronomer-system/pods/astronomer-agent-abc/log?tailLines=200&timestamps=true": k8sTextResponse(http.StatusOK, "connected\nbearer token abc\nready"),
		"GET /api/v1/namespaces/astronomer-system/events?fieldSelector=" + url.QueryEscape("involvedObject.name=astronomer-agent"): k8sJSONResponse(http.StatusOK, map[string]any{
			"items": []map[string]any{{"type": "Normal", "reason": "Pulled", "message": "image pulled", "lastTimestamp": now.Format(time.RFC3339)}},
		}),
		"GET /version": k8sJSONResponse(http.StatusOK, map[string]any{"gitVersion": "v1.30.2"}),
		"GET /apis": k8sJSONResponse(http.StatusOK, map[string]any{
			"groups": []map[string]any{{"name": "apps"}, {"name": "batch"}},
		}),
	}}
	h.SetK8sRequester(requester)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("cluster_id", clusterID.String())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/fleet/"+clusterID.String()+"/diagnostics/", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()
	h.Diagnostics(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var envelope struct {
		Data agentDiagnosticsResponse `json:"data"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	live := envelope.Data.Live
	if live == nil || len(live.Pods) != 1 || live.Deployment["name"] != "astronomer-agent" {
		t.Fatalf("live diagnostics = %+v", live)
	}
	if len(live.Logs) != 1 || live.Logs[0].Lines[1] != "[redacted sensitive log line]" {
		t.Fatalf("logs not redacted: %+v", live.Logs)
	}
	if got, _ := live.Discovery["apis"].(map[string]any)["group_count"].(float64); got != 2 {
		t.Fatalf("discovery = %+v", live.Discovery)
	}
}

func TestAgentFleetUpgradePlanReadyForConnectedRemoteAgent(t *testing.T) {
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	clusterID := uuid.New()
	h := NewAgentFleetHandler(&fakeAgentFleetQuerier{
		clusters: []sqlc.Cluster{
			{
				ID:          clusterID,
				Name:        "prod",
				DisplayName: "Production",
				Status:      "active",
				Annotations: profileAnnotation(agenttemplate.PrivilegeProfileOperator),
			},
		},
		history: map[uuid.UUID][]sqlc.AgentConnection{
			clusterID: {
				{
					ID:           uuid.New(),
					ClusterID:    clusterID,
					AgentID:      "agent-prod",
					SessionID:    "sess-prod",
					Status:       "connected",
					ConnectedAt:  now.Add(-20 * time.Minute),
					LastPing:     ts(now.Add(-20 * time.Second)),
					AgentVersion: "v1.0.0",
				},
			},
		},
	})
	h.now = func() time.Time { return now }
	h.SetAgentUpgradeTarget("example.com/astronomer-agent", "v1.2.3")

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("cluster_id", clusterID.String())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/fleet/"+clusterID.String()+"/upgrade-plan/", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()
	h.UpgradePlan(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var envelope struct {
		Data agentUpgradePlanResponse `json:"data"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got := envelope.Data
	if !got.Ready || got.TargetImage != "example.com/astronomer-agent:v1.2.3" {
		t.Fatalf("upgrade plan = %+v", got)
	}
	if len(got.Steps) == 0 || len(got.Validation) == 0 || len(got.Rollback) == 0 {
		t.Fatalf("expected actionable rollout plan: %+v", got)
	}
}

func TestAgentFleetUpgradePlanBlocksDisconnectedLocalAgent(t *testing.T) {
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	clusterID := uuid.New()
	h := NewAgentFleetHandler(&fakeAgentFleetQuerier{
		clusters: []sqlc.Cluster{
			{
				ID:      clusterID,
				Name:    "local",
				Status:  "active",
				IsLocal: true,
			},
		},
		history: map[uuid.UUID][]sqlc.AgentConnection{},
	})
	h.now = func() time.Time { return now }

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("cluster_id", clusterID.String())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/fleet/"+clusterID.String()+"/upgrade-plan/", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()
	h.UpgradePlan(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var envelope struct {
		Data agentUpgradePlanResponse `json:"data"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if envelope.Data.Ready || len(envelope.Data.Blockers) < 2 {
		t.Fatalf("expected blocked plan, got %+v", envelope.Data)
	}
}

func TestAgentFleetUpgradeQueuesLifecycleOperation(t *testing.T) {
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	clusterID := uuid.New()
	fake := &fakeAgentFleetQuerier{
		clusters: []sqlc.Cluster{
			{
				ID:          clusterID,
				Name:        "prod",
				DisplayName: "Production",
				Status:      "active",
				Annotations: profileAnnotation(agenttemplate.PrivilegeProfileOperator),
			},
		},
		history: map[uuid.UUID][]sqlc.AgentConnection{
			clusterID: {
				{
					ID:           uuid.New(),
					ClusterID:    clusterID,
					AgentID:      "agent-prod",
					SessionID:    "sess-prod",
					Status:       "connected",
					ConnectedAt:  now.Add(-20 * time.Minute),
					LastPing:     ts(now.Add(-20 * time.Second)),
					AgentVersion: "v1.0.0",
				},
			},
		},
	}
	h := NewAgentFleetHandler(fake)
	h.now = func() time.Time { return now }
	h.SetAgentUpgradeTarget("example.com/astronomer-agent", "v1.2.3")

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("cluster_id", clusterID.String())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/fleet/"+clusterID.String()+"/upgrade/", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()
	h.Upgrade(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if len(fake.created) != 1 {
		t.Fatalf("created operations = %d, want 1", len(fake.created))
	}
	if fake.created[0].OperationType != "agent_upgrade" || fake.created[0].TargetImage != "example.com/astronomer-agent:v1.2.3" || fake.created[0].Strategy != "agent_self_rollout" {
		t.Fatalf("created operation = %+v", fake.created[0])
	}
	var envelope struct {
		Data agentUpgradeOperationResponse `json:"data"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if envelope.Data.Operation.Status != "pending" || !envelope.Data.Plan.Ready {
		t.Fatalf("upgrade response = %+v", envelope.Data)
	}
}

func TestAgentFleetUpgradeDoesNotQueueBlockedPlan(t *testing.T) {
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	clusterID := uuid.New()
	fake := &fakeAgentFleetQuerier{
		clusters: []sqlc.Cluster{
			{
				ID:      clusterID,
				Name:    "local",
				Status:  "active",
				IsLocal: true,
			},
		},
		history: map[uuid.UUID][]sqlc.AgentConnection{},
	}
	h := NewAgentFleetHandler(fake)
	h.now = func() time.Time { return now }

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("cluster_id", clusterID.String())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/fleet/"+clusterID.String()+"/upgrade/", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()
	h.Upgrade(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if len(fake.created) != 0 {
		t.Fatalf("blocked upgrade created operations: %+v", fake.created)
	}
}

func TestAgentFleetOperationsListsLifecycleHistory(t *testing.T) {
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	clusterID := uuid.New()
	h := NewAgentFleetHandler(&fakeAgentFleetQuerier{
		clusters: []sqlc.Cluster{{ID: clusterID, Name: "prod", Status: "active"}},
		operations: map[uuid.UUID][]sqlc.AgentLifecycleOperation{
			clusterID: {
				{
					ID:            uuid.New(),
					ClusterID:     clusterID,
					OperationType: "agent_upgrade",
					Status:        "pending",
					TargetVersion: "v1.2.3",
					TargetImage:   "example.com/astronomer-agent:v1.2.3",
					Strategy:      "manifest_rollout",
					CreatedAt:     now,
					UpdatedAt:     now,
				},
			},
		},
	})

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("cluster_id", clusterID.String())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/fleet/"+clusterID.String()+"/operations/", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()
	h.Operations(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var envelope struct {
		Data agentLifecycleOperationsResponse `json:"data"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(envelope.Data.Items) != 1 || envelope.Data.Items[0].OperationType != "agent_upgrade" {
		t.Fatalf("operations response = %+v", envelope.Data)
	}
}

func ts(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

func profileAnnotation(profile string) json.RawMessage {
	data, _ := json.Marshal(map[string]string{
		agenttemplate.PrivilegeProfileAnnotation: profile,
	})
	return data
}

func k8sTextResponse(status int, body string) *protocol.K8sResponsePayload {
	return &protocol.K8sResponsePayload{
		StatusCode: status,
		Headers:    map[string]string{"Content-Type": "text/plain"},
		Body:       base64.StdEncoding.EncodeToString([]byte(body)),
	}
}
