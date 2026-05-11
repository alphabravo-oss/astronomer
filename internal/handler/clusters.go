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

	agenttemplate "github.com/alphabravocompany/astronomer-go/deploy/agent"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/clustermetrics"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgtype"
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
	// Cluster decommission. The DELETE handler no longer hard-deletes the
	// row; it inserts a cluster_decommissions row and enqueues the worker
	// reconciler. GetLatest backs the GET /decommission status endpoint.
	CreateClusterDecommission(ctx context.Context, arg sqlc.CreateClusterDecommissionParams) (sqlc.ClusterDecommission, error)
	GetLatestClusterDecommissionByCluster(ctx context.Context, clusterID uuid.UUID) (sqlc.ClusterDecommission, error)
	// Health
	GetClusterHealthStatus(ctx context.Context, clusterID uuid.UUID) (sqlc.ClusterHealthStatus, error)
	ListClusterConditions(ctx context.Context, clusterID uuid.UUID) ([]sqlc.ClusterCondition, error)
	// Registration
	CreateClusterRegistrationToken(ctx context.Context, arg sqlc.CreateClusterRegistrationTokenParams) (sqlc.ClusterRegistrationToken, error)
	GetRegistrationTokenByToken(ctx context.Context, token string) (sqlc.ClusterRegistrationToken, error)
	MarkRegistrationTokenUsed(ctx context.Context, id uuid.UUID) error
	// Registry config
	GetClusterRegistryConfig(ctx context.Context, clusterID uuid.UUID) (sqlc.ClusterRegistryConfig, error)
	UpsertClusterRegistryConfig(ctx context.Context, arg sqlc.UpsertClusterRegistryConfigParams) (sqlc.ClusterRegistryConfig, error)
	DeleteClusterRegistryConfig(ctx context.Context, clusterID uuid.UUID) error
}

// EventPublisher is the minimal contract ClusterHandler depends on for
// fan-out of cluster.* lifecycle events. Declared here (rather than imported
// from internal/events) so this package stays free of an events dependency
// — the cluster handler is a hot path and we don't want a transitive import
// cycle. *events.Bus implements this interface naturally.
type EventPublisher interface {
	Publish(eventType string, data any)
}

// ClusterDecommissionEnqueuer abstracts the asynq client surface the Delete
// handler needs. *asynq.Client satisfies this interface natively; tests can
// supply a stub. Nil-safe: when not wired, the handler still creates the
// cluster_decommissions row but the worker reconciler only fires via the
// periodic sweep instead of the immediate enqueue.
type ClusterDecommissionEnqueuer interface {
	Enqueue(task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error)
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
	// decommissionQueue is the asynq client used to enqueue
	// cluster_decommission tasks from the DELETE handler. Optional —
	// when nil, the row is still inserted but the worker doesn't pick it up
	// until the periodic sweep runs (slower path).
	decommissionQueue ClusterDecommissionEnqueuer
	agentImage        string
}

