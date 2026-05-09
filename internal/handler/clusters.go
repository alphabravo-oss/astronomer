package handler

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/clustermetrics"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"k8s.io/client-go/kubernetes"
	metricsv "k8s.io/metrics/pkg/client/clientset/versioned"
	"sigs.k8s.io/yaml"
)

// rfc1123ClusterName matches the same naming rules Rancher applies to imported
// cluster CRDs: lowercase letters/digits/hyphens, start+end alphanumeric,
// 1–63 chars. Mirrors k8s.io/apimachinery/pkg/util/validation.IsDNS1123Label.
var rfc1123ClusterName = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)

// validClusterName enforces the RFC-1123 label rules.
func validClusterName(s string) bool {
	return rfc1123ClusterName.MatchString(s)
}

// ClusterQuerier abstracts the cluster-related database queries needed by ClusterHandler.
type ClusterQuerier interface {
	GetClusterByID(ctx context.Context, id uuid.UUID) (sqlc.Cluster, error)
	GetClusterByName(ctx context.Context, name string) (sqlc.Cluster, error)
	ListClusters(ctx context.Context, arg sqlc.ListClustersParams) ([]sqlc.Cluster, error)
	CreateCluster(ctx context.Context, arg sqlc.CreateClusterParams) (sqlc.Cluster, error)
	UpdateCluster(ctx context.Context, arg sqlc.UpdateClusterParams) (sqlc.Cluster, error)
	DeleteCluster(ctx context.Context, id uuid.UUID) error
	CountClusters(ctx context.Context) (int64, error)
	// Health
	GetClusterHealthStatus(ctx context.Context, clusterID uuid.UUID) (sqlc.ClusterHealthStatus, error)
	// Registration
	CreateClusterRegistrationToken(ctx context.Context, arg sqlc.CreateClusterRegistrationTokenParams) (sqlc.ClusterRegistrationToken, error)
	GetRegistrationTokenByToken(ctx context.Context, token string) (sqlc.ClusterRegistrationToken, error)
	MarkRegistrationTokenUsed(ctx context.Context, id uuid.UUID) error
	// Registry config
	GetClusterRegistryConfig(ctx context.Context, clusterID uuid.UUID) (sqlc.ClusterRegistryConfig, error)
	UpsertClusterRegistryConfig(ctx context.Context, arg sqlc.UpsertClusterRegistryConfigParams) (sqlc.ClusterRegistryConfig, error)
}

// EventPublisher is the minimal contract ClusterHandler depends on for
// fan-out of cluster.* lifecycle events. Declared here (rather than imported
// from internal/events) so this package stays free of an events dependency
// — the cluster handler is a hot path and we don't want a transitive import
// cycle. *events.Bus implements this interface naturally.
type EventPublisher interface {
	Publish(eventType string, data any)
}

// ClusterHandler handles cluster endpoints.
type ClusterHandler struct {
	queries ClusterQuerier
	// metrics is an optional, lazily-wired aggregator that enriches list/get
	// responses with CPU%, memory%, and pod_count. When nil (or before
	// SetMetrics* is called) the handler returns zeros for those fields —
	// this is intentional: the dashboard renders zeros gracefully and we'd
	// rather degrade than 500 the cluster list when metrics-server is
	// unreachable.
	metrics *clustermetrics.Provider
	// publisher fans out cluster.created / cluster.updated / cluster.deleted
	// events. Optional and nil-safe: when not wired the CRUD path simply
	// doesn't notify SSE subscribers.
	publisher EventPublisher
}

// NewClusterHandler creates a new cluster handler.
func NewClusterHandler(queries ClusterQuerier) *ClusterHandler {
	return &ClusterHandler{queries: queries, metrics: clustermetrics.NewProvider()}
}

// SetMetricsLocalClient wires the in-process kubernetes clientset used to
// gather metrics for the local (is_local=true) cluster row. Metrics-server
// access is optional; pass a nil metricsClient when it isn't installed and
// CPU/memory percentages will simply remain zero.
//
// The setter pattern lets the wiring layer (cmd/server) inject the clients
// without ClusterHandler taking a hard dependency on rest.InClusterConfig in
// its constructor — which would break unit tests and offline `go build`.
func (h *ClusterHandler) SetMetricsLocalClient(cs *kubernetes.Clientset, metricsClient metricsv.Interface) {
	if h == nil || h.metrics == nil {
		return
	}
	h.metrics.SetLocalClient(cs, metricsClient)
}

