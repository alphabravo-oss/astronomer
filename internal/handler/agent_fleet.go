package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	agenttemplate "github.com/alphabravocompany/astronomer-go/deploy/agent"
	"github.com/alphabravocompany/astronomer-go/internal/agentcompat"
	"github.com/alphabravocompany/astronomer-go/internal/agentlifecycle"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/redaction"
	"github.com/alphabravocompany/astronomer-go/pkg/version"
)

type AgentFleetQuerier interface {
	// GetUserByID resolves the calling user for the superuser gate on the
	// cluster-admin posture report (E3). The production *sqlc.Queries
	// satisfies it; narrow test fakes implement it directly.
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlc.User, error)
	CountClusters(ctx context.Context) (int64, error)
	GetClusterByID(ctx context.Context, id uuid.UUID) (sqlc.Cluster, error)
	ListClusters(ctx context.Context, arg sqlc.ListClustersParams) ([]sqlc.Cluster, error)
	ListActiveConnections(ctx context.Context) ([]sqlc.AgentConnection, error)
	ListConnectionsByCluster(ctx context.Context, arg sqlc.ListConnectionsByClusterParams) ([]sqlc.AgentConnection, error)
	ListLatestConnectionsByClusters(ctx context.Context, clusterIds []uuid.UUID) ([]sqlc.AgentConnection, error)
	ListClusterConditions(ctx context.Context, clusterID uuid.UUID) ([]sqlc.ClusterCondition, error)
	ListArgoCDManagedClustersByCluster(ctx context.Context, clusterID uuid.UUID) ([]sqlc.ArgocdManagedCluster, error)
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
		agentUpgradeDefaultProfile: agenttemplate.PrivilegeProfileViewer,
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
	TotalClusters                 int64          `json:"total_clusters"`
	Connected                     int            `json:"connected"`
	Degraded                      int            `json:"degraded"`
	Disconnected                  int            `json:"disconnected"`
	Versions                      map[string]int `json:"versions"`
	Profiles                      map[string]int `json:"profiles"`
	Statuses                      map[string]int `json:"statuses"`
	Compatibility                 map[string]int `json:"compatibility"`
	ServerVersion                 string         `json:"server_version"`
	MinimumSupportedAgentVersion  string         `json:"minimum_supported_agent_version"`
	MinimumCompatibleAgentVersion string         `json:"minimum_compatible_agent_version"`
	GeneratedAt                   string         `json:"generated_at"`
}

type agentFleetItem struct {
	ClusterID            string                `json:"cluster_id"`
	ClusterName          string                `json:"cluster_name"`
	ClusterDisplayName   string                `json:"cluster_display_name"`
	ClusterStatus        string                `json:"cluster_status"`
	IsLocal              bool                  `json:"is_local"`
	AgentStatus          string                `json:"agent_status"`
	AgentID              string                `json:"agent_id,omitempty"`
	SessionID            string                `json:"session_id,omitempty"`
	AgentVersion         string                `json:"agent_version,omitempty"`
	KubernetesVersion    string                `json:"kubernetes_version,omitempty"`
	Distribution         string                `json:"distribution,omitempty"`
	NodeCount            int32                 `json:"node_count"`
	ConnectedAt          *string               `json:"connected_at,omitempty"`
	LastPing             *string               `json:"last_ping,omitempty"`
	LastHeartbeat        *string               `json:"last_heartbeat,omitempty"`
	DisconnectedAt       *string               `json:"disconnected_at,omitempty"`
	PodName              string                `json:"pod_name,omitempty"`
	NodeName             string                `json:"node_name,omitempty"`
	ChannelName          string                `json:"channel_name,omitempty"`
	PrivilegeProfile     string                `json:"privilege_profile"`
	Capabilities         map[string]bool       `json:"capabilities"`
	CompatibilityStatus  string                `json:"compatibility_status"`
	CompatibilityMessage string                `json:"compatibility_message,omitempty"`
	DegradedReasons      []string              `json:"degraded_reasons,omitempty"`
	RecommendedAction    string                `json:"recommended_action,omitempty"`
	OfflineBehavior      *agentOfflineBehavior `json:"offline_behavior,omitempty"`
}

type agentOfflineBehavior struct {
	State                     string   `json:"state"`
	LastKnownAt               *string  `json:"last_known_at,omitempty"`
	Stale                     bool     `json:"stale"`
	Message                   string   `json:"message"`
	PermittedQueuedOperations []string `json:"permitted_queued_operations"`
	BlockedOperations         []string `json:"blocked_operations"`
}

type agentDiagnosticsResponse struct {
	GeneratedAt           string                       `json:"generated_at"`
	Agent                 agentFleetItem               `json:"agent"`
	RecentConnections     []agentConnectionDiagnostic  `json:"recent_connections"`
	Conditions            []clusterConditionDiagnostic `json:"conditions"`
	ArgoCD                agentArgoCDDiagnostic        `json:"argocd"`
	Live                  *agentLiveDiagnostics        `json:"live,omitempty"`
	Recommendations       []string                     `json:"recommendations"`
	Redactions            []string                     `json:"redactions"`
	UpgradeRecommendation agentUpgradeRecommendation   `json:"upgrade_recommendation"`
}

