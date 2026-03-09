package handler

import "net/http"

// WorkloadHandler handles workload endpoints that are proxied through the agent tunnel.
// All endpoints are stubs until an active agent tunnel connection is available.
type WorkloadHandler struct{}

// NewWorkloadHandler creates a new workload handler.
func NewWorkloadHandler() *WorkloadHandler {
	return &WorkloadHandler{}
}

// tunnelRequired is the standard stub response for all workload endpoints.
func (h *WorkloadHandler) tunnelRequired(w http.ResponseWriter, _ *http.Request) {
	RespondJSON(w, http.StatusOK, map[string]string{
		"status":  "tunnel_required",
		"message": "This endpoint requires an active agent tunnel connection",
	})
}

// List handles GET /api/v1/clusters/{cluster_id}/workloads/.
func (h *WorkloadHandler) List(w http.ResponseWriter, r *http.Request) {
	h.tunnelRequired(w, r)
}

// Get handles GET /api/v1/clusters/{cluster_id}/workloads/{namespace}/{kind}/{name}/.
func (h *WorkloadHandler) Get(w http.ResponseWriter, r *http.Request) {
	h.tunnelRequired(w, r)
}

// Scale handles POST /api/v1/clusters/{cluster_id}/workloads/{namespace}/{kind}/{name}/scale/.
func (h *WorkloadHandler) Scale(w http.ResponseWriter, r *http.Request) {
	h.tunnelRequired(w, r)
}

// Restart handles POST /api/v1/clusters/{cluster_id}/workloads/{namespace}/{kind}/{name}/restart/.
func (h *WorkloadHandler) Restart(w http.ResponseWriter, r *http.Request) {
	h.tunnelRequired(w, r)
}

// Delete handles DELETE /api/v1/clusters/{cluster_id}/workloads/{namespace}/{kind}/{name}/.
func (h *WorkloadHandler) Delete(w http.ResponseWriter, r *http.Request) {
	h.tunnelRequired(w, r)
}

// ListNamespaces handles GET /api/v1/clusters/{cluster_id}/namespaces/.
func (h *WorkloadHandler) ListNamespaces(w http.ResponseWriter, r *http.Request) {
	h.tunnelRequired(w, r)
}

// ListNodes handles GET /api/v1/clusters/{cluster_id}/nodes/.
func (h *WorkloadHandler) ListNodes(w http.ResponseWriter, r *http.Request) {
	h.tunnelRequired(w, r)
}

// ListEvents handles GET /api/v1/clusters/{cluster_id}/events/.
func (h *WorkloadHandler) ListEvents(w http.ResponseWriter, r *http.Request) {
	h.tunnelRequired(w, r)
}
