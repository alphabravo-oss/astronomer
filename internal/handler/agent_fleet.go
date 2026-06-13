package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	agenttemplate "github.com/alphabravocompany/astronomer-go/deploy/agent"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type AgentFleetQuerier interface {
	CountClusters(ctx context.Context) (int64, error)
	GetClusterByID(ctx context.Context, id uuid.UUID) (sqlc.Cluster, error)
	ListClusters(ctx context.Context, arg sqlc.ListClustersParams) ([]sqlc.Cluster, error)
	ListActiveConnections(ctx context.Context) ([]sqlc.AgentConnection, error)
	ListConnectionsByCluster(ctx context.Context, arg sqlc.ListConnectionsByClusterParams) ([]sqlc.AgentConnection, error)
	ListClusterConditions(ctx context.Context, clusterID uuid.UUID) ([]sqlc.ClusterCondition, error)
	CreateAgentLifecycleOperation(ctx context.Context, arg sqlc.CreateAgentLifecycleOperationParams) (sqlc.AgentLifecycleOperation, error)
	ListAgentLifecycleOperationsByCluster(ctx context.Context, arg sqlc.ListAgentLifecycleOperationsByClusterParams) ([]sqlc.AgentLifecycleOperation, error)
}

type AgentFleetHandler struct {
	queries                    AgentFleetQuerier
	now                        func() time.Time
	agentImageRepository       string
	agentImageTag              string
	agentUpgradeDefaultProfile string
	requester                  K8sRequester
}

func NewAgentFleetHandler(queries AgentFleetQuerier) *AgentFleetHandler {
	return &AgentFleetHandler{
		queries:                    queries,
		now:                        time.Now,
		agentUpgradeDefaultProfile: agenttemplate.PrivilegeProfileOperator,
	}
}

func (h *AgentFleetHandler) SetAgentUpgradeTarget(repository, tag string) {
	if h == nil {
		return
	}
	h.agentImageRepository = strings.TrimSpace(repository)
	h.agentImageTag = strings.TrimSpace(tag)
}

func (h *AgentFleetHandler) SetK8sRequester(requester K8sRequester) {
	if h == nil {
		return
	}
	h.requester = requester
}

type agentFleetResponse struct {
	Summary agentFleetSummary `json:"summary"`
	Items   []agentFleetItem  `json:"items"`
	Limit   int32             `json:"limit"`
	Offset  int32             `json:"offset"`
}

type agentFleetSummary struct {
	TotalClusters int64          `json:"total_clusters"`
	Connected     int            `json:"connected"`
	Degraded      int            `json:"degraded"`
	Disconnected  int            `json:"disconnected"`
	Versions      map[string]int `json:"versions"`
	Profiles      map[string]int `json:"profiles"`
	Statuses      map[string]int `json:"statuses"`
	GeneratedAt   string         `json:"generated_at"`
}

type agentFleetItem struct {
	ClusterID          string          `json:"cluster_id"`
	ClusterName        string          `json:"cluster_name"`
	ClusterDisplayName string          `json:"cluster_display_name"`
	ClusterStatus      string          `json:"cluster_status"`
	IsLocal            bool            `json:"is_local"`
	AgentStatus        string          `json:"agent_status"`
	AgentID            string          `json:"agent_id,omitempty"`
	SessionID          string          `json:"session_id,omitempty"`
	AgentVersion       string          `json:"agent_version,omitempty"`
	KubernetesVersion  string          `json:"kubernetes_version,omitempty"`
	Distribution       string          `json:"distribution,omitempty"`
	NodeCount          int32           `json:"node_count"`
	ConnectedAt        *string         `json:"connected_at,omitempty"`
	LastPing           *string         `json:"last_ping,omitempty"`
	LastHeartbeat      *string         `json:"last_heartbeat,omitempty"`
	DisconnectedAt     *string         `json:"disconnected_at,omitempty"`
	PodName            string          `json:"pod_name,omitempty"`
	NodeName           string          `json:"node_name,omitempty"`
	ChannelName        string          `json:"channel_name,omitempty"`
	PrivilegeProfile   string          `json:"privilege_profile"`
	Capabilities       map[string]bool `json:"capabilities"`
	DegradedReasons    []string        `json:"degraded_reasons,omitempty"`
	RecommendedAction  string          `json:"recommended_action,omitempty"`
}

type agentDiagnosticsResponse struct {
	GeneratedAt           string                       `json:"generated_at"`
	Agent                 agentFleetItem               `json:"agent"`
	RecentConnections     []agentConnectionDiagnostic  `json:"recent_connections"`
	Conditions            []clusterConditionDiagnostic `json:"conditions"`
	Live                  *agentLiveDiagnostics        `json:"live,omitempty"`
	Recommendations       []string                     `json:"recommendations"`
	Redactions            []string                     `json:"redactions"`
	UpgradeRecommendation agentUpgradeRecommendation   `json:"upgrade_recommendation"`
}