// SetMetricsRequester wires the tunnel-backed K8sRequester used to gather
// metrics for non-local clusters. The handler-level K8sRequester returns
// protocol.K8sResponsePayload; the clustermetrics package uses a smaller
// transport-agnostic shape, so this method bridges between them.
func (h *ClusterHandler) SetMetricsRequester(r K8sRequester) {
	if h == nil || h.metrics == nil || r == nil {
		return
	}
	h.metrics.SetRemoteRequester(metricsRequesterAdapter{r: r})
}

// MetricsProvider returns the clustermetrics provider this handler uses.
// Exposed so the metrics publisher (which fans CPU/mem snapshots out to
// SSE subscribers) can share the same cache the dashboard list endpoint
// already populates — avoids stampeding the agent tunnel with parallel
// independent metric reads.
func (h *ClusterHandler) MetricsProvider() *clustermetrics.Provider {
	if h == nil {
		return nil
	}
	return h.metrics
}

// SetEventPublisher wires the SSE bus so cluster CRUD operations fan out
// to subscribers. Set once at startup; nil-safe.
func (h *ClusterHandler) SetEventPublisher(p EventPublisher) {
	if h == nil {
		return
	}
	h.publisher = p
}

// publishEvent is a nil-safe wrapper around the optional publisher.
func (h *ClusterHandler) publishEvent(eventType string, data any) {
	if h == nil || h.publisher == nil {
		return
	}
	h.publisher.Publish(eventType, data)
}

// clusterWithMetrics is the JSON shape returned to the frontend dashboard
// card. It embeds sqlc.Cluster so every existing field (id, name, status,
// node_count, ...) is preserved verbatim, and tacks on three computed
// scalars the UI consumes.
type clusterWithMetrics struct {
	sqlc.Cluster
	CPUPercentage    float64 `json:"cpu_percentage"`
	MemoryPercentage float64 `json:"memory_percentage"`
	PodCount         int     `json:"pod_count"`
}

// metricsRequesterAdapter bridges the handler-level K8sRequester (which
// returns *protocol.K8sResponsePayload with a base64-encoded body) into the
// transport-agnostic shape consumed by clustermetrics. Decoding the body
// here keeps the clustermetrics package free of protocol/tunnel imports.
type metricsRequesterAdapter struct{ r K8sRequester }

func (a metricsRequesterAdapter) Do(ctx context.Context, clusterID, method, path string, body []byte, headers map[string]string) (*clustermetrics.RawResponse, error) {
	resp, err := a.r.Do(ctx, clusterID, method, path, body, headers)
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, fmt.Errorf("nil response")
	}
	decoded, err := decodeResponseBody(resp)
	if err != nil {
		return nil, err
	}
	return &clustermetrics.RawResponse{StatusCode: resp.StatusCode, Body: decoded}, nil
}

// enrichCluster copies the sqlc.Cluster row plus the latest cached metrics
// snapshot into the wire-format struct. A short request-scoped timeout
// applies because List enriches every cluster sequentially — a slow agent
// would otherwise stall the entire response.
func (h *ClusterHandler) enrichCluster(ctx context.Context, c sqlc.Cluster) clusterWithMetrics {
	out := clusterWithMetrics{Cluster: c}
	if h.metrics == nil {
		return out
	}
	// 5s per cluster is generous for cache hits (~instant) and bounds the
	// worst case when a cache miss must round-trip through the agent.
	mctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	snap := h.metrics.Get(mctx, c.ID.String(), c.IsLocal)
	out.CPUPercentage = snap.CPUPercentage
	out.MemoryPercentage = snap.MemoryPercentage
	out.PodCount = snap.PodCount
	return out
}

// --- Request / Response types ---

// CreateClusterRequest represents the request body for creating a cluster.
type CreateClusterRequest struct {
	Name         string `json:"name"`
	DisplayName  string `json:"display_name"`
	Description  string `json:"description"`
	Environment  string `json:"environment"`
	Region       string `json:"region"`
	Provider     string `json:"provider"`
	Distribution string `json:"distribution"`
}

// UpdateClusterRequest represents the request body for updating a cluster.
type UpdateClusterRequest struct {
	DisplayName string          `json:"display_name"`
	Description string          `json:"description"`
	Environment string          `json:"environment"`
	Region      string          `json:"region"`
	Labels      json.RawMessage `json:"labels"`
	Annotations json.RawMessage `json:"annotations"`
}

