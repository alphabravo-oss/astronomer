package handler

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// ArgoCDQuerier abstracts the ArgoCD-related database queries needed by ArgoCDHandler.
type ArgoCDQuerier interface {
	GetArgoCDInstanceByID(ctx context.Context, id uuid.UUID) (sqlc.ArgocdInstance, error)
	ListArgoCDInstances(ctx context.Context, arg sqlc.ListArgoCDInstancesParams) ([]sqlc.ArgocdInstance, error)
	CreateArgoCDInstance(ctx context.Context, arg sqlc.CreateArgoCDInstanceParams) (sqlc.ArgocdInstance, error)
	DeleteArgoCDInstance(ctx context.Context, id uuid.UUID) error
	CountArgoCDInstances(ctx context.Context) (int64, error)
	// Applications
	ListArgoCDApplications(ctx context.Context, arg sqlc.ListArgoCDApplicationsParams) ([]sqlc.ArgocdApplication, error)
	ListAppsByInstance(ctx context.Context, arg sqlc.ListAppsByInstanceParams) ([]sqlc.ArgocdApplication, error)
	GetArgoCDApplicationByID(ctx context.Context, id uuid.UUID) (sqlc.ArgocdApplication, error)
	CountArgoCDApplications(ctx context.Context) (int64, error)
	CountAppsByInstance(ctx context.Context, argocdInstanceID uuid.UUID) (int64, error)
}

// ArgoCDHandler handles ArgoCD endpoints.
type ArgoCDHandler struct {
	queries ArgoCDQuerier
}

// NewArgoCDHandler creates a new ArgoCD handler.
func NewArgoCDHandler(queries ArgoCDQuerier) *ArgoCDHandler {
	return &ArgoCDHandler{queries: queries}
}

// --- Request types ---

// CreateArgoCDInstanceRequest represents the request body for creating an ArgoCD instance.
type CreateArgoCDInstanceRequest struct {
	Name               string    `json:"name"`
	ClusterID          uuid.UUID `json:"cluster_id"`
	ApiUrl             string    `json:"api_url"`
	AuthTokenEncrypted string    `json:"auth_token_encrypted"`
	VerifySsl          bool      `json:"verify_ssl"`
}

// --- Endpoints ---

// ListInstances handles GET /api/v1/argocd/instances/.
func (h *ArgoCDHandler) ListInstances(w http.ResponseWriter, r *http.Request) {
	limit := int32(queryInt(r, "limit", 20))
	offset := int32(queryInt(r, "offset", 0))

	instances, err := h.queries.ListArgoCDInstances(r.Context(), sqlc.ListArgoCDInstancesParams{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "list_error", "Failed to list ArgoCD instances")
		return
	}

	total, err := h.queries.CountArgoCDInstances(r.Context())
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "count_error", "Failed to count ArgoCD instances")
		return
	}

	RespondPaginated(w, r, instances, total)
}

// CreateInstance handles POST /api/v1/argocd/instances/.
func (h *ArgoCDHandler) CreateInstance(w http.ResponseWriter, r *http.Request) {
	var req CreateArgoCDInstanceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}

	if req.Name == "" {
		RespondError(w, http.StatusBadRequest, "validation_error", "Instance name is required")
		return
	}

	instance, err := h.queries.CreateArgoCDInstance(r.Context(), sqlc.CreateArgoCDInstanceParams{
		Name:               req.Name,
		ClusterID:          req.ClusterID,
		ApiUrl:             req.ApiUrl,
		AuthTokenEncrypted: req.AuthTokenEncrypted,
		VerifySsl:          req.VerifySsl,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "create_error", "Failed to create ArgoCD instance")
		return
	}

	RespondJSON(w, http.StatusCreated, instance)
}

// GetInstance handles GET /api/v1/argocd/instances/{id}/.
func (h *ArgoCDHandler) GetInstance(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid instance ID")
		return
	}

	instance, err := h.queries.GetArgoCDInstanceByID(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "ArgoCD instance not found")
		return
	}

	RespondJSON(w, http.StatusOK, instance)
}

// DeleteInstance handles DELETE /api/v1/argocd/instances/{id}/.
func (h *ArgoCDHandler) DeleteInstance(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid instance ID")
		return
	}

	if err := h.queries.DeleteArgoCDInstance(r.Context(), id); err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "ArgoCD instance not found")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ListAppsByInstance handles GET /api/v1/argocd/instances/{id}/apps/.
func (h *ArgoCDHandler) ListAppsByInstance(w http.ResponseWriter, r *http.Request) {
	instanceID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid instance ID")
		return
	}

	limit := int32(queryInt(r, "limit", 20))
	offset := int32(queryInt(r, "offset", 0))

	apps, err := h.queries.ListAppsByInstance(r.Context(), sqlc.ListAppsByInstanceParams{
		ArgocdInstanceID: instanceID,
		Limit:            limit,
		Offset:           offset,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "list_error", "Failed to list ArgoCD applications")
		return
	}

	total, err := h.queries.CountAppsByInstance(r.Context(), instanceID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "count_error", "Failed to count ArgoCD applications")
		return
	}

	RespondPaginated(w, r, apps, total)
}

// ListAllApps handles GET /api/v1/argocd/apps/.
func (h *ArgoCDHandler) ListAllApps(w http.ResponseWriter, r *http.Request) {
	limit := int32(queryInt(r, "limit", 20))
	offset := int32(queryInt(r, "offset", 0))

	apps, err := h.queries.ListArgoCDApplications(r.Context(), sqlc.ListArgoCDApplicationsParams{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "list_error", "Failed to list ArgoCD applications")
		return
	}

	total, err := h.queries.CountArgoCDApplications(r.Context())
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "count_error", "Failed to count ArgoCD applications")
		return
	}

	RespondPaginated(w, r, apps, total)
}

// GetApp handles GET /api/v1/argocd/apps/{id}/.
func (h *ArgoCDHandler) GetApp(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid application ID")
		return
	}

	app, err := h.queries.GetArgoCDApplicationByID(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "ArgoCD application not found")
		return
	}

	RespondJSON(w, http.StatusOK, app)
}

// SyncApp handles POST /api/v1/argocd/apps/{id}/sync/.
// This is a stub that will be implemented when the ArgoCD integration is complete.
func (h *ArgoCDHandler) SyncApp(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid application ID")
		return
	}

	// Verify the application exists.
	if _, err := h.queries.GetArgoCDApplicationByID(r.Context(), id); err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "ArgoCD application not found")
		return
	}

	// TODO: Trigger actual ArgoCD sync via API.
	RespondJSON(w, http.StatusAccepted, map[string]string{
		"status":  "accepted",
		"message": "Sync request accepted",
	})
}