// NewClusterHandler creates a new cluster handler.
func NewClusterHandler(queries ClusterQuerier) *ClusterHandler {
	return &ClusterHandler{
		queries:    queries,
		metrics:    clustermetrics.NewProvider(),
		agentImage: "ghcr.io/alphabravocompany/astronomer-go-agent:latest",
	}
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

func (h *ClusterHandler) SetAgentImage(repository, tag string) {
	if h == nil {
		return
	}
	if repository == "" {
		repository = "ghcr.io/alphabravocompany/astronomer-go-agent"
	}
	if tag == "" {
		tag = "latest"
	}
	h.agentImage = repository + ":" + tag
}

// SetEventPublisher wires the SSE bus so cluster CRUD operations fan out
// to subscribers. Set once at startup; nil-safe.
func (h *ClusterHandler) SetEventPublisher(p EventPublisher) {
	if h == nil {
		return
	}
	h.publisher = p
}

// SetDecommissionQueue wires the asynq client used by the DELETE handler to
// schedule the cluster_decommission reconciler. Optional: nil means the
// handler still records the cluster_decommissions row, but the worker only
// picks it up via the periodic sweep.
func (h *ClusterHandler) SetDecommissionQueue(q ClusterDecommissionEnqueuer) {
	if h == nil {
		return
	}
	h.decommissionQueue = q
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

// DecommissionPhaseStatus is one entry in the decommission status response.
// Mirrors the worker.tasks.phaseRecord shape so the frontend can render a
// per-phase progress indicator (when it eventually picks up the API).
type DecommissionPhaseStatus struct {
	Name        string         `json:"name"`
	Status      string         `json:"status"`
	StartedAt   string         `json:"started_at,omitempty"`
	CompletedAt string         `json:"completed_at,omitempty"`
	Error       string         `json:"error,omitempty"`
	Detail      map[string]any `json:"detail,omitempty"`
}

// DecommissionStatusResponse is the JSON body returned from
// GET /api/v1/clusters/{id}/decommission/ and POST .../decommission/ (the
// 202-Accepted enqueue path).
type DecommissionStatusResponse struct {
	DecommissionID string                    `json:"decommission_id"`
	ClusterID      string                    `json:"cluster_id"`
	ClusterName    string                    `json:"cluster_name"`
	Status         string                    `json:"status"`
	Attempts       int32                     `json:"attempts"`
	StartedAt      string                    `json:"started_at,omitempty"`
	CompletedAt    string                    `json:"completed_at,omitempty"`
	LastError      string                    `json:"last_error,omitempty"`
	Phases         []DecommissionPhaseStatus `json:"phases"`
	StatusURL      string                    `json:"status_url"`
}

// phaseOrder is the canonical order phases are rendered in the API response.
// We keep this in lockstep with the reconciler's execution order so the UI
// can render a left-to-right progress bar.
var phaseOrder = []string{
	tasks.PhaseCleanupManagedSide,
	tasks.PhaseRevokeAgentToken,
	tasks.PhaseArchiveAudit,
	tasks.PhaseDeleteDependents,
	tasks.PhaseTombstoneCluster,
}

func formatPhases(raw json.RawMessage) []DecommissionPhaseStatus {
	if len(raw) == 0 {
		return formatEmptyPhases()
	}
	type phaseRecord struct {
		Status      string         `json:"status"`
		StartedAt   time.Time      `json:"started_at,omitempty"`
		CompletedAt time.Time      `json:"completed_at,omitempty"`
		Error       string         `json:"error,omitempty"`
		Detail      map[string]any `json:"detail,omitempty"`
	}
	parsed := map[string]phaseRecord{}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return formatEmptyPhases()
	}
	out := make([]DecommissionPhaseStatus, 0, len(phaseOrder))
	for _, name := range phaseOrder {
		rec, ok := parsed[name]
		entry := DecommissionPhaseStatus{Name: name, Status: "pending"}
		if ok {
			entry.Status = rec.Status
			if !rec.StartedAt.IsZero() {
				entry.StartedAt = rec.StartedAt.UTC().Format(time.RFC3339)
			}
			if !rec.CompletedAt.IsZero() {
				entry.CompletedAt = rec.CompletedAt.UTC().Format(time.RFC3339)
			}
			entry.Error = rec.Error
			entry.Detail = rec.Detail
		}
		out = append(out, entry)
	}
	return out
}

func formatEmptyPhases() []DecommissionPhaseStatus {
	out := make([]DecommissionPhaseStatus, 0, len(phaseOrder))
	for _, name := range phaseOrder {
		out = append(out, DecommissionPhaseStatus{Name: name, Status: "pending"})
	}
	return out
}

func renderDecommission(row sqlc.ClusterDecommission, statusURL string) DecommissionStatusResponse {
	out := DecommissionStatusResponse{
		DecommissionID: row.ID.String(),
		ClusterID:      row.ClusterID.String(),
		ClusterName:    row.ClusterName,
		Status:         row.Status,
		Attempts:       row.Attempts,
		LastError:      row.LastError,
		Phases:         formatPhases(row.Phases),
		StatusURL:      statusURL,
	}
	if row.StartedAt.Valid {
		out.StartedAt = row.StartedAt.Time.UTC().Format(time.RFC3339)
	}
	if row.CompletedAt.Valid {
		out.CompletedAt = row.CompletedAt.Time.UTC().Format(time.RFC3339)
	}
	return out
}

// Delete handles DELETE /api/v1/clusters/{id}/.
//
// Previously this hard-deleted the cluster row, leaving residue (agent WS
// tunnel still connected until timeout, managed-side resources still
// running, audit_log rows orphaned, registration tokens not revoked).
// Now the handler inserts a cluster_decommissions row and enqueues the
// reconciler — the worker walks the cleanup phases and tombstones the
// cluster row at the end. The endpoint returns 202 Accepted with the
// decommission ID + a poll URL.
//
// Idempotent: re-DELETE on a cluster with an in-flight decommission returns
// the existing row's status (202 again) rather than creating a duplicate.
func (h *ClusterHandler) Delete(w http.ResponseWriter, r *http.Request) {
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
	if cluster.IsLocal {
		// The local cluster represents the host this server itself runs in;
		// decommissioning it would tear down the management plane. Refuse.
		RespondError(w, http.StatusForbidden, "forbidden", "Cannot decommission the local cluster")
		return
	}

	// Idempotency: if there's already an in-flight or succeeded decommission
	// for this cluster, return its status rather than creating a duplicate.
	if existing, lookupErr := h.queries.GetLatestClusterDecommissionByCluster(r.Context(), id); lookupErr == nil {
		if existing.Status == "pending" || existing.Status == "running" || existing.Status == "succeeded" {
			statusURL := fmt.Sprintf("/api/v1/clusters/%s/decommission/", id.String())
			RespondJSON(w, http.StatusAccepted, renderDecommission(existing, statusURL))
			return
		}
		// `failed` → fall through and create a fresh decommission row; the
		// previous attempt remains in the DB for forensics.
	}

	requestedBy := pgtype.UUID{}
	if userID := currentUserUUID(r); userID.Valid {
		requestedBy = userID
	}

	row, err := h.queries.CreateClusterDecommission(r.Context(), sqlc.CreateClusterDecommissionParams{
		ClusterID:     id,
		RequestedByID: requestedBy,
		ClusterName:   cluster.Name,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "create_decommission_failed", "Failed to enqueue cluster decommission")
		return
	}

	if h.decommissionQueue != nil {
		task, terr := tasks.NewClusterDecommissionTask(row.ID)
		if terr == nil {
			// Best-effort enqueue. Errors are logged on the asynq side but
			// the periodic sweep will pick the row up regardless, so we
			// don't fail the request when redis is briefly unavailable.
			_, _ = h.decommissionQueue.Enqueue(task)
		}
	}

	h.publishEvent("cluster.decommission_enqueued", map[string]any{
		"cluster_id":      id.String(),
		"decommission_id": row.ID.String(),
	})

	recordAudit(r, h.queries, "cluster.decommission.requested", "cluster", id.String(), cluster.Name, map[string]any{
		"decommission_id": row.ID.String(),
	})

	statusURL := fmt.Sprintf("/api/v1/clusters/%s/decommission/", id.String())
	RespondJSON(w, http.StatusAccepted, renderDecommission(row, statusURL))
}

// GetDecommission handles GET /api/v1/clusters/{id}/decommission/.
// Returns the latest decommission row's status (idempotent — callers can
// poll). 404 when no decommission has ever been enqueued for the cluster.
func (h *ClusterHandler) GetDecommission(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid cluster ID")
		return
	}
	row, err := h.queries.GetLatestClusterDecommissionByCluster(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "No decommission for cluster")
		return
	}
	statusURL := fmt.Sprintf("/api/v1/clusters/%s/decommission/", id.String())
	RespondJSON(w, http.StatusOK, renderDecommission(row, statusURL))
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