// UpdateRegistryConfigRequest represents the request body for upserting registry config.
type UpdateRegistryConfigRequest struct {
	PrivateRegistryUrl string `json:"private_registry_url"`
	RegistryUsername   string `json:"registry_username"`
	RegistryPassword   string `json:"registry_password"`
	Insecure           bool   `json:"insecure"`
	CaBundle           string `json:"ca_bundle"`
}

// --- Endpoints ---

// List handles GET /api/v1/clusters/.
func (h *ClusterHandler) List(w http.ResponseWriter, r *http.Request) {
	limit := int32(queryInt(r, "limit", 20))
	offset := int32(queryInt(r, "offset", 0))

	clusters, err := h.queries.ListClusters(r.Context(), sqlc.ListClustersParams{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "list_error", "Failed to list clusters")
		return
	}

	total, err := h.queries.CountClusters(r.Context())
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "count_error", "Failed to count clusters")
		return
	}

	enriched := make([]clusterWithMetrics, 0, len(clusters))
	for _, c := range clusters {
		enriched = append(enriched, h.enrichCluster(r.Context(), c))
	}
	RespondPaginated(w, r, enriched, total)
}

// Create handles POST /api/v1/clusters/.
func (h *ClusterHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req CreateClusterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}

	if req.Name == "" {
		RespondError(w, http.StatusBadRequest, "validation_error", "Cluster name is required")
		return
	}
	if !validClusterName(req.Name) {
		RespondError(w, http.StatusBadRequest, "validation_error",
			"Cluster name must be RFC-1123 (lowercase letters, digits, hyphens; start and end with an alphanumeric; max 63 chars)")
		return
	}

	cluster, err := h.queries.CreateCluster(r.Context(), sqlc.CreateClusterParams{
		Name:         req.Name,
		DisplayName:  req.DisplayName,
		Description:  req.Description,
		Environment:  req.Environment,
		Region:       req.Region,
		Provider:     req.Provider,
		Distribution: req.Distribution,
		CreatedByID:  currentUserUUID(r),
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "create_error", "Failed to create cluster")
		return
	}

	h.publishEvent("cluster.created", map[string]any{
		"cluster_id":   cluster.ID.String(),
		"name":         cluster.Name,
		"display_name": cluster.DisplayName,
		"status":       cluster.Status,
	})

	recordAudit(r, h.queries, "cluster.create", "cluster", cluster.ID.String(), cluster.Name, map[string]any{
		"environment":  req.Environment,
		"region":       req.Region,
		"provider":     req.Provider,
		"distribution": req.Distribution,
	})

	RespondJSON(w, http.StatusCreated, cluster)
}

// Get handles GET /api/v1/clusters/{id}/.
func (h *ClusterHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid cluster ID")
		return
	}

	cluster, err := h.queries.GetClusterByID(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Cluster not found")
		return
	}

	RespondJSON(w, http.StatusOK, h.enrichCluster(r.Context(), cluster))
}

// Update handles PUT /api/v1/clusters/{id}/.
func (h *ClusterHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid cluster ID")
		return
	}

	var req UpdateClusterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}

	labels := req.Labels
	if labels == nil {
		labels = json.RawMessage(`{}`)
	}
	annotations := req.Annotations
	if annotations == nil {
		annotations = json.RawMessage(`{}`)
	}

	cluster, err := h.queries.UpdateCluster(r.Context(), sqlc.UpdateClusterParams{
		ID:          id,
		DisplayName: req.DisplayName,
		Description: req.Description,
		Environment: req.Environment,
		Region:      req.Region,
		Labels:      labels,
		Annotations: annotations,
	})
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Cluster not found")
		return
	}

	h.publishEvent("cluster.updated", map[string]any{
		"cluster_id":   cluster.ID.String(),
		"name":         cluster.Name,
		"display_name": cluster.DisplayName,
		"status":       cluster.Status,
	})

	recordAudit(r, h.queries, "cluster.update", "cluster", cluster.ID.String(), cluster.Name, map[string]any{
		"display_name": req.DisplayName,
		"description":  req.Description,
		"environment":  req.Environment,
		"region":       req.Region,
	})

	RespondJSON(w, http.StatusOK, cluster)
}