type agentLiveDiagnostics struct {
	CollectedAt string           `json:"collected_at"`
	Deployment  map[string]any   `json:"deployment,omitempty"`
	Pods        []agentLivePod   `json:"pods,omitempty"`
	Events      []agentLiveEvent `json:"events,omitempty"`
	Logs        []agentLiveLog   `json:"logs,omitempty"`
	Discovery   map[string]any   `json:"discovery,omitempty"`
	Errors      []string         `json:"errors,omitempty"`
}

type agentLivePod struct {
	Name            string   `json:"name"`
	Namespace       string   `json:"namespace"`
	Phase           string   `json:"phase"`
	NodeName        string   `json:"node_name,omitempty"`
	Ready           bool     `json:"ready"`
	RestartCount    int32    `json:"restart_count"`
	ContainerImages []string `json:"container_images,omitempty"`
}

type agentLiveEvent struct {
	Type    string `json:"type,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
	Time    string `json:"time,omitempty"`
}

type agentLiveLog struct {
	PodName   string   `json:"pod_name"`
	Lines     []string `json:"lines"`
	Truncated bool     `json:"truncated"`
}

type agentDiagnosticsBundleResponse struct {
	Version     string                   `json:"version"`
	GeneratedAt string                   `json:"generated_at"`
	ClusterID   string                   `json:"cluster_id"`
	ClusterName string                   `json:"cluster_name"`
	Diagnostics agentDiagnosticsResponse `json:"diagnostics"`
	Notes       []string                 `json:"notes"`
}

type agentConnectionDiagnostic struct {
	ID             string  `json:"id"`
	AgentID        string  `json:"agent_id"`
	SessionID      string  `json:"session_id"`
	Status         string  `json:"status"`
	AgentVersion   string  `json:"agent_version,omitempty"`
	ConnectedAt    string  `json:"connected_at"`
	LastPing       *string `json:"last_ping,omitempty"`
	DisconnectedAt *string `json:"disconnected_at,omitempty"`
	PodName        string  `json:"pod_name,omitempty"`
	NodeName       string  `json:"node_name,omitempty"`
	ChannelName    string  `json:"channel_name,omitempty"`
}

type clusterConditionDiagnostic struct {
	Type               string  `json:"type"`
	Status             string  `json:"status"`
	Reason             string  `json:"reason,omitempty"`
	Message            string  `json:"message,omitempty"`
	LastTransitionTime string  `json:"last_transition_time"`
	LastProbeTime      *string `json:"last_probe_time,omitempty"`
}

type agentUpgradeRecommendation struct {
	CurrentVersion string `json:"current_version,omitempty"`
	Status         string `json:"status"`
	Message        string `json:"message"`
}

type agentUpgradePlanRequest struct {
	TargetVersion string `json:"target_version"`
	TargetImage   string `json:"target_image"`
	Strategy      string `json:"strategy"`
}

type agentUpgradePlanResponse struct {
	ClusterID        string   `json:"cluster_id"`
	ClusterName      string   `json:"cluster_name"`
	CurrentVersion   string   `json:"current_version,omitempty"`
	TargetVersion    string   `json:"target_version"`
	CurrentImage     string   `json:"current_image,omitempty"`
	TargetImage      string   `json:"target_image"`
	PrivilegeProfile string   `json:"privilege_profile"`
	Strategy         string   `json:"strategy"`
	Ready            bool     `json:"ready"`
	Blockers         []string `json:"blockers,omitempty"`
	Steps            []string `json:"steps"`
	Validation       []string `json:"validation"`
	Rollback         []string `json:"rollback"`
}

type agentUpgradeOperationResponse struct {
	Operation agentLifecycleOperationResponse `json:"operation"`
	Plan      agentUpgradePlanResponse        `json:"plan"`
}

type agentLifecycleOperationsResponse struct {
	Items  []agentLifecycleOperationResponse `json:"items"`
	Limit  int32                             `json:"limit"`
	Offset int32                             `json:"offset"`
}

type agentLifecycleOperationResponse struct {
	ID             string          `json:"id"`
	ClusterID      string          `json:"cluster_id"`
	OperationType  string          `json:"operation_type"`
	Status         string          `json:"status"`
	TargetVersion  string          `json:"target_version"`
	TargetImage    string          `json:"target_image"`
	CurrentVersion string          `json:"current_version,omitempty"`
	Strategy       string          `json:"strategy"`
	OperationSpec  json.RawMessage `json:"operation_spec,omitempty"`
	RequestedBy    *string         `json:"requested_by,omitempty"`
	StartedAt      *string         `json:"started_at,omitempty"`
	CompletedAt    *string         `json:"completed_at,omitempty"`
	LastError      string          `json:"last_error,omitempty"`
	CreatedAt      string          `json:"created_at"`
	UpdatedAt      string          `json:"updated_at"`
}

type agentFleetHandlerError struct {
	status  int
	code    string
	message string
}

func (e *agentFleetHandlerError) Error() string {
	return e.message
}

func (h *AgentFleetHandler) List(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, "agent_fleet_unavailable", "Agent fleet inventory is not configured")
		return
	}

	limit := int32(queryInt(r, "limit", 100))
	offset := int32(queryInt(r, "offset", 0))
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}

	total, err := h.queries.CountClusters(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "count_error", "Failed to count clusters")
		return
	}
	clusters, err := h.queries.ListClusters(r.Context(), sqlc.ListClustersParams{Limit: limit, Offset: offset})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "list_error", "Failed to list clusters")
		return
	}
	active, err := h.queries.ListActiveConnections(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "agent_connection_error", "Failed to list active agent connections")
		return
	}

	activeByCluster := make(map[uuid.UUID]sqlc.AgentConnection, len(active))
	for _, conn := range active {
		if existing, ok := activeByCluster[conn.ClusterID]; !ok || conn.ConnectedAt.After(existing.ConnectedAt) {
			activeByCluster[conn.ClusterID] = conn
		}
	}

	now := h.now().UTC()
	items := make([]agentFleetItem, 0, len(clusters))
	summary := agentFleetSummary{
		TotalClusters: total,
		Versions:      map[string]int{},
		Profiles:      map[string]int{},
		Statuses:      map[string]int{},
		GeneratedAt:   now.Format(time.RFC3339),
	}
	for _, cluster := range clusters {
		conn, connected := activeByCluster[cluster.ID]
		if !connected {
			latest, lerr := h.queries.ListConnectionsByCluster(r.Context(), sqlc.ListConnectionsByClusterParams{
				ClusterID: cluster.ID,
				Limit:     1,
				Offset:    0,
			})
			if lerr == nil && len(latest) > 0 {
				conn = latest[0]
			}
		}
		item := buildAgentFleetItem(cluster, conn, connected, now)
		items = append(items, item)
		summary.Statuses[item.AgentStatus]++
		switch item.AgentStatus {
		case "connected":
			summary.Connected++
		case "degraded":
			summary.Degraded++
		default:
			summary.Disconnected++
		}
		if item.AgentVersion != "" {
			summary.Versions[item.AgentVersion]++
		}
		summary.Profiles[item.PrivilegeProfile]++
	}

	RespondJSON(w, http.StatusOK, agentFleetResponse{
		Summary: summary,
		Items:   items,
		Limit:   limit,
		Offset:  offset,
	})
}

func (h *AgentFleetHandler) Diagnostics(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, "agent_fleet_unavailable", "Agent fleet inventory is not configured")
		return
	}
	clusterID, err := uuid.Parse(chi.URLParam(r, "cluster_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_cluster_id", "Invalid cluster ID")
		return
	}
	_, diagnostics, err := h.buildDiagnostics(r.Context(), clusterID)
	if err != nil {
		respondAgentFleetError(w, r, err)
		return
	}
	RespondJSON(w, http.StatusOK, diagnostics)
}

func (h *AgentFleetHandler) DiagnosticsBundle(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, "agent_fleet_unavailable", "Agent fleet inventory is not configured")
		return
	}
	clusterID, err := uuid.Parse(chi.URLParam(r, "cluster_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_cluster_id", "Invalid cluster ID")
		return
	}
	cluster, diagnostics, err := h.buildDiagnostics(r.Context(), clusterID)
	if err != nil {
		respondAgentFleetError(w, r, err)
		return
	}

	filename := "astronomer-agent-diagnostics-" + cluster.ID.String() + ".json"
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(agentDiagnosticsBundleResponse{
		Version:     "v1",
		GeneratedAt: h.now().UTC().Format(time.RFC3339),
		ClusterID:   cluster.ID.String(),
		ClusterName: firstNonEmptyAgentValue(cluster.DisplayName, cluster.Name),
		Diagnostics: diagnostics,
		Notes: []string{
			"Secrets, registration tokens, and certificate material are intentionally excluded.",
			"Use this bundle for support triage; it does not grant cluster access.",
		},
	})
}

func (h *AgentFleetHandler) buildDiagnostics(ctx context.Context, clusterID uuid.UUID) (sqlc.Cluster, agentDiagnosticsResponse, error) {
	cluster, err := h.queries.GetClusterByID(ctx, clusterID)
	if err != nil {
		return sqlc.Cluster{}, agentDiagnosticsResponse{}, &agentFleetHandlerError{status: http.StatusNotFound, code: "not_found", message: "Cluster not found"}
	}
	connections, err := h.queries.ListConnectionsByCluster(ctx, sqlc.ListConnectionsByClusterParams{
		ClusterID: clusterID,
		Limit:     10,
		Offset:    0,
	})
	if err != nil {
		return sqlc.Cluster{}, agentDiagnosticsResponse{}, &agentFleetHandlerError{status: http.StatusInternalServerError, code: "connection_error", message: "Failed to list agent connections"}
	}
	conditions, err := h.queries.ListClusterConditions(ctx, clusterID)
	if err != nil {
		return sqlc.Cluster{}, agentDiagnosticsResponse{}, &agentFleetHandlerError{status: http.StatusInternalServerError, code: "condition_error", message: "Failed to list cluster conditions"}
	}

	now := h.now().UTC()
	active := sqlc.AgentConnection{}
	connected := false
	for _, conn := range connections {
		if conn.Status == "connected" {
			active = conn
			connected = true
			break
		}
	}
	if !connected && len(connections) > 0 {
		active = connections[0]
	}
	agent := buildAgentFleetItem(cluster, active, connected, now)
	recommendations := append([]string{}, agent.DegradedReasons...)
	if agent.RecommendedAction != "" {
		recommendations = append(recommendations, agent.RecommendedAction)
	}
	for _, condition := range conditions {
		if condition.Status != "True" && condition.Message != "" {
			recommendations = append(recommendations, condition.Type+": "+condition.Message)
		}
	}

	response := agentDiagnosticsResponse{
		GeneratedAt:       now.Format(time.RFC3339),
		Agent:             agent,
		RecentConnections: connectionDiagnostics(connections),
		Conditions:        conditionDiagnostics(conditions),
		Recommendations:   recommendations,
		Redactions: []string{
			"agent registration tokens are not included",
			"cluster CA certificate material is not included",
			"pod logs and Kubernetes secrets are not included in this bundle",
		},
		UpgradeRecommendation: upgradeRecommendation(agent),
	}
	if h.requester != nil && agent.AgentStatus != "disconnected" {
		live := h.collectLiveDiagnostics(ctx, cluster.ID.String(), now)
		response.Live = &live
		response.Redactions = append(response.Redactions, "live agent logs are tail-limited and sensitive-looking lines are redacted")
	}
	return cluster, response, nil
}

func (h *AgentFleetHandler) UpgradePlan(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, "agent_fleet_unavailable", "Agent fleet inventory is not configured")
		return
	}
	clusterID, err := uuid.Parse(chi.URLParam(r, "cluster_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_cluster_id", "Invalid cluster ID")
		return
	}
	var req agentUpgradePlanRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	_, plan, err := h.buildUpgradePlanForCluster(r.Context(), clusterID, req)
	if err != nil {
		respondAgentFleetError(w, r, err)
		return
	}
	RespondJSON(w, http.StatusOK, plan)
}

func (h *AgentFleetHandler) Upgrade(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, "agent_fleet_unavailable", "Agent fleet inventory is not configured")
		return
	}
	clusterID, err := uuid.Parse(chi.URLParam(r, "cluster_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_cluster_id", "Invalid cluster ID")
		return
	}
	var req agentUpgradePlanRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	cluster, plan, err := h.buildUpgradePlanForCluster(r.Context(), clusterID, req)
	if err != nil {
		respondAgentFleetError(w, r, err)
		return
	}
	if !plan.Ready {
		RespondJSON(w, http.StatusConflict, map[string]any{
			"plan":     plan,
			"blockers": plan.Blockers,
		})
		return
	}
	spec, err := json.Marshal(map[string]any{
		"request":      req,
		"plan":         plan,
		"queued_at":    h.now().UTC().Format(time.RFC3339),
		"cluster_name": firstNonEmptyAgentValue(cluster.DisplayName, cluster.Name),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "encode_error", "Failed to encode lifecycle operation")
		return
	}
	op, err := h.queries.CreateAgentLifecycleOperation(r.Context(), sqlc.CreateAgentLifecycleOperationParams{
		ClusterID:      cluster.ID,
		OperationType:  "agent_upgrade",
		TargetVersion:  plan.TargetVersion,
		TargetImage:    plan.TargetImage,
		CurrentVersion: plan.CurrentVersion,
		Strategy:       plan.Strategy,
		OperationSpec:  spec,
		RequestedBy:    currentUserUUID(r),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "create_error", "Failed to queue agent upgrade operation")
		return
	}
	recordAudit(r, h.queries, "agent.upgrade.queued", "agent_lifecycle_operation", op.ID.String(), firstNonEmptyAgentValue(cluster.DisplayName, cluster.Name), map[string]any{
		"cluster_id":      cluster.ID.String(),
		"current_version": plan.CurrentVersion,
		"target_version":  plan.TargetVersion,
		"target_image":    plan.TargetImage,
		"strategy":        plan.Strategy,
	})
	RespondJSON(w, http.StatusAccepted, agentUpgradeOperationResponse{
		Operation: agentLifecycleOperationDTO(op),
		Plan:      plan,
	})
}

func (h *AgentFleetHandler) Operations(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, "agent_fleet_unavailable", "Agent fleet inventory is not configured")
		return
	}
	clusterID, err := uuid.Parse(chi.URLParam(r, "cluster_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_cluster_id", "Invalid cluster ID")
		return
	}
	if _, err := h.queries.GetClusterByID(r.Context(), clusterID); err != nil {
		RespondRequestError(w, r, http.StatusNotFound, "not_found", "Cluster not found")
		return
	}
	limit := int32(queryInt(r, "limit", 20))
	offset := int32(queryInt(r, "offset", 0))
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	ops, err := h.queries.ListAgentLifecycleOperationsByCluster(r.Context(), sqlc.ListAgentLifecycleOperationsByClusterParams{
		ClusterID: clusterID,
		Limit:     limit,
		Offset:    offset,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "list_error", "Failed to list agent lifecycle operations")
		return
	}
	items := make([]agentLifecycleOperationResponse, 0, len(ops))
	for _, op := range ops {
		items = append(items, agentLifecycleOperationDTO(op))
	}
	RespondJSON(w, http.StatusOK, agentLifecycleOperationsResponse{Items: items, Limit: limit, Offset: offset})
}

func (h *AgentFleetHandler) buildUpgradePlanForCluster(ctx context.Context, clusterID uuid.UUID, req agentUpgradePlanRequest) (sqlc.Cluster, agentUpgradePlanResponse, error) {
	cluster, err := h.queries.GetClusterByID(ctx, clusterID)
	if err != nil {
		return sqlc.Cluster{}, agentUpgradePlanResponse{}, &agentFleetHandlerError{status: http.StatusNotFound, code: "not_found", message: "Cluster not found"}
	}
	connections, err := h.queries.ListConnectionsByCluster(ctx, sqlc.ListConnectionsByClusterParams{
		ClusterID: clusterID,
		Limit:     1,
		Offset:    0,
	})
	if err != nil {
		return sqlc.Cluster{}, agentUpgradePlanResponse{}, &agentFleetHandlerError{status: http.StatusInternalServerError, code: "connection_error", message: "Failed to load agent connection state"}
	}
	now := h.now().UTC()
	conn := sqlc.AgentConnection{}
	connected := false
	if len(connections) > 0 {
		conn = connections[0]
		connected = conn.Status == "connected"
	}
	agent := buildAgentFleetItem(cluster, conn, connected, now)
	return cluster, h.buildUpgradePlan(cluster, agent, req), nil
}

func buildAgentFleetItem(cluster sqlc.Cluster, conn sqlc.AgentConnection, connected bool, now time.Time) agentFleetItem {
	profile := agentPrivilegeProfileFromAnnotations(cluster.Annotations)
	item := agentFleetItem{
		ClusterID:          cluster.ID.String(),
		ClusterName:        cluster.Name,
		ClusterDisplayName: cluster.DisplayName,
		ClusterStatus:      cluster.Status,
		IsLocal:            cluster.IsLocal,
		AgentVersion:       firstNonEmptyAgentValue(conn.AgentVersion, cluster.AgentVersion),
		KubernetesVersion:  cluster.KubernetesVersion,
		Distribution:       cluster.Distribution,
		NodeCount:          cluster.NodeCount,
		LastHeartbeat:      timestampPtr(cluster.LastHeartbeat),
		PrivilegeProfile:   profile,
		Capabilities:       inferredAgentCapabilities(profile),
	}
	if conn.ID != uuid.Nil {
		item.AgentID = conn.AgentID
		item.SessionID = conn.SessionID
		item.ConnectedAt = stringPtr(conn.ConnectedAt.UTC().Format(time.RFC3339))
		item.LastPing = timestampPtr(conn.LastPing)
		item.DisconnectedAt = timestampPtr(conn.DisconnectedAt)
		item.PodName = conn.PodName
		item.NodeName = conn.NodeName
		item.ChannelName = conn.ChannelName
	}

	reasons := make([]string, 0, 3)
	if !connected {
		item.AgentStatus = "disconnected"
		if cluster.Status == "awaiting_agent" {
			item.RecommendedAction = "Install or restart the Astronomer agent manifest for this cluster."
		} else {
			item.RecommendedAction = "Check the agent deployment, network egress, and registration token state."
		}
		return item
	}
	if item.LastPing != nil {
		if t, err := time.Parse(time.RFC3339, *item.LastPing); err == nil && now.Sub(t) > 2*time.Minute {
			reasons = append(reasons, "agent connection ping is stale")
		}
	}
	if item.LastHeartbeat != nil {
		if t, err := time.Parse(time.RFC3339, *item.LastHeartbeat); err == nil && now.Sub(t) > 2*time.Minute {
			reasons = append(reasons, "cluster heartbeat is stale")
		}
	}
	if profile == agenttemplate.PrivilegeProfileAdmin {
		reasons = append(reasons, "agent is using full-admin privilege profile")
	}
	if len(reasons) > 0 {
		item.AgentStatus = "degraded"
		item.DegradedReasons = reasons
		item.RecommendedAction = "Review the degraded reasons and rotate to the least-privilege operator profile where possible."
		return item
	}
	item.AgentStatus = "connected"
	return item
}

func agentPrivilegeProfileFromAnnotations(raw json.RawMessage) string {
	if len(raw) == 0 {
		return agenttemplate.PrivilegeProfileAdmin
	}
	var annotations map[string]string
	if err := json.Unmarshal(raw, &annotations); err != nil {
		return agenttemplate.PrivilegeProfileAdmin
	}
	return agenttemplate.NormalizePrivilegeProfile(annotations[agenttemplate.PrivilegeProfileAnnotation])
}

func inferredAgentCapabilities(profile string) map[string]bool {
	switch agenttemplate.NormalizePrivilegeProfile(profile) {
	case agenttemplate.PrivilegeProfileViewer:
		return map[string]bool{
			"watch":         true,
			"logs":          true,
			"exec":          false,
			"helm":          false,
			"service_proxy": false,
			"mutate":        false,
		}
	case agenttemplate.PrivilegeProfileOperator:
		return map[string]bool{
			"watch":         true,
			"logs":          true,
			"exec":          true,
			"helm":          true,
			"service_proxy": true,
			"mutate":        true,
		}
	default:
		return map[string]bool{
			"watch":         true,
			"logs":          true,
			"exec":          true,
			"helm":          true,
			"service_proxy": true,
			"mutate":        true,
		}
	}
}

func connectionDiagnostics(connections []sqlc.AgentConnection) []agentConnectionDiagnostic {
	out := make([]agentConnectionDiagnostic, 0, len(connections))
	for _, conn := range connections {
		out = append(out, agentConnectionDiagnostic{
			ID:             conn.ID.String(),
			AgentID:        conn.AgentID,
			SessionID:      conn.SessionID,
			Status:         conn.Status,
			AgentVersion:   conn.AgentVersion,
			ConnectedAt:    conn.ConnectedAt.UTC().Format(time.RFC3339),
			LastPing:       timestampPtr(conn.LastPing),
			DisconnectedAt: timestampPtr(conn.DisconnectedAt),
			PodName:        conn.PodName,
			NodeName:       conn.NodeName,
			ChannelName:    conn.ChannelName,
		})
	}
	return out
}

func conditionDiagnostics(conditions []sqlc.ClusterCondition) []clusterConditionDiagnostic {
	out := make([]clusterConditionDiagnostic, 0, len(conditions))
	for _, condition := range conditions {
		out = append(out, clusterConditionDiagnostic{
			Type:               condition.Type,
			Status:             condition.Status,
			Reason:             condition.Reason,
			Message:            condition.Message,
			LastTransitionTime: condition.LastTransitionTime.UTC().Format(time.RFC3339),
			LastProbeTime:      timePtr(condition.LastProbeTime),
		})
	}
	return out
}

func (h *AgentFleetHandler) collectLiveDiagnostics(ctx context.Context, clusterID string, now time.Time) agentLiveDiagnostics {
	out := agentLiveDiagnostics{
		CollectedAt: now.UTC().Format(time.RFC3339),
		Discovery:   map[string]any{},
	}
	if h == nil || h.requester == nil {
		out.Errors = append(out.Errors, "kubernetes requester is not configured")
		return out
	}
	liveCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	deploymentPath := "/apis/apps/v1/namespaces/astronomer-system/deployments/astronomer-agent"
	var deployment map[string]any
	if err := h.getLiveJSON(liveCtx, clusterID, deploymentPath, &deployment); err != nil {
		out.Errors = append(out.Errors, "deployment: "+err.Error())
	} else {
		out.Deployment = summarizeAgentDeployment(deployment)
	}

	labelSelector := url.QueryEscape("app.kubernetes.io/name=astronomer-agent,app.kubernetes.io/component=agent")
	var podList struct {
		Items []struct {
			Metadata struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
			} `json:"metadata"`
			Spec struct {
				NodeName   string `json:"nodeName"`
				Containers []struct {
					Image string `json:"image"`
				} `json:"containers"`
			} `json:"spec"`
			Status struct {
				Phase             string `json:"phase"`
				ContainerStatuses []struct {
					Ready        bool  `json:"ready"`
					RestartCount int32 `json:"restartCount"`
				} `json:"containerStatuses"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := h.getLiveJSON(liveCtx, clusterID, "/api/v1/namespaces/astronomer-system/pods?labelSelector="+labelSelector, &podList); err != nil {
		out.Errors = append(out.Errors, "pods: "+err.Error())
	} else {
		for _, pod := range podList.Items {
			entry := agentLivePod{
				Name:      pod.Metadata.Name,
				Namespace: pod.Metadata.Namespace,
				Phase:     pod.Status.Phase,
				NodeName:  pod.Spec.NodeName,
				Ready:     len(pod.Status.ContainerStatuses) > 0,
			}
			for _, container := range pod.Spec.Containers {
				if container.Image != "" {
					entry.ContainerImages = append(entry.ContainerImages, container.Image)
				}
			}
			for _, status := range pod.Status.ContainerStatuses {
				entry.Ready = entry.Ready && status.Ready
				entry.RestartCount += status.RestartCount
			}
			out.Pods = append(out.Pods, entry)
			if len(out.Logs) < 3 && pod.Metadata.Name != "" {
				logs, truncated, err := h.getLivePodLogs(liveCtx, clusterID, pod.Metadata.Name)
				if err != nil {
					out.Errors = append(out.Errors, "logs/"+pod.Metadata.Name+": "+err.Error())
				} else {
					out.Logs = append(out.Logs, agentLiveLog{PodName: pod.Metadata.Name, Lines: logs, Truncated: truncated})
				}
			}
		}
	}

	var eventList struct {
		Items []struct {
			Type           string `json:"type"`
			Reason         string `json:"reason"`
			Message        string `json:"message"`
			EventTime      string `json:"eventTime"`
			LastTimestamp  string `json:"lastTimestamp"`
			FirstTimestamp string `json:"firstTimestamp"`
		} `json:"items"`
	}
	if err := h.getLiveJSON(liveCtx, clusterID, "/api/v1/namespaces/astronomer-system/events?fieldSelector="+url.QueryEscape("involvedObject.name=astronomer-agent"), &eventList); err != nil {
		out.Errors = append(out.Errors, "events: "+err.Error())
	} else {
		for i, event := range eventList.Items {
			if i >= 20 {
				break
			}
			out.Events = append(out.Events, agentLiveEvent{
				Type:    event.Type,
				Reason:  event.Reason,
				Message: event.Message,
				Time:    firstNonEmptyAgentValue(event.EventTime, event.LastTimestamp, event.FirstTimestamp),
			})
		}
	}

	var version map[string]any
	if err := h.getLiveJSON(liveCtx, clusterID, "/version", &version); err != nil {
		out.Errors = append(out.Errors, "version: "+err.Error())
	} else {
		out.Discovery["version"] = version
	}
	var apis map[string]any
	if err := h.getLiveJSON(liveCtx, clusterID, "/apis", &apis); err != nil {
		out.Errors = append(out.Errors, "apis: "+err.Error())
	} else {
		out.Discovery["apis"] = summarizeAPIResourceList(apis)
	}
	return out
}