// ClusterConditionResponse is the JSON shape returned from
// GET /api/v1/clusters/{id}/conditions/. Names mirror metav1.Condition so
// the frontend can render Kubernetes-style pills without translation.
type ClusterConditionResponse struct {
	Type               string `json:"type"`
	Status             string `json:"status"`
	Reason             string `json:"reason"`
	Message            string `json:"message"`
	LastTransitionTime string `json:"last_transition_time"`
	LastProbeTime      string `json:"last_probe_time"`
}

// ListConditions handles GET /api/v1/clusters/{id}/conditions/. Returns
// one entry per condition type that the health-check worker has written
// (Connected, AgentReachable, GatewayAPISupported, ...). Returns an empty
// list (not 404) for a cluster that hasn't had a health-check tick yet —
// the UI then shows neutral pills rather than an error toast.
func (h *ClusterHandler) ListConditions(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid cluster ID")
		return
	}
	rows, err := h.queries.ListClusterConditions(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "db_error", "Failed to list conditions")
		return
	}
	out := make([]ClusterConditionResponse, 0, len(rows))
	for _, c := range rows {
		out = append(out, ClusterConditionResponse{
			Type:               c.Type,
			Status:             c.Status,
			Reason:             c.Reason,
			Message:            c.Message,
			LastTransitionTime: c.LastTransitionTime.UTC().Format(time.RFC3339),
			LastProbeTime:      c.LastProbeTime.UTC().Format(time.RFC3339),
		})
	}
	RespondJSON(w, http.StatusOK, out)
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

	manifest := h.renderAgentInstallManifest(cluster, token.Token, agentServerURL(r))

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

func (h *ClusterHandler) renderAgentInstallManifest(cluster sqlc.Cluster, token, serverURL string) string {
	agentImage := "ghcr.io/alphabravocompany/astronomer-go-agent:latest"
	if h != nil && h.agentImage != "" {
		agentImage = h.agentImage
	}
	return agenttemplate.RenderInstallYAML(agenttemplate.InstallTemplateData{
		ServerURL:         serverURL,
		ClusterID:         cluster.ID.String(),
		RegistrationToken: token,
		CACert:            "",
		AgentImage:        agentImage,
	})
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

// DeleteRegistryConfig handles DELETE /api/v1/clusters/{id}/registry/.
func (h *ClusterHandler) DeleteRegistryConfig(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid cluster ID")
		return
	}

	if err := h.queries.DeleteClusterRegistryConfig(r.Context(), id); err != nil {
		RespondError(w, http.StatusInternalServerError, "delete_error", "Failed to delete registry config")
		return
	}

	recordAudit(r, h.queries, "cluster.registry.delete", "cluster", id.String(), "", nil)
	w.WriteHeader(http.StatusNoContent)
}