// Delete handles DELETE /api/v1/clusters/{id}/.
func (h *ClusterHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid cluster ID")
		return
	}

	// Capture the row's friendly name BEFORE the delete so the audit row has
	// something humans can recognise; it's allowed to fail (e.g. id is bogus,
	// row already gone) — DeleteCluster will surface the canonical error.
	clusterName := ""
	if existing, lookupErr := h.queries.GetClusterByID(r.Context(), id); lookupErr == nil {
		clusterName = existing.Name
	}

	if err := h.queries.DeleteCluster(r.Context(), id); err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Cluster not found")
		return
	}

	h.publishEvent("cluster.deleted", map[string]any{
		"cluster_id": id.String(),
	})

	recordAudit(r, h.queries, "cluster.delete", "cluster", id.String(), clusterName, nil)

	w.WriteHeader(http.StatusNoContent)
}

// GetHealth handles GET /api/v1/clusters/{id}/health/.
func (h *ClusterHandler) GetHealth(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid cluster ID")
		return
	}

	health, err := h.queries.GetClusterHealthStatus(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Health status not found for cluster")
		return
	}

	RespondJSON(w, http.StatusOK, health)
}

// GenerateRegistrationToken handles POST /api/v1/clusters/{id}/register/.
func (h *ClusterHandler) GenerateRegistrationToken(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid cluster ID")
		return
	}

	// Verify cluster exists.
	if _, err := h.queries.GetClusterByID(r.Context(), id); err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Cluster not found")
		return
	}

	// Generate a random registration token.
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		RespondError(w, http.StatusInternalServerError, "token_error", "Failed to generate registration token")
		return
	}
	tokenStr := base64.URLEncoding.EncodeToString(b)

	token, err := h.queries.CreateClusterRegistrationToken(r.Context(), sqlc.CreateClusterRegistrationTokenParams{
		ClusterID: id,
		Token:     tokenStr,
		ExpiresAt: time.Now().Add(24 * time.Hour),
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "create_error", "Failed to create registration token")
		return
	}

	recordAudit(r, h.queries, "cluster.register_token", "cluster", id.String(), "", map[string]any{
		"token_id":   token.ID.String(),
		"expires_at": token.ExpiresAt.UTC().Format(time.RFC3339),
	})

	RespondJSON(w, http.StatusCreated, token)
}

// GetRegistryConfig handles GET /api/v1/clusters/{id}/registry/.
func (h *ClusterHandler) GetRegistryConfig(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid cluster ID")
		return
	}

	config, err := h.queries.GetClusterRegistryConfig(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Registry config not found for cluster")
		return
	}

	RespondJSON(w, http.StatusOK, config)
}

// GetManifest handles GET /api/v1/clusters/{id}/manifest/.
// Returns the agent install manifest as raw YAML for curl-based installation.
func (h *ClusterHandler) GetManifest(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid cluster ID")
		return
	}
	cluster, err := h.queries.GetClusterByID(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Cluster not found")
		return
	}

	// Generate a fresh registration token.
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		RespondError(w, http.StatusInternalServerError, "token_error", "Failed to generate registration token")
		return
	}
	tokenStr := base64.URLEncoding.EncodeToString(b)
	token, err := h.queries.CreateClusterRegistrationToken(r.Context(), sqlc.CreateClusterRegistrationTokenParams{
		ClusterID: id,
		Token:     tokenStr,
		ExpiresAt: time.Now().Add(24 * time.Hour),
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "create_error", "Failed to create registration token")
		return
	}

	recordAudit(r, h.queries, "cluster.register_token", "cluster", id.String(), cluster.Name, map[string]any{
		"token_id":   token.ID.String(),
		"source":     "manifest_download",
		"expires_at": token.ExpiresAt.UTC().Format(time.RFC3339),
	})

	// TODO: render the actual install.yaml.template from astronomer/agent/manifests
	// once the template renderer is ported. For now we emit a minimal placeholder
	// manifest sufficient for the UI to render and the curl installer to consume.
	manifest := renderAgentInstallManifest(cluster, token.Token, agentServerURL(r))

	w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="astronomer-agent-%s.yaml"`, cluster.Name))
	_, _ = w.Write([]byte(manifest))
}

// GetKubeconfig handles GET /api/v1/clusters/{id}/kubeconfig/.
// Generates a kubeconfig snippet for direct API access using stored CA + URL.
func (h *ClusterHandler) GetKubeconfig(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid cluster ID")
		return
	}
	cluster, err := h.queries.GetClusterByID(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Cluster not found")
		return
	}
	if cluster.ApiServerUrl == "" {
		RespondError(w, http.StatusNotFound, "not_found", "Cluster API server URL is not yet available")
		return
	}
	userEmail := authenticatedEmail(r)
	kubeconfig := buildDirectKubeconfig(cluster, userEmail)
	RespondJSON(w, http.StatusOK, kubeconfig)
}

// GenerateKubeconfig handles POST /api/v1/clusters/{id}/generate-kubeconfig/.
// Returns a kubeconfig that routes through the Astronomer proxy.
func (h *ClusterHandler) GenerateKubeconfig(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid cluster ID")
		return
	}
	cluster, err := h.queries.GetClusterByID(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Cluster not found")
		return
	}
	serverURL := agentServerURL(r)
	userEmail := authenticatedEmail(r)
	kubeconfig := buildProxyKubeconfig(cluster, userEmail, serverURL)
	yamlBytes, err := yaml.Marshal(kubeconfig)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "render_error", "Failed to render kubeconfig")
		return
	}
	w.Header().Set("Content-Type", "application/x-yaml")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="kubeconfig-%s.yaml"`, cluster.Name))
	_, _ = w.Write(yamlBytes)
}