func (h *AgentFleetHandler) getLiveJSON(ctx context.Context, clusterID, path string, out any) error {
	resp, err := h.requester.Do(ctx, clusterID, http.MethodGet, path, nil, requestHeaders(""))
	if err != nil {
		return err
	}
	if err := ensureSuccess(resp); err != nil {
		return err
	}
	return parseJSONResponse(resp, out)
}

func (h *AgentFleetHandler) getLivePodLogs(ctx context.Context, clusterID, podName string) ([]string, bool, error) {
	path := "/api/v1/namespaces/astronomer-system/pods/" + url.PathEscape(podName) + "/log?tailLines=200&timestamps=true"
	resp, err := h.requester.Do(ctx, clusterID, http.MethodGet, path, nil, requestHeaders(""))
	if err != nil {
		return nil, false, err
	}
	if err := ensureSuccess(resp); err != nil {
		return nil, false, err
	}
	raw, err := decodeResponseBody(resp)
	if err != nil {
		return nil, false, err
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return []string{}, false, nil
	}
	truncated := false
	if len(lines) > 200 {
		lines = lines[len(lines)-200:]
		truncated = true
	}
	for i, line := range lines {
		lines[i] = redactDiagnosticLine(line)
	}
	return lines, truncated, nil
}

func summarizeAgentDeployment(raw map[string]any) map[string]any {
	return map[string]any{
		"name":               stringAt(raw, "metadata", "name"),
		"namespace":          stringAt(raw, "metadata", "namespace"),
		"generation":         rawAt(raw, "metadata", "generation"),
		"replicas":           rawAt(raw, "spec", "replicas"),
		"updated_replicas":   rawAt(raw, "status", "updatedReplicas"),
		"ready_replicas":     rawAt(raw, "status", "readyReplicas"),
		"available_replicas": rawAt(raw, "status", "availableReplicas"),
		"conditions":         rawAt(raw, "status", "conditions"),
	}
}

