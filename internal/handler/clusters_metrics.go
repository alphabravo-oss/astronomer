package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/handler/clustermetrics"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/jackc/pgx/v5"
	"k8s.io/client-go/kubernetes"
	metricsv "k8s.io/metrics/pkg/client/clientset/versioned"
)

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

// The previous clusterWithMetrics struct (anonymous-embed sqlc.Cluster +
// CPU/Memory/Pod scalars) was replaced by the explicit ClusterResponse DTO
// in clusters_response.go. See TestClusterResponse_WireCompat for the
// byte-for-byte wire compat guarantee.

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

// enrichClusterFromCache copies the sqlc.Cluster row plus the most-recent
// CACHED metrics snapshot into the wire-format struct. Cache-only on
// purpose: this is called from List which iterates every cluster, and a
// slow agent on a single cluster previously stalled the entire response
// for up to 5s × N clusters. The background metrics
// publisher (internal/metrics/publisher.go) keeps the cache warm; stale
// or missing entries return zero values rather than blocking.
func (h *ClusterHandler) enrichClusterFromCache(_ context.Context, c sqlc.Cluster) (ClusterResponse, error) {
	out, err := clusterToResponse(c)
	if err != nil {
		return ClusterResponse{}, err
	}
	if h.metrics == nil {
		return out, nil
	}
	snap := h.metrics.Peek(c.ID.String())
	out.CPUPercentage = snap.CPUPercentage
	out.MemoryPercentage = snap.MemoryPercentage
	out.PodCount = snap.PodCount
	out.MetricsServerPresent = snap.MetricsServerPresent
	return out, nil
}

// enrichClusterFresh is the slow-path counterpart to enrichClusterFromCache.
// Called from single-cluster endpoints (Get) where the caller is willing to
// wait for an up-to-date snapshot. Bounded by a 5s per-cluster timeout to
// keep a hung agent from holding the HTTP handler indefinitely.
func (h *ClusterHandler) enrichClusterFresh(ctx context.Context, c sqlc.Cluster) (ClusterResponse, error) {
	out, err := clusterToResponse(c)
	if err != nil {
		return ClusterResponse{}, err
	}
	if h.metrics == nil {
		return out, nil
	}
	mctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	snap := h.metrics.Get(mctx, c.ID.String(), c.IsLocal)
	out.CPUPercentage = snap.CPUPercentage
	out.MemoryPercentage = snap.MemoryPercentage
	out.PodCount = snap.PodCount
	out.MetricsServerPresent = snap.MetricsServerPresent
	return out, nil
}

// GetHealth handles GET /api/v1/clusters/{id}/health/.
func (h *ClusterHandler) GetHealth(w http.ResponseWriter, r *http.Request) {
	id, ok := parseClusterIDParam(w, r, "id")
	if !ok {
		return
	}

	health, err := h.queries.GetClusterHealthStatus(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Health status not found for cluster")
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
	id, ok := parseClusterIDParam(w, r, "id")
	if !ok {
		return
	}
	rows, err := h.queries.ListClusterConditions(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, "Failed to list conditions")
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
	// The query returns the complete condition set for this cluster.
	paging.Write(w, out, paging.Exact(len(out), len(out), 0, len(out)))
}

// ListConditionRemediation handles GET /api/v1/clusters/{id}/condition-remediation/.
// Returns the 50 most recent remediation attempts (success / failed /
// skipped) for the cluster, ordered newest-first. Read by the
// cluster-detail page to show on-call "what did the controller do
// when this condition went red?" — closes the loop the
// cluster_conditions table opens.
func (h *ClusterHandler) ListConditionRemediation(w http.ResponseWriter, r *http.Request) {
	id, ok := parseClusterIDParam(w, r, "id")
	if !ok {
		return
	}
	if _, err := h.queries.GetClusterByID(r.Context(), id); err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return
	}
	rows, err := h.queries.ListClusterConditionRemediationByCluster(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list remediation attempts")
		return
	}
	// The endpoint returns the complete bounded remediation history.
	paging.Write(w, rows, paging.Exact(len(rows), len(rows), 0, len(rows)))
}

// GetMetrics handles GET /api/v1/clusters/{id}/metrics/.
// Returns CPU/memory/pod aggregate metrics derived from health snapshots.
func (h *ClusterHandler) GetMetrics(w http.ResponseWriter, r *http.Request) {
	id, ok := parseClusterIDParam(w, r, "id")
	if !ok {
		return
	}
	cluster, err := h.queries.GetClusterByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return
	}
	livenessStore, ok := h.queries.(clusterLivenessQuerier)
	if !ok {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.StoreUnavailable, "Cluster liveness store is not available")
		return
	}
	liveness, livenessErr := livenessStore.GetClusterLiveness(r.Context(), id)
	if livenessErr != nil && !errors.Is(livenessErr, pgx.ErrNoRows) {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, "Failed to load cluster liveness")
		return
	}
	isConnected := liveness.LastHeartbeat.Valid && time.Since(liveness.LastHeartbeat.Time) < 5*time.Minute
	metrics := map[string]any{
		"cluster_id":         cluster.ID.String(),
		"cluster_name":       cluster.Name,
		"status":             cluster.Status,
		"is_connected":       isConnected,
		"kubernetes_version": cluster.KubernetesVersion,
		"node_count":         cluster.NodeCount,
		"agent_version":      cluster.AgentVersion,
	}
	if liveness.LastHeartbeat.Valid {
		metrics["last_heartbeat"] = liveness.LastHeartbeat.Time.UTC().Format(time.RFC3339)
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
	id, ok := parseClusterIDParam(w, r, "id")
	if !ok {
		return
	}
	cluster, err := h.queries.GetClusterByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
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