// PreviewKubeconfig handles GET /api/v1/clusters/{id}/kubeconfig-preview/.
// Returns the proxy kubeconfig as JSON for UI display.
func (h *ClusterHandler) PreviewKubeconfig(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid cluster ID")
		return
	}
	cluster, err := h.queries.GetClusterByID(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Cluster not found")
		return
	}
	serverURL := agentServerURL(r)
	userEmail := authenticatedEmail(r)
	kubeconfig := buildProxyKubeconfig(cluster, userEmail, serverURL)
	RespondJSON(w, http.StatusOK, kubeconfig)
}

// GetMetrics handles GET /api/v1/clusters/{id}/metrics/.
// Returns CPU/memory/pod aggregate metrics derived from health snapshots.
func (h *ClusterHandler) GetMetrics(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid cluster ID")
		return
	}
	cluster, err := h.queries.GetClusterByID(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Cluster not found")
		return
	}
	isConnected := cluster.LastHeartbeat.Valid && time.Since(cluster.LastHeartbeat.Time) < 5*time.Minute
	metrics := map[string]any{
		"cluster_id":         cluster.ID.String(),
		"cluster_name":       cluster.Name,
		"status":             cluster.Status,
		"is_connected":       isConnected,
		"kubernetes_version": cluster.KubernetesVersion,
		"node_count":         cluster.NodeCount,
		"agent_version":      cluster.AgentVersion,
	}
	if cluster.LastHeartbeat.Valid {
		metrics["last_heartbeat"] = cluster.LastHeartbeat.Time.UTC().Format(time.RFC3339)
	} else {
		metrics["last_heartbeat"] = nil
	}
	if health, err := h.queries.GetClusterHealthStatus(r.Context(), id); err == nil {
		metrics["cpu_usage_percent"] = health.CpuUsagePercent
		metrics["memory_usage_percent"] = health.MemoryUsagePercent
		metrics["pod_count"] = health.PodCount
		metrics["conditions"] = health.Conditions
		metrics["last_health_check"] = health.LastCheck.UTC().Format(time.RFC3339)
	}
	RespondJSON(w, http.StatusOK, metrics)
}

// GetMetricsSummary handles GET /api/v1/clusters/{id}/metrics/summary/.
// Returns a metrics summary using cached health data.
func (h *ClusterHandler) GetMetricsSummary(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid cluster ID")
		return
	}
	cluster, err := h.queries.GetClusterByID(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Cluster not found")
		return
	}
	nodeCount := int(cluster.NodeCount)
	cpuUsage := 0.0
	memUsage := 0.0
	podCount := 0
	if health, err := h.queries.GetClusterHealthStatus(r.Context(), id); err == nil {
		cpuUsage = health.CpuUsagePercent
		memUsage = health.MemoryUsagePercent
		podCount = int(health.PodCount)
	}
	podCapacity := 110 * nodeCount
	if podCapacity == 0 {
		podCapacity = 110
	}
	summary := map[string]any{
		"cpu_usage":         cpuUsage,
		"cpu_capacity":      100,
		"cpu_percentage":    cpuUsage,
		"memory_usage":      memUsage,
		"memory_capacity":   100,
		"memory_percentage": memUsage,
		"pod_count":         podCount,
		"pod_capacity":      podCapacity,
		"node_count":        nodeCount,
		"network_receive":   0,
		"network_transmit":  0,
		"disk_usage":        0,
		"disk_capacity":     0,
	}
	RespondJSON(w, http.StatusOK, summary)
}