type agentLiveDiagnostics struct {
	CollectedAt string               `json:"collected_at"`
	Deployment  map[string]any       `json:"deployment,omitempty"`
	Pods        []agentLivePod       `json:"pods,omitempty"`
	Events      []agentLiveEvent     `json:"events,omitempty"`
	Logs        []agentLiveLog       `json:"logs,omitempty"`
	Discovery   map[string]any       `json:"discovery,omitempty"`
	Checks      []agentSelfTestCheck `json:"checks,omitempty"`
	Errors      []string             `json:"errors,omitempty"`
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

type agentSelfTestResponse struct {
	GeneratedAt     string               `json:"generated_at"`
	ClusterID       string               `json:"cluster_id"`
	ClusterName     string               `json:"cluster_name"`
	Status          string               `json:"status"`
	Checks          []agentSelfTestCheck `json:"checks"`
	Recommendations []string             `json:"recommendations,omitempty"`
}

type agentSelfTestCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message"`
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

type agentArgoCDDiagnostic struct {
	Registered         bool     `json:"registered"`
	InstanceCount      int      `json:"instance_count"`
	ClusterSecretNames []string `json:"cluster_secret_names,omitempty"`
	ServerURLs         []string `json:"server_urls,omitempty"`
	LastUpdatedAt      *string  `json:"last_updated_at,omitempty"`
}

type agentUpgradeRecommendation struct {
	CurrentVersion string `json:"current_version,omitempty"`
	Status         string `json:"status"`
	Message        string `json:"message"`
}

type agentUpgradePlanRequest struct {
	TargetVersion    string   `json:"target_version"`
	TargetImage      string   `json:"target_image"`
	Strategy         string   `json:"strategy"`
	CanaryClusterIDs []string `json:"canary_cluster_ids"`
	BatchSize        int32    `json:"batch_size"`
	MaxUnavailable   int32    `json:"max_unavailable"`
	RollbackImage    string   `json:"rollback_image"`
}

type agentUpgradePlanResponse struct {
	ClusterID               string   `json:"cluster_id"`
	ClusterName             string   `json:"cluster_name"`
	CurrentVersion          string   `json:"current_version,omitempty"`
	TargetVersion           string   `json:"target_version"`
	CurrentImage            string   `json:"current_image,omitempty"`
	TargetImage             string   `json:"target_image"`
	RollbackImage           string   `json:"rollback_image,omitempty"`
	PrivilegeProfile        string   `json:"privilege_profile"`
	Strategy                string   `json:"strategy"`
	CanaryClusterIDs        []string `json:"canary_cluster_ids,omitempty"`
	BatchSize               int32    `json:"batch_size"`
	MaxUnavailable          int32    `json:"max_unavailable"`
	Ready                   bool     `json:"ready"`
	Blockers                []string `json:"blockers,omitempty"`
	PreflightChecks         []string `json:"preflight_checks"`
	Steps                   []string `json:"steps"`
	PostUpgradeHealthChecks []string `json:"post_upgrade_health_checks"`
	Validation              []string `json:"validation"`
	Rollback                []string `json:"rollback"`
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
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.AgentFleetUnavailable, "Agent fleet inventory is not configured")
		return
	}

	limit := int32(queryLimit(r, 100))
	offset := int32(queryInt(r, "offset", 0))
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}

	total, err := h.queries.CountClusters(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountError, "Failed to count clusters")
		return
	}
	clusters, err := h.queries.ListClusters(r.Context(), sqlc.ListClustersParams{Limit: limit, Offset: offset})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list clusters")
		return
	}
	active, err := h.queries.ListActiveConnections(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.AgentConnectionError, "Failed to list active agent connections")
		return
	}

	activeByCluster := make(map[uuid.UUID]sqlc.AgentConnection, len(active))
	for _, conn := range active {
		if existing, ok := activeByCluster[conn.ClusterID]; !ok || conn.ConnectedAt.After(existing.ConnectedAt) {
			activeByCluster[conn.ClusterID] = conn
		}
	}

	// Load the most recent connection per cluster for the page in a single
	// DISTINCT ON query rather than one fallback query per disconnected
	// cluster (disconnected is the steady state, so the per-row fallback used
	// to issue up to ~500 queries per page).
	latestByCluster := make(map[uuid.UUID]sqlc.AgentConnection, len(clusters))
	if len(clusters) > 0 {
		clusterIDs := make([]uuid.UUID, len(clusters))
		for i, cluster := range clusters {
			clusterIDs[i] = cluster.ID
		}
		latest, lerr := h.queries.ListLatestConnectionsByClusters(r.Context(), clusterIDs)
		if lerr != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.AgentConnectionError, "Failed to list agent connections")
			return
		}
		for _, conn := range latest {
			latestByCluster[conn.ClusterID] = conn
		}
	}

	now := h.now().UTC()
	items := make([]agentFleetItem, 0, len(clusters))
	summary := agentFleetSummary{
		TotalClusters:                 total,
		Versions:                      map[string]int{},
		Profiles:                      map[string]int{},
		Statuses:                      map[string]int{},
		Compatibility:                 map[string]int{},
		ServerVersion:                 version.Version,
		MinimumSupportedAgentVersion:  agentcompat.MinimumSupportedVersion,
		MinimumCompatibleAgentVersion: agentcompat.MinimumCompatibleVersion,
		GeneratedAt:                   now.Format(time.RFC3339),
	}
	for _, cluster := range clusters {
		conn, connected := activeByCluster[cluster.ID]
		if !connected {
			if latest, ok := latestByCluster[cluster.ID]; ok {
				conn = latest
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
		summary.Compatibility[item.CompatibilityStatus]++
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
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.AgentFleetUnavailable, "Agent fleet inventory is not configured")
		return
	}
	clusterID, err := uuid.Parse(chi.URLParam(r, "cluster_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
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
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.AgentFleetUnavailable, "Agent fleet inventory is not configured")
		return
	}
	clusterID, err := uuid.Parse(chi.URLParam(r, "cluster_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
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
	bundle := agentDiagnosticsBundleResponse{
		Version:     "v1",
		GeneratedAt: h.now().UTC().Format(time.RFC3339),
		ClusterID:   cluster.ID.String(),
		ClusterName: firstNonEmptyAgentValue(cluster.DisplayName, cluster.Name),
		Diagnostics: diagnostics,
		Notes: []string{
			"Credential material and certificate bodies are intentionally excluded.",
			"Use this bundle for support triage; it does not grant cluster access.",
		},
	}
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(redaction.Payload(bundle))
}

func (h *AgentFleetHandler) SelfTest(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.AgentFleetUnavailable, "Agent fleet inventory is not configured")
		return
	}
	clusterID, err := uuid.Parse(chi.URLParam(r, "cluster_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}
	cluster, diagnostics, err := h.buildDiagnostics(r.Context(), clusterID)
	if err != nil {
		respondAgentFleetError(w, r, err)
		return
	}
	RespondJSON(w, http.StatusOK, buildAgentSelfTest(cluster, diagnostics, h.now().UTC()))
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
	managedClusters, err := h.queries.ListArgoCDManagedClustersByCluster(ctx, clusterID)
	if err != nil {
		return sqlc.Cluster{}, agentDiagnosticsResponse{}, &agentFleetHandlerError{status: http.StatusInternalServerError, code: "argocd_error", message: "Failed to load Argo CD registration state"}
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
	argoCD := buildAgentArgoCDDiagnostic(managedClusters)
	if !argoCD.Registered {
		recommendations = append(recommendations, "Cluster is not registered to the built-in Argo CD managed-cluster inventory.")
	}

	response := agentDiagnosticsResponse{
		GeneratedAt:       now.Format(time.RFC3339),
		Agent:             agent,
		RecentConnections: connectionDiagnostics(connections),
		Conditions:        conditionDiagnostics(conditions),
		ArgoCD:            argoCD,
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

func buildAgentSelfTest(cluster sqlc.Cluster, diagnostics agentDiagnosticsResponse, now time.Time) agentSelfTestResponse {
	agent := diagnostics.Agent
	checks := []agentSelfTestCheck{
		agentConnectionSelfTestCheck(agent),
		agentTimestampSelfTestCheck("heartbeat_freshness", "heartbeat", agent.LastHeartbeat, now, 2*time.Minute, 5*time.Minute),
		agentTimestampSelfTestCheck("ping_freshness", "connection ping", agent.LastPing, now, 2*time.Minute, 5*time.Minute),
		agentPrivilegeProfileSelfTestCheck(agent),
		agentCompatibilitySelfTestCheck(agent),
		agentArgoCDSelfTestCheck(diagnostics.ArgoCD),
		agentLiveDiagnosticsSelfTestCheck(agent, diagnostics.Live),
		agentClusterConditionsSelfTestCheck(diagnostics.Conditions),
	}

	return agentSelfTestResponse{
		GeneratedAt:     now.UTC().Format(time.RFC3339),
		ClusterID:       cluster.ID.String(),
		ClusterName:     firstNonEmptyAgentValue(cluster.DisplayName, cluster.Name),
		Status:          agentSelfTestOverallStatus(checks),
		Checks:          checks,
		Recommendations: append([]string{}, diagnostics.Recommendations...),
	}
}

func agentConnectionSelfTestCheck(agent agentFleetItem) agentSelfTestCheck {
	switch agent.AgentStatus {
	case "connected":
		return agentSelfTestCheck{Name: "agent_connection", Status: "passed", Message: "Agent tunnel is connected."}
	case "degraded":
		message := "Agent tunnel is connected but degraded."
		if len(agent.DegradedReasons) > 0 {
			message = message + " " + strings.Join(agent.DegradedReasons, "; ")
		}
		return agentSelfTestCheck{Name: "agent_connection", Status: "warning", Message: message}
	case "disconnected":
		return agentSelfTestCheck{Name: "agent_connection", Status: "failed", Message: "Agent tunnel is disconnected."}
	default:
		return agentSelfTestCheck{Name: "agent_connection", Status: "warning", Message: "Agent tunnel state is unknown."}
	}
}

func agentTimestampSelfTestCheck(name, label string, value *string, now time.Time, warnAfter, failAfter time.Duration) agentSelfTestCheck {
	if value == nil || strings.TrimSpace(*value) == "" {
		return agentSelfTestCheck{Name: name, Status: "failed", Message: "No " + label + " timestamp has been recorded."}
	}
	parsed, err := time.Parse(time.RFC3339, *value)
	if err != nil {
		return agentSelfTestCheck{Name: name, Status: "failed", Message: "The " + label + " timestamp is invalid."}
	}
	age := now.Sub(parsed)
	if age < 0 {
		age = 0
	}
	ageText := age.Round(time.Second).String()
	switch {
	case age > failAfter:
		return agentSelfTestCheck{Name: name, Status: "failed", Message: "Last " + label + " is stale (" + ageText + " ago)."}
	case age > warnAfter:
		return agentSelfTestCheck{Name: name, Status: "warning", Message: "Last " + label + " is aging (" + ageText + " ago)."}
	default:
		return agentSelfTestCheck{Name: name, Status: "passed", Message: "Last " + label + " is fresh (" + ageText + " ago)."}
	}
}

func agentPrivilegeProfileSelfTestCheck(agent agentFleetItem) agentSelfTestCheck {
	effectiveProfile := agenttemplate.NormalizePrivilegeProfile(agent.PrivilegeProfile)
	switch effectiveProfile {
	case agenttemplate.PrivilegeProfileViewer, agenttemplate.PrivilegeProfileOperator,
		agenttemplate.PrivilegeProfileNamespaceViewer, agenttemplate.PrivilegeProfileNamespaceOperator:
		return agentSelfTestCheck{Name: "privilege_profile", Status: "passed", Message: "Agent is using the effective " + effectiveProfile + " privilege profile."}
	case agenttemplate.PrivilegeProfileAdmin:
		return agentSelfTestCheck{Name: "privilege_profile", Status: "passed", Message: "Agent is using the explicit full-management (admin) privilege profile. Apply a viewer/operator profile to scope it down if least privilege is required."}
	case agenttemplate.PrivilegeProfileCustom:
		return agentSelfTestCheck{Name: "privilege_profile", Status: "warning", Message: "Agent is using custom RBAC; run live diagnostics to verify required permissions."}
	default:
		return agentSelfTestCheck{Name: "privilege_profile", Status: "warning", Message: "Agent privilege profile is unknown."}
	}
}

func agentCompatibilitySelfTestCheck(agent agentFleetItem) agentSelfTestCheck {
	message := agent.CompatibilityMessage
	if message == "" {
		message = "Agent compatibility status is " + agent.CompatibilityStatus + "."
	}
	switch agent.CompatibilityStatus {
	case "supported":
		return agentSelfTestCheck{Name: "compatibility", Status: "passed", Message: message}
	case "blocked":
		return agentSelfTestCheck{Name: "compatibility", Status: "failed", Message: message}
	default:
		return agentSelfTestCheck{Name: "compatibility", Status: "warning", Message: message}
	}
}

func agentArgoCDSelfTestCheck(argoCD agentArgoCDDiagnostic) agentSelfTestCheck {
	if !argoCD.Registered {
		return agentSelfTestCheck{Name: "argocd_registration", Status: "failed", Message: "Cluster is not registered to built-in Argo CD."}
	}
	return agentSelfTestCheck{
		Name:    "argocd_registration",
		Status:  "passed",
		Message: fmt.Sprintf("Cluster is registered to %d Argo CD instance(s).", argoCD.InstanceCount),
	}
}

func agentLiveDiagnosticsSelfTestCheck(agent agentFleetItem, live *agentLiveDiagnostics) agentSelfTestCheck {
	if agent.AgentStatus == "disconnected" {
		return agentSelfTestCheck{Name: "live_diagnostics", Status: "failed", Message: "Live diagnostics cannot run while the agent is disconnected."}
	}
	if live == nil {
		return agentSelfTestCheck{Name: "live_diagnostics", Status: "warning", Message: "Live diagnostics requester is not configured for this server."}
	}
	if len(live.Errors) > 0 {
		return agentSelfTestCheck{Name: "live_diagnostics", Status: "warning", Message: "Live diagnostics completed with errors: " + strings.Join(live.Errors, "; ")}
	}
	warnings := make([]string, 0)
	for _, check := range live.Checks {
		if check.Status == "failed" {
			return agentSelfTestCheck{Name: "live_diagnostics", Status: "failed", Message: "Live diagnostic check failed: " + check.Name + ". " + check.Message}
		}
		if check.Status == "warning" {
			warnings = append(warnings, check.Name)
		}
	}
	if len(warnings) > 0 {
		return agentSelfTestCheck{Name: "live_diagnostics", Status: "warning", Message: "Live diagnostics completed with warnings: " + strings.Join(warnings, ", ")}
	}
	return agentSelfTestCheck{Name: "live_diagnostics", Status: "passed", Message: "Live diagnostics completed through the agent tunnel."}
}

func agentClusterConditionsSelfTestCheck(conditions []clusterConditionDiagnostic) agentSelfTestCheck {
	if len(conditions) == 0 {
		return agentSelfTestCheck{Name: "cluster_conditions", Status: "warning", Message: "No cluster conditions have been recorded yet."}
	}
	// optionalCapabilityConditions are cluster features that are legitimately
	// absent on many clusters (e.g. no Gateway API CRDs installed). A False here
	// means "this optional capability isn't present", not "the cluster is
	// unhealthy", so it must NOT fail the self-test — surface it as a warning.
	optionalCapabilityConditions := map[string]bool{
		"GatewayAPISupported": true,
	}
	falseConditions := make([]string, 0)
	optionalFalse := make([]string, 0)
	unknownConditions := make([]string, 0)
	for _, condition := range conditions {
		// MetricsAvailable (C3 / M13) is observability only — a cluster with no
		// metrics-server legitimately reports it False (NoMetricsServer) and that
		// must NOT flip the agent self-test to failed. Skip it here; the distinct
		// reason is still surfaced in the raw conditions list for the UI pill.
		if condition.Type == "MetricsAvailable" {
			continue
		}
		switch condition.Status {
		case "True":
			continue
		case "False":
			if optionalCapabilityConditions[condition.Type] {
				optionalFalse = append(optionalFalse, condition.Type)
			} else {
				falseConditions = append(falseConditions, condition.Type)
			}
		default:
			unknownConditions = append(unknownConditions, condition.Type)
		}
	}
	if len(falseConditions) > 0 {
		return agentSelfTestCheck{Name: "cluster_conditions", Status: "failed", Message: "False cluster conditions: " + strings.Join(falseConditions, ", ")}
	}
	if len(optionalFalse) > 0 || len(unknownConditions) > 0 {
		notes := append(append([]string{}, optionalFalse...), unknownConditions...)
		return agentSelfTestCheck{Name: "cluster_conditions", Status: "warning", Message: "Optional or unknown cluster conditions not satisfied: " + strings.Join(notes, ", ")}
	}
	return agentSelfTestCheck{Name: "cluster_conditions", Status: "passed", Message: "Recorded cluster conditions are healthy."}
}

func agentSelfTestOverallStatus(checks []agentSelfTestCheck) string {
	status := "passed"
	for _, check := range checks {
		switch check.Status {
		case "failed":
			return "failed"
		case "warning":
			status = "warning"
		}
	}
	return status
}

func (h *AgentFleetHandler) UpgradePlan(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.AgentFleetUnavailable, "Agent fleet inventory is not configured")
		return
	}
	clusterID, err := uuid.Parse(chi.URLParam(r, "cluster_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
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
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.AgentFleetUnavailable, "Agent fleet inventory is not configured")
		return
	}
	clusterID, err := uuid.Parse(chi.URLParam(r, "cluster_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
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
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.EncodeError, "Failed to encode lifecycle operation")
		return
	}
	op, err := h.createAgentLifecycleOperation(withOperationIdempotency(r, "agent_lifecycle"), sqlc.CreateAgentLifecycleOperationParams{
		ClusterID:      cluster.ID,
		OperationType:  agentlifecycle.OperationTypeUpgrade,
		TargetVersion:  plan.TargetVersion,
		TargetImage:    plan.TargetImage,
		CurrentVersion: plan.CurrentVersion,
		Strategy:       plan.Strategy,
		OperationSpec:  spec,
		RequestedBy:    currentUserUUID(r),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CreateError, "Failed to queue agent upgrade operation")
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

func (h *AgentFleetHandler) createAgentLifecycleOperation(ctx context.Context, params sqlc.CreateAgentLifecycleOperationParams) (sqlc.AgentLifecycleOperation, error) {
	if idem, ok := operationIdempotencyFromContext(ctx); ok {
		type idempotentCreator interface {
			CreateAgentLifecycleOperationIdempotent(context.Context, sqlc.CreateAgentLifecycleOperationIdempotentParams) (sqlc.AgentLifecycleOperation, error)
		}
		if creator, ok := h.queries.(idempotentCreator); ok {
			return creator.CreateAgentLifecycleOperationIdempotent(ctx, sqlc.CreateAgentLifecycleOperationIdempotentParams{
				Scope:          idem.scope,
				IdempotencyKey: idem.key,
				ClusterID:      params.ClusterID,
				OperationType:  params.OperationType,
				TargetVersion:  params.TargetVersion,
				TargetImage:    params.TargetImage,
				CurrentVersion: params.CurrentVersion,
				Strategy:       params.Strategy,
				OperationSpec:  params.OperationSpec,
				RequestedBy:    params.RequestedBy,
			})
		}
	}
	return h.queries.CreateAgentLifecycleOperation(ctx, params)
}

func (h *AgentFleetHandler) Operations(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.AgentFleetUnavailable, "Agent fleet inventory is not configured")
		return
	}
	clusterID, err := uuid.Parse(chi.URLParam(r, "cluster_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}
	if _, err := h.queries.GetClusterByID(r.Context(), clusterID); err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return
	}
	limit := int32(queryLimit(r, 20))
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
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list agent lifecycle operations")
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
	agentVersion := firstNonEmptyAgentValue(conn.AgentVersion, cluster.AgentVersion)
	compatibility := agentcompat.Evaluate(agentVersion)
	item := agentFleetItem{
		ClusterID:            cluster.ID.String(),
		ClusterName:          cluster.Name,
		ClusterDisplayName:   cluster.DisplayName,
		ClusterStatus:        cluster.Status,
		IsLocal:              cluster.IsLocal,
		AgentVersion:         agentVersion,
		KubernetesVersion:    cluster.KubernetesVersion,
		Distribution:         cluster.Distribution,
		NodeCount:            cluster.NodeCount,
		LastHeartbeat:        timestampPtr(cluster.LastHeartbeat),
		PrivilegeProfile:     profile,
		Capabilities:         inferredAgentCapabilities(profile),
		CompatibilityStatus:  compatibility.Status,
		CompatibilityMessage: compatibility.Message,
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
		item.OfflineBehavior = buildAgentOfflineBehavior(cluster, conn, now)
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
	// Note: the full-admin privilege profile is a least-privilege *posture*
	// advisory (surfaced via PrivilegeProfile + the admin-posture endpoint),
	// not a health fault. A connected, fresh, compatible admin-profile agent
	// is fully functional, so it no longer flips the agent to "degraded".
	if compatibility.DegradedReason != "" {
		reasons = append(reasons, compatibility.DegradedReason)
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

func buildAgentOfflineBehavior(cluster sqlc.Cluster, conn sqlc.AgentConnection, now time.Time) *agentOfflineBehavior {
	lastKnown := latestAgentObservationTime(cluster, conn)
	var lastKnownAt *string
	stale := true
	if !lastKnown.IsZero() {
		stamp := lastKnown.UTC().Format(time.RFC3339)
		lastKnownAt = &stamp
		stale = now.Sub(lastKnown) > 5*time.Minute
	}
	message := "Agent tunnel is offline; live diagnostics and in-cluster operations are blocked until it reconnects."
	if lastKnownAt == nil {
		message = "Agent tunnel is offline and no last-known observation has been recorded yet."
	}
	return &agentOfflineBehavior{
		State:       "offline",
		LastKnownAt: lastKnownAt,
		Stale:       stale,
		Message:     message,
		PermittedQueuedOperations: []string{
			"cluster_metadata_updates",
			"argocd_registration_repair",
			"agent_install_manifest_regeneration",
		},
		BlockedOperations: []string{
			agentlifecycle.OperationTypeUpgrade,
			"live_diagnostics",
			"kubernetes_proxy",
			"kubectl_exec",
			"pod_logs",
			"service_proxy",
			"in_cluster_mutations",
		},
	}
}

func latestAgentObservationTime(cluster sqlc.Cluster, conn sqlc.AgentConnection) time.Time {
	newest := time.Time{}
	for _, candidate := range []pgtype.Timestamptz{cluster.LastHeartbeat, conn.LastPing, conn.DisconnectedAt} {
		if candidate.Valid && candidate.Time.After(newest) {
			newest = candidate.Time
		}
	}
	for _, candidate := range []time.Time{conn.ConnectedAt, cluster.UpdatedAt} {
		if !candidate.IsZero() && candidate.After(newest) {
			newest = candidate
		}
	}
	return newest
}

func agentPrivilegeProfileFromAnnotations(raw json.RawMessage) string {
	// Unspecified, malformed, and unknown annotations fail closed to viewer via
	// the canonical normalizer. Admin is available only as an explicit value.
	if len(raw) == 0 {
		return agenttemplate.NormalizePrivilegeProfile("")
	}
	var annotations map[string]string
	if err := json.Unmarshal(raw, &annotations); err != nil {
		return agenttemplate.NormalizePrivilegeProfile("")
	}
	return agenttemplate.NormalizePrivilegeProfile(annotations[agenttemplate.PrivilegeProfileAnnotation])
}

func inferredAgentCapabilities(profile string) map[string]bool {
	switch agenttemplate.NormalizePrivilegeProfile(profile) {
	case agenttemplate.PrivilegeProfileViewer, agenttemplate.PrivilegeProfileNamespaceViewer:
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
	case agenttemplate.PrivilegeProfileNamespaceOperator:
		return map[string]bool{
			"watch":         true,
			"logs":          true,
			"exec":          true,
			"helm":          false,
			"service_proxy": true,
			"mutate":        true,
		}
	case agenttemplate.PrivilegeProfileCustom:
		return map[string]bool{
			"watch":         false,
			"logs":          false,
			"exec":          false,
			"helm":          false,
			"service_proxy": false,
			"mutate":        false,
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

func buildAgentArgoCDDiagnostic(rows []sqlc.ArgocdManagedCluster) agentArgoCDDiagnostic {
	out := agentArgoCDDiagnostic{
		Registered:    len(rows) > 0,
		InstanceCount: len(rows),
	}
	var newest time.Time
	for _, row := range rows {
		if row.ClusterSecretName != "" {
			out.ClusterSecretNames = append(out.ClusterSecretNames, row.ClusterSecretName)
		}
		if row.ServerUrl != "" {
			out.ServerURLs = append(out.ServerURLs, row.ServerUrl)
		}
		if row.UpdatedAt.After(newest) {
			newest = row.UpdatedAt
		}
	}
	if !newest.IsZero() {
		out.LastUpdatedAt = stringPtr(newest.UTC().Format(time.RFC3339))
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
	versionBody, versionHeaders, err := h.getLiveRaw(liveCtx, clusterID, "/version")
	if err != nil {
		out.Errors = append(out.Errors, "version: "+err.Error())
	} else {
		if err := json.Unmarshal(versionBody, &version); err != nil {
			out.Errors = append(out.Errors, "version: "+err.Error())
		} else {
			out.Discovery["version"] = version
		}
		out.Checks = append(out.Checks, agentClockSkewDiagnosticCheck(versionHeaders, now))
	}
	var apis map[string]any
	if err := h.getLiveJSON(liveCtx, clusterID, "/apis", &apis); err != nil {
		out.Errors = append(out.Errors, "apis: "+err.Error())
	} else {
		out.Discovery["apis"] = summarizeAPIResourceList(apis)
	}
	out.Checks = append(out.Checks,
		h.collectRBACSelfReview(liveCtx, clusterID),
		h.collectKubernetesReadyzCheck(liveCtx, clusterID),
	)
	return out
}

func (h *AgentFleetHandler) collectRBACSelfReview(ctx context.Context, clusterID string) agentSelfTestCheck {
	body := map[string]any{
		"apiVersion": "authorization.k8s.io/v1",
		"kind":       "SelfSubjectAccessReview",
		"spec": map[string]any{
			"resourceAttributes": map[string]any{
				"namespace": "astronomer-system",
				"verb":      "get",
				"resource":  "pods",
			},
		},
	}
	var review struct {
		Status struct {
			Allowed         bool   `json:"allowed"`
			Denied          bool   `json:"denied"`
			Reason          string `json:"reason"`
			EvaluationError string `json:"evaluationError"`
		} `json:"status"`
	}
	if err := h.postLiveJSON(ctx, clusterID, "/apis/authorization.k8s.io/v1/selfsubjectaccessreviews", body, &review); err != nil {
		return agentSelfTestCheck{Name: "rbac_self_check", Status: "warning", Message: "Unable to run Kubernetes SelfSubjectAccessReview: " + err.Error()}
	}
	if review.Status.Allowed {
		return agentSelfTestCheck{Name: "rbac_self_check", Status: "passed", Message: "Agent can read its own pods in astronomer-system."}
	}
	if review.Status.Denied {
		return agentSelfTestCheck{Name: "rbac_self_check", Status: "failed", Message: firstNonEmptyAgentValue(review.Status.Reason, "Kubernetes denied the agent pod read self-check.")}
	}
	return agentSelfTestCheck{Name: "rbac_self_check", Status: "warning", Message: firstNonEmptyAgentValue(review.Status.EvaluationError, "Kubernetes returned an inconclusive SelfSubjectAccessReview.")}
}

func (h *AgentFleetHandler) collectKubernetesReadyzCheck(ctx context.Context, clusterID string) agentSelfTestCheck {
	body, _, err := h.getLiveRaw(ctx, clusterID, "/readyz")
	if err != nil {
		return agentSelfTestCheck{Name: "network_readyz", Status: "warning", Message: "Kubernetes readyz check failed through the agent tunnel: " + err.Error()}
	}
	text := strings.TrimSpace(string(body))
	if text == "" || strings.EqualFold(text, "ok") {
		return agentSelfTestCheck{Name: "network_readyz", Status: "passed", Message: "Kubernetes API readyz succeeded through the agent tunnel."}
	}
	return agentSelfTestCheck{Name: "network_readyz", Status: "warning", Message: "Kubernetes API readyz returned: " + truncateDiagnosticMessage(text)}
}

func agentClockSkewDiagnosticCheck(headers map[string]string, now time.Time) agentSelfTestCheck {
	dateValue := firstHeaderValue(headers, "Date")
	if dateValue == "" {
		return agentSelfTestCheck{Name: "clock_skew", Status: "warning", Message: "Kubernetes API response did not include a Date header."}
	}
	remoteTime, err := http.ParseTime(dateValue)
	if err != nil {
		return agentSelfTestCheck{Name: "clock_skew", Status: "warning", Message: "Kubernetes API Date header could not be parsed."}
	}
	skew := now.Sub(remoteTime)
	if skew < 0 {
		skew = -skew
	}
	skewText := skew.Round(time.Second).String()
	switch {
	case skew > 5*time.Minute:
		return agentSelfTestCheck{Name: "clock_skew", Status: "failed", Message: "Management plane and Kubernetes API clocks differ by " + skewText + "."}
	case skew > 2*time.Minute:
		return agentSelfTestCheck{Name: "clock_skew", Status: "warning", Message: "Management plane and Kubernetes API clocks differ by " + skewText + "."}
	default:
		return agentSelfTestCheck{Name: "clock_skew", Status: "passed", Message: "Management plane and Kubernetes API clock skew is " + skewText + "."}
	}
}

func (h *AgentFleetHandler) getLiveJSON(ctx context.Context, clusterID, path string, out any) error {
	body, _, err := h.getLiveRaw(ctx, clusterID, path)
	if err != nil {
		return err
	}
	if len(body) == 0 {
		return nil
	}
	return json.Unmarshal(body, out)
}

func (h *AgentFleetHandler) getLiveRaw(ctx context.Context, clusterID, path string) ([]byte, map[string]string, error) {
	resp, err := h.requester.Do(ctx, clusterID, http.MethodGet, path, nil, requestHeaders(""))
	if err != nil {
		return nil, nil, err
	}
	if err := ensureSuccess(resp); err != nil {
		return nil, resp.Headers, err
	}
	body, err := decodeResponseBody(resp)
	return body, resp.Headers, err
}

func (h *AgentFleetHandler) postLiveJSON(ctx context.Context, clusterID, path string, body any, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	resp, err := h.requester.Do(ctx, clusterID, http.MethodPost, path, raw, requestHeaders("application/json"))
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
	raw, _, err := h.getLiveRaw(ctx, clusterID, path)
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
		lines[i] = redaction.SensitiveLine(line)
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

func firstHeaderValue(headers map[string]string, name string) string {
	if len(headers) == 0 {
		return ""
	}
	for key, value := range headers {
		if strings.EqualFold(key, name) {
			return value
		}
	}
	return ""
}

func truncateDiagnosticMessage(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 200 {
		return value
	}
	return value[:200] + "...[truncated]"
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
	if agent.CompatibilityStatus == "deprecated" {
		return agentUpgradeRecommendation{
			CurrentVersion: agent.AgentVersion,
			Status:         "upgrade_recommended",
			Message:        "Agent is on a deprecated compatibility track; queue an upgrade to the supported agent image.",
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
	currentImage := ""
	if agent.AgentVersion != "" {
		currentImage = targetAgentImage(h.agentImageRepository, agent.AgentVersion)
	}
	rollbackImage := strings.TrimSpace(req.RollbackImage)
	if rollbackImage == "" {
		rollbackImage = currentImage
	}
	strategy := strings.TrimSpace(req.Strategy)
	if strategy == "" {
		strategy = "agent_self_rollout"
	}
	batchSize := req.BatchSize
	if batchSize <= 0 {
		batchSize = 1
	}
	maxUnavailable := req.MaxUnavailable
	if maxUnavailable <= 0 {
		maxUnavailable = 1
	}
	canaryClusterIDs := sanitizeUpgradeCanaryIDs(req.CanaryClusterIDs)
	if len(canaryClusterIDs) == 0 && !cluster.IsLocal {
		canaryClusterIDs = []string{cluster.ID.String()}
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
	if maxUnavailable > batchSize {
		blockers = append(blockers, "max_unavailable cannot be greater than batch_size")
	}
	if rollbackImage == "" {
		blockers = append(blockers, "rollback image could not be inferred; provide rollback_image explicitly")
	}
	profile := agent.PrivilegeProfile
	if profile == "" {
		profile = h.agentUpgradeDefaultProfile
	}
	return agentUpgradePlanResponse{
		ClusterID:        cluster.ID.String(),
		ClusterName:      firstNonEmptyAgentValue(cluster.DisplayName, cluster.Name),
		CurrentVersion:   agent.AgentVersion,
		TargetVersion:    targetVersion,
		CurrentImage:     currentImage,
		TargetImage:      targetImage,
		RollbackImage:    rollbackImage,
		PrivilegeProfile: profile,
		Strategy:         strategy,
		CanaryClusterIDs: canaryClusterIDs,
		BatchSize:        batchSize,
		MaxUnavailable:   maxUnavailable,
		Ready:            len(blockers) == 0,
		Blockers:         blockers,
		PreflightChecks: []string{
			"Agent self-test returns passed or only approved warnings.",
			"Agent tunnel is connected and heartbeat/ping are fresh.",
			"Target image is configured and pullable from the adopted cluster.",
			"Rollback image is known before patching the Deployment.",
			"Canary cluster list is approved for the first rollout batch.",
			"max_unavailable is less than or equal to batch_size.",
		},
		Steps: []string{
			"Queue the upgrade operation in Astronomer.",
			"Upgrade canary clusters first and wait for post-upgrade health checks.",
			"Proceed through batches without exceeding max unavailable agents.",
			"The connected agent patches the astronomer-system/astronomer-agent Deployment to the target image.",
			"The agent reports whether the Deployment patch was accepted by the Kubernetes API.",
			"Confirm the replacement agent pod reconnects and reports the target version in Agent Fleet.",
		},
		PostUpgradeHealthChecks: []string{
			"Replacement agent pod reconnects within the rollout timeout.",
			"Agent Fleet reports the target version and supported compatibility status.",
			"Heartbeat schema version is current and heartbeat/ping freshness checks pass.",
			"Diagnostics self-test has no failed checks.",
			"Argo CD managed-cluster registration remains present.",
		},
		Validation: []string{
			"Agent Fleet status returns connected for the cluster.",
			"Last heartbeat and last ping are both fresh.",
			"Privilege profile is unchanged or intentionally narrowed.",
			"Kubernetes proxy GET /version succeeds through the tunnel.",
		},
		Rollback: []string{
			"Reapply rollback_image if the new agent fails to reconnect.",
			"Keep the current registration token and CA bundle unchanged during rollback.",
			"Collect diagnostics before deleting the failed agent pod if possible.",
		},
	}
}

func sanitizeUpgradeCanaryIDs(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		out = append(out, value)
		seen[value] = true
	}
	return out
}

func respondAgentFleetError(w http.ResponseWriter, r *http.Request, err error) {
	var handlerErr *agentFleetHandlerError
	if errors.As(err, &handlerErr) {
		RespondRequestError(w, r, handlerErr.status, handlerErr.code, handlerErr.message)
		return
	}
	RespondRequestError(w, r, http.StatusInternalServerError, apierror.AgentFleetError, "Agent fleet request failed")
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
		repository = "ghcr.io/alphabravo-oss/astronomer-go-agent"
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
