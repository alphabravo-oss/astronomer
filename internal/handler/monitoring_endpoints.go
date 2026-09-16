package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (h *MonitoringHandler) UnmarshalBody(r *http.Request, dst any) error {
	return json.NewDecoder(r.Body).Decode(dst)
}

// --- Monitoring endpoints CRUD (Python: /api/v1/monitoring/endpoints/) ---
//
// We back this on the existing `monitoring_backends` table since the Python
// `PrometheusEndpoint` model maps to the same configuration concept.

// ListEndpoints handles GET /api/v1/monitoring/endpoints/.
func (h *MonitoringHandler) ListEndpoints(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil {
		paging.Write(w, []any{}, paging.Exact(0, queryLimit(r, 20), queryOffset(r), len([]any{})))
		return
	}
	backend, err := h.queries.GetDefaultMonitoringBackend(r.Context())
	if err != nil && err != pgx.ErrNoRows {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to load monitoring endpoints")
		return
	}
	items := []map[string]any{}
	if err == nil && backend.ID != uuid.Nil {
		items = append(items, monitoringBackendResponse(backend, h.readAuthConfig(backend)))
	}
	page, metadata := pageWindow(r, items)
	paging.Write(w, page, metadata)
}

// GetEndpoint handles GET /api/v1/monitoring/endpoints/{id}/.
func (h *MonitoringHandler) GetEndpoint(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.MonitoringError, "monitoring store not configured")
		return
	}
	idStr := chi.URLParam(r, "id")
	backend, err := h.queries.GetDefaultMonitoringBackend(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Monitoring endpoint not found")
		return
	}
	if backend.ID.String() != idStr {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Monitoring endpoint not found")
		return
	}
	RespondJSON(w, http.StatusOK, monitoringBackendResponse(backend, h.readAuthConfig(backend)))
}

// CreateEndpoint handles POST /api/v1/monitoring/endpoints/.
// Currently maps to UpsertDefaultMonitoringBackend.
func (h *MonitoringHandler) CreateEndpoint(w http.ResponseWriter, r *http.Request) {
	h.UpdateBackendConfig(w, r)
}

// UpdateEndpoint handles PUT /api/v1/monitoring/endpoints/{id}/.
func (h *MonitoringHandler) UpdateEndpoint(w http.ResponseWriter, r *http.Request) {
	h.UpdateBackendConfig(w, r)
}

// DeleteEndpoint handles DELETE /api/v1/monitoring/endpoints/{id}/. The
// guarded SQL mutation refuses to cascade cluster monitoring configurations
// or orphan managed shared-stack releases. Operators must detach/uninstall
// those dependencies first; the API reports that state as a conflict.
func (h *MonitoringHandler) DeleteEndpoint(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.MonitoringError, "monitoring store not configured")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid monitoring endpoint ID")
		return
	}
	existing, err := h.queries.GetDefaultMonitoringBackend(r.Context())
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && existing.ID != id) {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Monitoring endpoint not found")
		return
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.MonitoringError, "Failed to load monitoring endpoint")
		return
	}
	if _, ok := h.queries.(monitoringBackendDeleter); !ok {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.MonitoringError, "monitoring backend deletion is not configured")
		return
	}

	_, err = executeMutation(r, h.runTx,
		func(q MonitoringMutationTx) (sqlc.MonitoringBackend, error) {
			deleter, ok := q.(monitoringBackendDeleter)
			if !ok {
				return sqlc.MonitoringBackend{}, errMonitoringBackendDeleteUnsupported
			}
			return deleter.DeleteDefaultMonitoringBackendIfUnused(r.Context(), id)
		},
		func(backend sqlc.MonitoringBackend) mutationAuditEvent {
			return mutationAuditEvent{
				action:       "monitoring.endpoint.delete",
				resourceType: "monitoring_backend",
				resourceID:   backend.ID.String(),
				resourceName: backend.BackendType,
				status:       http.StatusNoContent,
				detail:       map[string]any{"tenant_id": backend.TenantID, "auth_type": backend.AuthType},
			}
		})
	if errors.Is(err, pgx.ErrNoRows) {
		RespondRequestError(w, r, http.StatusConflict, apierror.Conflict,
			"Uninstall active cluster and managed shared monitoring stacks before deleting the endpoint")
		return
	}
	if errors.Is(err, errMonitoringBackendDeleteUnsupported) {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.MonitoringError, "monitoring backend deletion is not configured")
		return
	}
	if err != nil {
		respondMonitoringMutationError(w, r, err, http.StatusInternalServerError, apierror.MonitoringError, "Failed to delete monitoring endpoint")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