func buildDirectKubeconfig(cluster sqlc.Cluster, userEmail string) map[string]any {
	if userEmail == "" {
		userEmail = "user"
	}
	return map[string]any{
		"apiVersion": "v1",
		"kind":       "Config",
		"clusters": []map[string]any{
			{
				"cluster": map[string]any{
					"server":                     cluster.ApiServerUrl,
					"certificate-authority-data": cluster.CaCertificate,
				},
				"name": cluster.Name,
			},
		},
		"contexts": []map[string]any{
			{
				"context": map[string]any{
					"cluster": cluster.Name,
					"user":    userEmail,
				},
				"name": cluster.Name + "-context",
			},
		},
		"current-context": cluster.Name + "-context",
		"users": []map[string]any{
			{
				"name": userEmail,
				"user": map[string]any{
					"token": "REPLACE_WITH_TOKEN",
				},
			},
		},
	}
}

func buildProxyKubeconfig(cluster sqlc.Cluster, userEmail, serverURL string) map[string]any {
	if userEmail == "" {
		userEmail = "user"
	}
	proxyURL := fmt.Sprintf("%s/api/v1/clusters/%s/k8s", serverURL, cluster.ID.String())
	return map[string]any{
		"apiVersion": "v1",
		"kind":       "Config",
		"clusters": []map[string]any{
			{
				"cluster": map[string]any{
					"server":                   proxyURL,
					"insecure-skip-tls-verify": false,
				},
				"name": cluster.Name,
			},
		},
		"contexts": []map[string]any{
			{
				"context": map[string]any{
					"cluster": cluster.Name,
					"user":    userEmail,
				},
				"name": cluster.Name + "-context",
			},
		},
		"current-context": cluster.Name + "-context",
		"users": []map[string]any{
			{
				"name": userEmail,
				"user": map[string]any{
					"token": "REPLACE_WITH_API_TOKEN",
				},
			},
		},
	}
}

func renderAgentInstallManifest(cluster sqlc.Cluster, token, serverURL string) string {
	// TODO: replace this stub with the real template renderer once the
	// agent install template (astronomer/agent/manifests/install.yaml.template)
	// is ported to Go.
	return fmt.Sprintf(`# Astronomer agent install manifest (placeholder)
# cluster: %s
# server: %s
# token: %s
apiVersion: v1
kind: Namespace
metadata:
  name: astronomer-agent
---
apiVersion: v1
kind: Secret
metadata:
  name: astronomer-agent-credentials
  namespace: astronomer-agent
type: Opaque
stringData:
  cluster-id: %s
  registration-token: %s
  server-url: %s
`, cluster.Name, serverURL, token, cluster.ID.String(), token, serverURL)
}

func agentServerURL(r *http.Request) string {
	scheme := "https"
	if r.TLS == nil && r.Header.Get("X-Forwarded-Proto") == "" {
		scheme = "http"
	}
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		scheme = proto
	}
	host := r.Host
	if forwarded := r.Header.Get("X-Forwarded-Host"); forwarded != "" {
		host = forwarded
	}
	return fmt.Sprintf("%s://%s", scheme, host)
}

func authenticatedEmail(r *http.Request) string {
	if user, ok := middleware.GetAuthenticatedUser(r.Context()); ok && user != nil {
		return user.Email
	}
	return ""
}

// UpdateRegistryConfig handles PUT /api/v1/clusters/{id}/registry/.
func (h *ClusterHandler) UpdateRegistryConfig(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid cluster ID")
		return
	}

	var req UpdateRegistryConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}

	config, err := h.queries.UpsertClusterRegistryConfig(r.Context(), sqlc.UpsertClusterRegistryConfigParams{
		ClusterID:          id,
		PrivateRegistryUrl: req.PrivateRegistryUrl,
		RegistryUsername:   req.RegistryUsername,
		RegistryPassword:   req.RegistryPassword,
		Insecure:           req.Insecure,
		CaBundle:           req.CaBundle,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "update_error", "Failed to update registry config")
		return
	}

	// Don't surface the password / CA in the audit detail; recordAudit will
	// redact the well-known keys but we keep the explicit map narrow anyway.
	recordAudit(r, h.queries, "cluster.update", "cluster", id.String(), "", map[string]any{
		"private_registry_url": req.PrivateRegistryUrl,
		"registry_username":    req.RegistryUsername,
		"insecure":             req.Insecure,
	})

	RespondJSON(w, http.StatusOK, config)
}
