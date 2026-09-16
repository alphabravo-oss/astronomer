package handler

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	agenttemplate "github.com/alphabravocompany/astronomer-go/deploy/agent"
	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/events"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/google/uuid"
)

type ClusterAgentQuerier interface {
	// GetUserByID resolves the calling user for the superuser gate on the
	// cluster-admin posture report (E3). The production *sqlc.Queries
	// satisfies it; narrow test fakes implement it directly.
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlc.User, error)
	CountClusters(ctx context.Context) (int64, error)
	GetClusterByID(ctx context.Context, id uuid.UUID) (sqlc.Cluster, error)
	ListClusters(ctx context.Context, arg sqlc.ListClustersParams) ([]sqlc.Cluster, error)
	ListConnectionsByCluster(ctx context.Context, arg sqlc.ListConnectionsByClusterParams) ([]sqlc.AgentConnection, error)
	ListLatestConnectionsByClusters(ctx context.Context, clusterIds []uuid.UUID) ([]sqlc.AgentConnection, error)
	ListClusterConditions(ctx context.Context, clusterID uuid.UUID) ([]sqlc.ClusterCondition, error)
	CreateAgentLifecycleOperation(ctx context.Context, arg sqlc.CreateAgentLifecycleOperationParams) (sqlc.AgentLifecycleOperation, error)
	ListAgentLifecycleOperationsByCluster(ctx context.Context, arg sqlc.ListAgentLifecycleOperationsByClusterParams) ([]sqlc.AgentLifecycleOperation, error)
}

type ClusterAgentMutationTx interface {
	ClusterAgentQuerier
	audit.OutboxQuerier
}

type clusterAgentRunTxFunc func(context.Context, func(ClusterAgentMutationTx) error) error

type ClusterAgentHandler struct {
	queries                    ClusterAgentQuerier
	now                        func() time.Time
	agentImageRepository       string
	agentImageTag              string
	agentUpgradeDefaultProfile string
	requester                  K8sRequester
	bus                        *events.Bus
	runTx                      clusterAgentRunTxFunc
}

func (h *ClusterAgentHandler) SetRunTx(runTx clusterAgentRunTxFunc) {
	if h != nil {
		h.runTx = runTx
	}
}

func (h *ClusterAgentHandler) TransactionalAuditWired() bool { return h != nil && h.runTx != nil }

// SetEventBus wires the SSE bus for cluster_agents.changed liveness events
// (P4.5). Optional: fire-and-forget and nil-safe.
func (h *ClusterAgentHandler) SetEventBus(bus *events.Bus) {
	if h == nil {
		return
	}
	h.bus = bus
}

// publishClusterAgentChanged emits the metadata-only cluster_agents.changed event
// after a successful agent-lifecycle write.
func (h *ClusterAgentHandler) publishClusterAgentChanged(clusterID uuid.UUID, opID string) {
	if h == nil {
		return
	}
	events.PublishChanged(h.bus, "cluster_agents", clusterID.String(), opID, map[string]any{"kind": "lifecycle_operation"})
}

func NewClusterAgentHandler(queries ClusterAgentQuerier) *ClusterAgentHandler {
	return &ClusterAgentHandler{
		queries:                    queries,
		now:                        time.Now,
		agentUpgradeDefaultProfile: agenttemplate.PrivilegeProfileViewer,
	}
}

func (h *ClusterAgentHandler) SetAgentUpgradeTarget(repository, tag string) {
	if h == nil {
		return
	}
	h.agentImageRepository = strings.TrimSpace(repository)
	h.agentImageTag = strings.TrimSpace(tag)
}

func (h *ClusterAgentHandler) SetK8sRequester(requester K8sRequester) {
	if h == nil {
		return
	}
	h.requester = requester
}

type clusterAgentResponse struct {
	paging.Response[clusterAgentItem]
	Summary clusterAgentSummary `json:"summary"`
}

type clusterAgentSummary struct {
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
	MaximumSupportedAgentVersion  string         `json:"maximum_supported_agent_version_exclusive"`
	GeneratedAt                   string         `json:"generated_at"`
}

type clusterAgentItem struct {
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
	Agent                 clusterAgentItem             `json:"agent"`
	RecentConnections     []agentConnectionDiagnostic  `json:"recent_connections"`
	Conditions            []clusterConditionDiagnostic `json:"conditions"`
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

type agentUpgradeRecommendation struct {
	CurrentVersion string `json:"current_version,omitempty"`
	Status         string `json:"status"`
	Message        string `json:"message"`
}

// openapi:request AgentUpgradePlanRequest
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
	ClusterID               string                       `json:"cluster_id"`
	ClusterName             string                       `json:"cluster_name"`
	CurrentVersion          string                       `json:"current_version,omitempty"`
	TargetVersion           string                       `json:"target_version"`
	CurrentImage            string                       `json:"current_image,omitempty"`
	TargetImage             string                       `json:"target_image"`
	RollbackImage           string                       `json:"rollback_image,omitempty"`
	PrivilegeProfile        string                       `json:"privilege_profile"`
	AgentOverrides          agenttemplate.AgentOverrides `json:"agent_overrides"`
	ConfigurationDigest     string                       `json:"configuration_digest"`
	PlanDigest              string                       `json:"plan_digest"`
	Strategy                string                       `json:"strategy"`
	CanaryClusterIDs        []string                     `json:"canary_cluster_ids,omitempty"`
	BatchSize               int32                        `json:"batch_size"`
	MaxUnavailable          int32                        `json:"max_unavailable"`
	Ready                   bool                         `json:"ready"`
	Blockers                []string                     `json:"blockers,omitempty"`
	PreflightChecks         []string                     `json:"preflight_checks"`
	Steps                   []string                     `json:"steps"`
	PostUpgradeHealthChecks []string                     `json:"post_upgrade_health_checks"`
	Validation              []string                     `json:"validation"`
	Rollback                []string                     `json:"rollback"`
}

type agentUpgradeOperationResponse struct {
	Operation agentLifecycleOperationResponse `json:"operation"`
	Plan      agentUpgradePlanResponse        `json:"plan"`
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

type clusterAgentHandlerError struct {
	status  int
	code    string
	message string
}

func (e *clusterAgentHandlerError) Error() string {
	return e.message
}
