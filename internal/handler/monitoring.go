package handler

import (
	"net/http"
)

// MonitoringHandler handles monitoring proxy endpoints.
// These are stubs that will be wired to the agent tunnel later.
type MonitoringHandler struct{}

// NewMonitoringHandler creates a new monitoring handler.
func NewMonitoringHandler() *MonitoringHandler {
	return &MonitoringHandler{}
}

// monitoringStubResponse is the placeholder response for unimplemented monitoring endpoints.
var monitoringStubResponse = map[string]any{
	"status":  "not_implemented",
	"message": "Monitoring proxy will be connected via agent tunnel",
}

// PrometheusQuery handles POST /api/v1/clusters/{cluster_id}/monitoring/query/.
func (h *MonitoringHandler) PrometheusQuery(w http.ResponseWriter, r *http.Request) {
	RespondJSON(w, http.StatusOK, monitoringStubResponse)
}

// PrometheusQueryRange handles POST /api/v1/clusters/{cluster_id}/monitoring/query_range/.
func (h *MonitoringHandler) PrometheusQueryRange(w http.ResponseWriter, r *http.Request) {
	RespondJSON(w, http.StatusOK, monitoringStubResponse)
}

// ListMetrics handles GET /api/v1/clusters/{cluster_id}/monitoring/metrics/.
func (h *MonitoringHandler) ListMetrics(w http.ResponseWriter, r *http.Request) {
	RespondJSON(w, http.StatusOK, monitoringStubResponse)
}