func summarizeAPIResourceList(raw map[string]any) map[string]any {
	groups, _ := raw["groups"].([]any)
	names := make([]string, 0, len(groups))
	for _, item := range groups {
		group, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if name, _ := group["name"].(string); name != "" {
			names = append(names, name)
		}
	}
	return map[string]any{
		"group_count": len(names),
		"groups":      names,
	}
}

func rawAt(raw map[string]any, keys ...string) any {
	var cur any = raw
	for _, key := range keys {
		next, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = next[key]
	}
	return cur
}

func stringAt(raw map[string]any, keys ...string) string {
	value, _ := rawAt(raw, keys...).(string)
	return value
}

func redactDiagnosticLine(line string) string {
	lower := strings.ToLower(line)
	for _, marker := range []string{"token", "secret", "authorization", "bearer ", "password"} {
		if strings.Contains(lower, marker) {
			return "[redacted sensitive log line]"
		}
	}
	if len(line) > 500 {
		return line[:500] + "...[truncated]"
	}
	return line
}

func upgradeRecommendation(agent agentFleetItem) agentUpgradeRecommendation {
	if agent.AgentVersion == "" {
		return agentUpgradeRecommendation{
			Status:  "unknown",
			Message: "Agent version is not reported yet; wait for a heartbeat before planning an upgrade.",
		}
	}
	if agent.AgentStatus == "disconnected" {
		return agentUpgradeRecommendation{
			CurrentVersion: agent.AgentVersion,
			Status:         "blocked",
			Message:        "Agent must reconnect before an in-place upgrade can be coordinated.",
		}
	}
	return agentUpgradeRecommendation{
		CurrentVersion: agent.AgentVersion,
		Status:         "upgrade_trackable",
		Message:        "Queue an upgrade operation; the connected agent will patch its own Deployment image and report the result.",
	}
}

