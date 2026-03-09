package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// ResourceHandler handles generic Kubernetes resource proxy and platform stub endpoints.
type ResourceHandler struct{}

// NewResourceHandler creates a new resource handler.
func NewResourceHandler() *ResourceHandler {
	return &ResourceHandler{}
}

// --- Stub Endpoints ---

// ListResources handles GET /api/v1/clusters/{cluster_id}/resources/{group}/{version}/{kind}/.
// Stub: will proxy to the cluster agent when implemented.
func (h *ResourceHandler) ListResources(w http.ResponseWriter, r *http.Request) {
	clusterID := chi.URLParam(r, "cluster_id")
	group := chi.URLParam(r, "group")
	version := chi.URLParam(r, "version")
	kind := chi.URLParam(r, "kind")

	RespondJSON(w, http.StatusOK, map[string]any{
		"cluster_id": clusterID,
		"group":      group,
		"version":    version,
		"kind":       kind,
		"items":      []any{},
		"message":    "Resource proxy not yet implemented",
	})
}

// GetSettings handles GET /api/v1/settings/.
// Stub: returns platform configuration placeholder.
func (h *ResourceHandler) GetSettings(w http.ResponseWriter, r *http.Request) {
	RespondJSON(w, http.StatusOK, map[string]any{
		"platform_name":     "Astronomer",
		"version":           "0.1.0",
		"telemetry_enabled": false,
	})
}

// ListActivity handles GET /api/v1/activity/.
// Stub: returns recent audit log entries placeholder.
func (h *ResourceHandler) ListActivity(w http.ResponseWriter, r *http.Request) {
	RespondPaginated(w, r, []any{}, 0)
}

// ListUsers handles GET /api/v1/users/.
// Stub: returns user list placeholder.
func (h *ResourceHandler) ListUsers(w http.ResponseWriter, r *http.Request) {
	RespondPaginated(w, r, []any{}, 0)
}

// GetUser handles GET /api/v1/users/{id}/.
// Stub: returns user detail placeholder.
func (h *ResourceHandler) GetUser(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	RespondJSON(w, http.StatusOK, map[string]any{
		"id":      id,
		"message": "User endpoint not yet implemented",
	})
}
