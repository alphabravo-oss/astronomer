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
	"github.com/alphabravocompany/astronomer-go/internal/agentcompat"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/redaction"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"github.com/alphabravocompany/astronomer-go/pkg/version"
)

type fakeAgentFleetQuerier struct {
	clusters   []sqlc.Cluster
	active     []sqlc.AgentConnection
	history    map[uuid.UUID][]sqlc.AgentConnection
	conditions map[uuid.UUID][]sqlc.ClusterCondition
	argocd     map[uuid.UUID][]sqlc.ArgocdManagedCluster
	operations map[uuid.UUID][]sqlc.AgentLifecycleOperation
	created    []sqlc.AgentLifecycleOperation
	idempotent []sqlc.CreateAgentLifecycleOperationIdempotentParams
	users      map[uuid.UUID]sqlc.User
}

func (f *fakeAgentFleetQuerier) GetUserByID(_ context.Context, id uuid.UUID) (sqlc.User, error) {
	if u, ok := f.users[id]; ok {
		return u, nil
	}
	return sqlc.User{}, errString("user not found")
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

func (f *fakeAgentFleetQuerier) ListArgoCDManagedClustersByCluster(_ context.Context, clusterID uuid.UUID) ([]sqlc.ArgocdManagedCluster, error) {
	return f.argocd[clusterID], nil
}

func (f *fakeAgentFleetQuerier) CreateAgentLifecycleOperation(_ context.Context, arg sqlc.CreateAgentLifecycleOperationParams) (sqlc.AgentLifecycleOperation, error) {
	return f.recordAgentLifecycleOperation(sqlc.AgentLifecycleOperation{
		ClusterID:      arg.ClusterID,
		OperationType:  arg.OperationType,
		Status:         "pending",
		TargetVersion:  arg.TargetVersion,
		TargetImage:    arg.TargetImage,
		CurrentVersion: arg.CurrentVersion,
		Strategy:       arg.Strategy,
		OperationSpec:  arg.OperationSpec,
		RequestedBy:    arg.RequestedBy,
	})
}

func (f *fakeAgentFleetQuerier) CreateAgentLifecycleOperationIdempotent(_ context.Context, arg sqlc.CreateAgentLifecycleOperationIdempotentParams) (sqlc.AgentLifecycleOperation, error) {
	f.idempotent = append(f.idempotent, arg)
	return f.recordAgentLifecycleOperation(sqlc.AgentLifecycleOperation{
		ClusterID:      arg.ClusterID,
		OperationType:  arg.OperationType,
		Status:         "pending",
		TargetVersion:  arg.TargetVersion,
		TargetImage:    arg.TargetImage,
		CurrentVersion: arg.CurrentVersion,
		Strategy:       arg.Strategy,
		OperationSpec:  arg.OperationSpec,
		RequestedBy:    arg.RequestedBy,
	})
}

func (f *fakeAgentFleetQuerier) recordAgentLifecycleOperation(op sqlc.AgentLifecycleOperation) (sqlc.AgentLifecycleOperation, error) {
	now := time.Date(2026, 6, 13, 12, 1, 0, 0, time.UTC)
	op.ID = uuid.New()
	op.CreatedAt = now
	op.UpdatedAt = now
	f.created = append(f.created, op)
	if f.operations == nil {
		f.operations = map[uuid.UUID][]sqlc.AgentLifecycleOperation{}
	}
	f.operations[op.ClusterID] = append([]sqlc.AgentLifecycleOperation{op}, f.operations[op.ClusterID]...)
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
				AgentVersion: "v0.1.0", // deprecated tier (below the v0.2.0 supported floor)
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
					AgentVersion:   "v0.0.5", // below the v0.1.0 compatible floor → blocked
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
	if got.Summary.ServerVersion != version.Version {
		t.Fatalf("server version = %q, want %q", got.Summary.ServerVersion, version.Version)
	}
	if got.Summary.MinimumSupportedAgentVersion != agentcompat.MinimumSupportedVersion {
		t.Fatalf("minimum supported agent = %q, want %q", got.Summary.MinimumSupportedAgentVersion, agentcompat.MinimumSupportedVersion)
	}
	if got.Summary.MinimumCompatibleAgentVersion != agentcompat.MinimumCompatibleVersion {
		t.Fatalf("minimum compatible agent = %q, want %q", got.Summary.MinimumCompatibleAgentVersion, agentcompat.MinimumCompatibleVersion)
	}
	if len(got.Items) != 3 {
		t.Fatalf("items len = %d, want 3", len(got.Items))
	}
	if got.Items[0].AgentStatus != "connected" {
		t.Fatalf("first status = %q, want connected", got.Items[0].AgentStatus)
	}
	if got.Items[0].CompatibilityStatus != "supported" {
		t.Fatalf("first compatibility = %q, want supported", got.Items[0].CompatibilityStatus)
	}
	if got.Items[1].AgentStatus != "degraded" || len(got.Items[1].DegradedReasons) == 0 {
		t.Fatalf("second item = %+v, want degraded with reasons", got.Items[1])
	}
	if got.Items[1].CompatibilityStatus != "deprecated" {
		t.Fatalf("second compatibility = %q, want deprecated", got.Items[1].CompatibilityStatus)
	}
	if got.Items[2].AgentStatus != "disconnected" {
		t.Fatalf("third status = %q, want disconnected", got.Items[2].AgentStatus)
	}
	if got.Items[2].OfflineBehavior == nil {
		t.Fatalf("third offline behavior is nil")
	}
	if got.Items[2].OfflineBehavior.State != "offline" || !got.Items[2].OfflineBehavior.Stale {
		t.Fatalf("third offline behavior = %+v, want stale offline", got.Items[2].OfflineBehavior)
	}
	if got.Items[2].OfflineBehavior.LastKnownAt == nil || *got.Items[2].OfflineBehavior.LastKnownAt != now.Add(-30*time.Minute).UTC().Format(time.RFC3339) {
		t.Fatalf("third last known = %v", got.Items[2].OfflineBehavior.LastKnownAt)
	}
	if !containsString(got.Items[2].OfflineBehavior.BlockedOperations, "kubernetes_proxy") ||
		!containsString(got.Items[2].OfflineBehavior.PermittedQueuedOperations, "argocd_registration_repair") {
		t.Fatalf("third offline operations = %+v", got.Items[2].OfflineBehavior)
	}
	if got.Summary.Compatibility["supported"] != 1 || got.Summary.Compatibility["deprecated"] != 1 || got.Summary.Compatibility["blocked"] != 1 {
		t.Fatalf("compatibility summary = %+v, want supported=1 deprecated=1 blocked=1", got.Summary.Compatibility)
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

func TestAgentFleetDiagnosticsBundleStrictlyRedactsLiveSensitiveValues(t *testing.T) {
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
	h.SetK8sRequester(&fakeAgentFleetLiveRequester{responses: map[string]*protocol.K8sResponsePayload{
		"GET /apis/apps/v1/namespaces/astronomer-system/deployments/astronomer-agent": k8sJSONResponse(http.StatusOK, map[string]any{
			"metadata": map[string]any{"name": "astronomer-agent", "namespace": "astronomer-system"},
			"status": map[string]any{
				"conditions": []map[string]any{{
					"type":    "Available",
					"message": "authorization=deployment-header-token password=deployment-password",
				}},
			},
		}),
		"GET /api/v1/namespaces/astronomer-system/pods?labelSelector=" + selector: k8sJSONResponse(http.StatusOK, map[string]any{
			"items": []map[string]any{{
				"metadata": map[string]any{"name": "astronomer-agent-abc", "namespace": "astronomer-system"},
				"spec":     map[string]any{"containers": []map[string]any{{"image": "example.com/astronomer-agent:v1.0.0"}}},
				"status":   map[string]any{"phase": "Running", "containerStatuses": []map[string]any{{"ready": true}}},
			}},
		}),
		"GET /api/v1/namespaces/astronomer-system/pods/astronomer-agent-abc/log?tailLines=200&timestamps=true": k8sTextResponse(http.StatusOK, "ready\nAuthorization: log-header-token\npostgres://user:db-password@example.com/db"),
		"GET /api/v1/namespaces/astronomer-system/events?fieldSelector=" + url.QueryEscape("involvedObject.name=astronomer-agent"): k8sJSONResponse(http.StatusOK, map[string]any{
			"items": []map[string]any{{
				"type":          "Warning",
				"reason":        "Failed",
				"message":       "Bearer event-bearer-token client_secret=event-client-secret",
				"lastTimestamp": now.Format(time.RFC3339),
			}},
		}),
		"GET /version": k8sJSONResponseWithHeaders(http.StatusOK, map[string]any{
			"gitVersion": "v1.30.2",
			"token":      "version-token",
		}, map[string]string{"Date": now.Format(http.TimeFormat)}),
		"GET /apis": k8sJSONResponse(http.StatusOK, map[string]any{"groups": []map[string]any{{"name": "apps"}}}),
		"POST /apis/authorization.k8s.io/v1/selfsubjectaccessreviews": k8sJSONResponse(http.StatusCreated, map[string]any{
			"status": map[string]any{"allowed": true},
		}),
		"GET /readyz": k8sTextResponse(http.StatusOK, "ok"),
	}})

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("cluster_id", clusterID.String())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/fleet/"+clusterID.String()+"/diagnostics/bundle/", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()
	h.DiagnosticsBundle(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, leaked := range []string{
		"deployment-header-token",
		"deployment-password",
		"log-header-token",
		"db-password",
		"event-bearer-token",
		"event-client-secret",
		"version-token",
	} {
		if strings.Contains(body, leaked) {
			t.Fatalf("diagnostics bundle leaked %q: %s", leaked, body)
		}
	}
	if !strings.Contains(body, "[redacted]") && !strings.Contains(body, "[redacted sensitive log line]") {
		t.Fatalf("diagnostics bundle did not include redaction markers: %s", body)
	}
}

func TestRedactAgentDiagnosticsPayloadRedactsKubeconfigAndPrivateKeys(t *testing.T) {
	payload := map[string]any{
		"kubeconfig": "apiVersion: v1\nclusters: []\nusers: []\n",
		"message":    "-----BEGIN RSA PRIVATE KEY-----\nabc\n-----END RSA PRIVATE KEY-----",
		"url":        "postgres://user:password@example.com/db",
	}
	redacted, ok := redaction.Payload(payload).(map[string]any)
	if !ok {
		t.Fatalf("redacted payload type = %T", redacted)
	}
	if redacted["kubeconfig"] != "[redacted]" {
		t.Fatalf("kubeconfig = %v", redacted["kubeconfig"])
	}
	if redacted["message"] != "[redacted private key]" {
		t.Fatalf("message = %v", redacted["message"])
	}
	if strings.Contains(redacted["url"].(string), "password") {
		t.Fatalf("url not redacted: %v", redacted["url"])
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
		"GET /version": k8sJSONResponseWithHeaders(http.StatusOK, map[string]any{"gitVersion": "v1.30.2"}, map[string]string{"Date": now.Add(-1 * time.Second).Format(http.TimeFormat)}),
		"GET /apis": k8sJSONResponse(http.StatusOK, map[string]any{
			"groups": []map[string]any{{"name": "apps"}, {"name": "batch"}},
		}),
		"POST /apis/authorization.k8s.io/v1/selfsubjectaccessreviews": k8sJSONResponse(http.StatusCreated, map[string]any{
			"status": map[string]any{"allowed": true},
		}),
		"GET /readyz": k8sTextResponse(http.StatusOK, "ok"),
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
	checks := selfTestCheckStatuses(live.Checks)
	for _, name := range []string{"clock_skew", "rbac_self_check", "network_readyz"} {
		if checks[name] != "passed" {
			t.Fatalf("live check %s = %q, want passed; checks=%+v", name, checks[name], live.Checks)
		}
	}
}

func TestAgentFleetSelfTestPassesForHealthyConnectedAgent(t *testing.T) {
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
				AgentVersion:  "v1.0.0",
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
		conditions: map[uuid.UUID][]sqlc.ClusterCondition{
			clusterID: {
				{
					ID:                 uuid.New(),
					ClusterID:          clusterID,
					Type:               "Connected",
					Status:             "True",
					Reason:             "FreshHeartbeat",
					Message:            "heartbeat is fresh",
					LastTransitionTime: now.Add(-1 * time.Minute),
					LastProbeTime:      now.Add(-30 * time.Second),
				},
			},
		},
		argocd: map[uuid.UUID][]sqlc.ArgocdManagedCluster{
			clusterID: {
				{
					ID:                uuid.New(),
					ArgocdInstanceID:  uuid.New(),
					ClusterID:         clusterID,
					ClusterSecretName: "prod",
					ServerUrl:         "https://astronomer.example/api/v1/internal/argocd/clusters/" + clusterID.String() + "/k8s",
					UpdatedAt:         now.Add(-1 * time.Minute),
				},
			},
		},
	})
	h.now = func() time.Time { return now }
	selector := url.QueryEscape("app.kubernetes.io/name=astronomer-agent,app.kubernetes.io/component=agent")
	h.SetK8sRequester(&fakeAgentFleetLiveRequester{responses: map[string]*protocol.K8sResponsePayload{
		"GET /apis/apps/v1/namespaces/astronomer-system/deployments/astronomer-agent": k8sJSONResponse(http.StatusOK, map[string]any{
			"metadata": map[string]any{"name": "astronomer-agent", "namespace": "astronomer-system"},
			"status":   map[string]any{"readyReplicas": 1, "updatedReplicas": 1},
		}),
		"GET /api/v1/namespaces/astronomer-system/pods?labelSelector=" + selector:                                                  k8sJSONResponse(http.StatusOK, map[string]any{"items": []map[string]any{}}),
		"GET /api/v1/namespaces/astronomer-system/events?fieldSelector=" + url.QueryEscape("involvedObject.name=astronomer-agent"): k8sJSONResponse(http.StatusOK, map[string]any{"items": []map[string]any{}}),
		"GET /version": k8sJSONResponseWithHeaders(http.StatusOK, map[string]any{"gitVersion": "v1.30.2"}, map[string]string{"Date": now.Add(-1 * time.Second).Format(http.TimeFormat)}),
		"GET /apis":    k8sJSONResponse(http.StatusOK, map[string]any{"groups": []map[string]any{{"name": "apps"}}}),
		"POST /apis/authorization.k8s.io/v1/selfsubjectaccessreviews": k8sJSONResponse(http.StatusCreated, map[string]any{
			"status": map[string]any{"allowed": true},
		}),
		"GET /readyz": k8sTextResponse(http.StatusOK, "ok"),
	}})

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("cluster_id", clusterID.String())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/fleet/"+clusterID.String()+"/self-test/", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()
	h.SelfTest(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var envelope struct {
		Data agentSelfTestResponse `json:"data"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if envelope.Data.Status != "passed" {
		t.Fatalf("self-test status = %q, checks=%+v", envelope.Data.Status, envelope.Data.Checks)
	}
	statuses := selfTestCheckStatuses(envelope.Data.Checks)
	for _, name := range []string{"agent_connection", "heartbeat_freshness", "ping_freshness", "privilege_profile", "compatibility", "argocd_registration", "live_diagnostics", "cluster_conditions"} {
		if statuses[name] != "passed" {
			t.Fatalf("check %s = %q, want passed; checks=%+v", name, statuses[name], envelope.Data.Checks)
		}
	}
}

func TestAgentFleetSelfTestFailsForDisconnectedBlockedAgent(t *testing.T) {
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	clusterID := uuid.New()
	h := NewAgentFleetHandler(&fakeAgentFleetQuerier{
		clusters: []sqlc.Cluster{
			{
				ID:           clusterID,
				Name:         "legacy",
				DisplayName:  "Legacy",
				Status:       "disconnected",
				Annotations:  profileAnnotation(agenttemplate.PrivilegeProfileViewer),
				AgentVersion: "v0.0.5", // below the v0.1.0 compatible floor → blocked
			},
		},
		history: map[uuid.UUID][]sqlc.AgentConnection{
			clusterID: {
				{
					ID:             uuid.New(),
					ClusterID:      clusterID,
					AgentID:        "agent-legacy",
					SessionID:      "sess-legacy",
					Status:         "disconnected",
					ConnectedAt:    now.Add(-2 * time.Hour),
					DisconnectedAt: ts(now.Add(-30 * time.Minute)),
					AgentVersion:   "v0.0.5", // below the v0.1.0 compatible floor → blocked
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
					Reason:             "AgentDisconnected",
					Message:            "agent is disconnected",
					LastTransitionTime: now.Add(-30 * time.Minute),
					LastProbeTime:      now.Add(-30 * time.Minute),
				},
			},
		},
	})
	h.now = func() time.Time { return now }

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("cluster_id", clusterID.String())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/fleet/"+clusterID.String()+"/self-test/", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()
	h.SelfTest(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var envelope struct {
		Data agentSelfTestResponse `json:"data"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if envelope.Data.Status != "failed" {
		t.Fatalf("self-test status = %q, checks=%+v", envelope.Data.Status, envelope.Data.Checks)
	}
	statuses := selfTestCheckStatuses(envelope.Data.Checks)
	for _, name := range []string{"agent_connection", "heartbeat_freshness", "ping_freshness", "compatibility", "argocd_registration", "live_diagnostics", "cluster_conditions"} {
		if statuses[name] != "failed" {
			t.Fatalf("check %s = %q, want failed; checks=%+v", name, statuses[name], envelope.Data.Checks)
		}
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
	if got.BatchSize != 1 || got.MaxUnavailable != 1 || got.RollbackImage != "example.com/astronomer-agent:v1.0.0" {
		t.Fatalf("rollout defaults = %+v", got)
	}
	if len(got.CanaryClusterIDs) != 1 || got.CanaryClusterIDs[0] != clusterID.String() {
		t.Fatalf("canary defaults = %+v", got.CanaryClusterIDs)
	}
	if len(got.PreflightChecks) == 0 || len(got.PostUpgradeHealthChecks) == 0 || len(got.Steps) == 0 || len(got.Validation) == 0 || len(got.Rollback) == 0 {
		t.Fatalf("expected actionable rollout plan: %+v", got)
	}
}

func TestAgentFleetUpgradePlanAcceptsRolloutControls(t *testing.T) {
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

	body := `{
		"target_version":"v1.3.0",
		"target_image":"registry.example/astronomer-agent:v1.3.0",
		"strategy":"canary_batches",
		"canary_cluster_ids":["` + clusterID.String() + `","` + clusterID.String() + `","canary-b"],
		"batch_size":5,
		"max_unavailable":2,
		"rollback_image":"registry.example/astronomer-agent:v1.0.0"
	}`
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("cluster_id", clusterID.String())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/fleet/"+clusterID.String()+"/upgrade-plan/", strings.NewReader(body))
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
	if !got.Ready || got.BatchSize != 5 || got.MaxUnavailable != 2 || got.Strategy != "canary_batches" {
		t.Fatalf("rollout controls = %+v", got)
	}
	if got.RollbackImage != "registry.example/astronomer-agent:v1.0.0" {
		t.Fatalf("rollback image = %q", got.RollbackImage)
	}
	if len(got.CanaryClusterIDs) != 2 || got.CanaryClusterIDs[0] != clusterID.String() || got.CanaryClusterIDs[1] != "canary-b" {
		t.Fatalf("canary ids = %+v", got.CanaryClusterIDs)
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
	req.Header.Set("Idempotency-Key", "agent-upgrade-retry-1")
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
	if len(fake.idempotent) != 1 {
		t.Fatalf("idempotent operation calls = %d, want 1", len(fake.idempotent))
	}
	idem := fake.idempotent[0]
	if idem.Scope != "agent_lifecycle:user:anonymous:POST:/api/v1/agents/fleet/"+clusterID.String()+"/upgrade/" || idem.IdempotencyKey != "agent-upgrade-retry-1" {
		t.Fatalf("idempotency params = %+v", idem)
	}
	if idem.ClusterID != clusterID || idem.OperationType != "agent_upgrade" || idem.TargetImage != "example.com/astronomer-agent:v1.2.3" {
		t.Fatalf("idempotent operation payload = %+v", idem)
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

func selfTestCheckStatuses(checks []agentSelfTestCheck) map[string]string {
	out := make(map[string]string, len(checks))
	for _, check := range checks {
		out[check.Name] = check.Status
	}
	return out
}

func k8sJSONResponseWithHeaders(status int, payload any, headers map[string]string) *protocol.K8sResponsePayload {
	resp := k8sJSONResponse(status, payload)
	for key, value := range headers {
		resp.Headers[key] = value
	}
	return resp
}

func k8sTextResponse(status int, body string) *protocol.K8sResponsePayload {
	return &protocol.K8sResponsePayload{
		StatusCode: status,
		Headers:    map[string]string{"Content-Type": "text/plain"},
		Body:       base64.StdEncoding.EncodeToString([]byte(body)),
	}
}

// TestAgentPrivilegeProfileFromAnnotationsFailsClosed is the negative test for
// finding C2 in the fleet handler: absent/unparseable/unknown annotations must
// resolve to the read-only viewer profile, never cluster-admin.
func TestAgentPrivilegeProfileFromAnnotationsDefaultsAndFailsClosed(t *testing.T) {
	// Unspecified (empty / unparseable / absent key) -> least-privilege viewer default.
	for name, raw := range map[string]json.RawMessage{
		"empty":       nil,
		"unparseable": json.RawMessage(`oops`),
		"absent":      json.RawMessage(`{}`),
	} {
		if got := agentPrivilegeProfileFromAnnotations(raw); got != agenttemplate.PrivilegeProfileViewer {
			t.Fatalf("agentPrivilegeProfileFromAnnotations(%s) = %q, want %q (unspecified -> viewer)", name, got, agenttemplate.PrivilegeProfileViewer)
		}
	}
	// Explicit but unknown (typo) -> fail closed to viewer.
	unknown := json.RawMessage(`{"astronomer.io/agent-privilege-profile":"root"}`)
	if got := agentPrivilegeProfileFromAnnotations(unknown); got != agenttemplate.PrivilegeProfileViewer {
		t.Fatalf("agentPrivilegeProfileFromAnnotations(unknown) = %q, want %q (fail closed)", got, agenttemplate.PrivilegeProfileViewer)
	}
}

// TestAgentPrivilegeProfileFromAnnotationsPreservesExplicit proves explicit
// profiles still resolve in the fleet handler after the C2 fix.
func TestAgentPrivilegeProfileFromAnnotationsPreservesExplicit(t *testing.T) {
	cases := map[string]string{
		"admin":    agenttemplate.PrivilegeProfileAdmin,
		"operator": agenttemplate.PrivilegeProfileOperator,
		"viewer":   agenttemplate.PrivilegeProfileViewer,
	}
	for annotation, want := range cases {
		raw := json.RawMessage(`{"astronomer.io/agent-privilege-profile":"` + annotation + `"}`)
		if got := agentPrivilegeProfileFromAnnotations(raw); got != want {
			t.Fatalf("agentPrivilegeProfileFromAnnotations(%q) = %q, want %q", annotation, got, want)
		}
	}
}

// TestAgentPrivilegeProfileSelfTestPasses verifies the fleet self-test treats
// the supported profiles — including the least-privilege viewer default — as
// passing, and only flags genuinely uncertain postures (custom RBAC).
func TestAgentPrivilegeProfileSelfTestPasses(t *testing.T) {
	for _, profile := range []string{
		agenttemplate.PrivilegeProfileAdmin,
		agenttemplate.PrivilegeProfileViewer,
		agenttemplate.PrivilegeProfileOperator,
		"", // unspecified -> viewer default
	} {
		c := agentPrivilegeProfileSelfTestCheck(agentFleetItem{PrivilegeProfile: profile})
		if c.Status != "passed" {
			t.Fatalf("profile %q should pass self-test, got %q: %s", profile, c.Status, c.Message)
		}
	}
	// Custom RBAC still warns (needs live verification).
	custom := agentPrivilegeProfileSelfTestCheck(agentFleetItem{PrivilegeProfile: agenttemplate.PrivilegeProfileCustom})
	if custom.Status != "warning" {
		t.Fatalf("custom profile should warn, got %q: %s", custom.Status, custom.Message)
	}
}