func (h *AgentFleetHandler) buildUpgradePlan(cluster sqlc.Cluster, agent agentFleetItem, req agentUpgradePlanRequest) agentUpgradePlanResponse {
	targetVersion := strings.TrimSpace(req.TargetVersion)
	if targetVersion == "" {
		targetVersion = h.agentImageTag
	}
	if targetVersion == "" {
		targetVersion = "latest"
	}
	targetImage := strings.TrimSpace(req.TargetImage)
	if targetImage == "" {
		targetImage = targetAgentImage(h.agentImageRepository, targetVersion)
	}
	strategy := strings.TrimSpace(req.Strategy)
	if strategy == "" {
		strategy = "agent_self_rollout"
	}
	blockers := make([]string, 0, 3)
	if agent.AgentStatus == "disconnected" {
		blockers = append(blockers, "agent is disconnected; reconnect it before rollout")
	}
	if targetImage == "" {
		blockers = append(blockers, "target image is not configured")
	}
	if cluster.IsLocal {
		blockers = append(blockers, "local management-cluster agent is upgraded with the Astronomer server release")
	}
	profile := agent.PrivilegeProfile
	if profile == "" {
		profile = h.agentUpgradeDefaultProfile
	}
	currentImage := ""
	if agent.AgentVersion != "" {
		currentImage = targetAgentImage(h.agentImageRepository, agent.AgentVersion)
	}
	return agentUpgradePlanResponse{
		ClusterID:        cluster.ID.String(),
		ClusterName:      firstNonEmptyAgentValue(cluster.DisplayName, cluster.Name),
		CurrentVersion:   agent.AgentVersion,
		TargetVersion:    targetVersion,
		CurrentImage:     currentImage,
		TargetImage:      targetImage,
		PrivilegeProfile: profile,
		Strategy:         strategy,
		Ready:            len(blockers) == 0,
		Blockers:         blockers,
		Steps: []string{
			"Queue the upgrade operation in Astronomer.",
			"The connected agent patches the astronomer-system/astronomer-agent Deployment to the target image.",
			"The agent reports whether the Deployment patch was accepted by the Kubernetes API.",
			"Confirm the replacement agent pod reconnects and reports the target version in Agent Fleet.",
		},
		Validation: []string{
			"Agent Fleet status returns connected for the cluster.",
			"Last heartbeat and last ping are both fresh.",
			"Privilege profile is unchanged or intentionally narrowed.",
			"Kubernetes proxy GET /version succeeds through the tunnel.",
		},
		Rollback: []string{
			"Reapply the previous agent image tag if the new agent fails to reconnect.",
			"Keep the current registration token and CA bundle unchanged during rollback.",
			"Collect diagnostics before deleting the failed agent pod if possible.",
		},
	}
}

func respondAgentFleetError(w http.ResponseWriter, r *http.Request, err error) {
	var handlerErr *agentFleetHandlerError
	if errors.As(err, &handlerErr) {
		RespondRequestError(w, r, handlerErr.status, handlerErr.code, handlerErr.message)
		return
	}
	RespondRequestError(w, r, http.StatusInternalServerError, "agent_fleet_error", "Agent fleet request failed")
}

func agentLifecycleOperationDTO(op sqlc.AgentLifecycleOperation) agentLifecycleOperationResponse {
	return agentLifecycleOperationResponse{
		ID:             op.ID.String(),
		ClusterID:      op.ClusterID.String(),
		OperationType:  op.OperationType,
		Status:         op.Status,
		TargetVersion:  op.TargetVersion,
		TargetImage:    op.TargetImage,
		CurrentVersion: op.CurrentVersion,
		Strategy:       op.Strategy,
		OperationSpec:  op.OperationSpec,
		RequestedBy:    uuidFromPgtype(op.RequestedBy),
		StartedAt:      timestampPtr(op.StartedAt),
		CompletedAt:    timestampPtr(op.CompletedAt),
		LastError:      op.LastError,
		CreatedAt:      op.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:      op.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func uuidFromPgtype(value pgtype.UUID) *string {
	if !value.Valid {
		return nil
	}
	id := uuid.UUID(value.Bytes)
	return stringPtr(id.String())
}

func targetAgentImage(repository, tag string) string {
	repository = strings.TrimSpace(repository)
	tag = strings.TrimSpace(tag)
	if repository == "" {
		repository = "ghcr.io/alphabravocompany/astronomer-go-agent"
	}
	if tag == "" {
		return repository
	}
	if strings.Contains(repository, "@sha256:") {
		return repository
	}
	return repository + ":" + tag
}

func timestampPtr(ts pgtype.Timestamptz) *string {
	if !ts.Valid {
		return nil
	}
	return stringPtr(ts.Time.UTC().Format(time.RFC3339))
}

func timePtr(t time.Time) *string {
	if t.IsZero() {
		return nil
	}
	return stringPtr(t.UTC().Format(time.RFC3339))
}

func stringPtr(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func firstNonEmptyAgentValue(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
