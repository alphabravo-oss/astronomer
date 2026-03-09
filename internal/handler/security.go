package handler

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// SecurityQuerier abstracts the security-related database queries needed by SecurityHandler.
type SecurityQuerier interface {
	// Templates
	GetPodSecurityTemplateByID(ctx context.Context, id uuid.UUID) (sqlc.PodSecurityTemplate, error)
	ListPodSecurityTemplates(ctx context.Context, arg sqlc.ListPodSecurityTemplatesParams) ([]sqlc.PodSecurityTemplate, error)
	CreatePodSecurityTemplate(ctx context.Context, arg sqlc.CreatePodSecurityTemplateParams) (sqlc.PodSecurityTemplate, error)
	DeletePodSecurityTemplate(ctx context.Context, id uuid.UUID) error
	CountPodSecurityTemplates(ctx context.Context) (int64, error)
	// Policies
	GetPolicyByCluster(ctx context.Context, clusterID uuid.UUID) (sqlc.ClusterSecurityPolicy, error)
	// Scans
	ListScansByCluster(ctx context.Context, arg sqlc.ListScansByClusterParams) ([]sqlc.SecurityScanResult, error)
	GetSecurityScanResultByID(ctx context.Context, id uuid.UUID) (sqlc.SecurityScanResult, error)
	CountSecurityScanResults(ctx context.Context) (int64, error)
}

// SecurityHandler handles security endpoints.
type SecurityHandler struct {
	queries SecurityQuerier
}

// NewSecurityHandler creates a new security handler.
func NewSecurityHandler(queries SecurityQuerier) *SecurityHandler {
	return &SecurityHandler{queries: queries}
}

// --- Request types ---

// CreateTemplateRequest represents the request body for creating a pod security template.
type CreateTemplateRequest struct {
	Name                 string          `json:"name"`
	Description          string          `json:"description"`
	IsDefault            bool            `json:"is_default"`
	EnforceLevel         string          `json:"enforce_level"`
	EnforceVersion       string          `json:"enforce_version"`
	AuditLevel           string          `json:"audit_level"`
	AuditVersion         string          `json:"audit_version"`
	WarnLevel            string          `json:"warn_level"`
	WarnVersion          string          `json:"warn_version"`
	ExemptUsernames      json.RawMessage `json:"exempt_usernames"`
	ExemptRuntimeClasses json.RawMessage `json:"exempt_runtime_classes"`
	ExemptNamespaces     json.RawMessage `json:"exempt_namespaces"`
}

// --- Endpoints ---

// ListTemplates handles GET /api/v1/security/templates/.
func (h *SecurityHandler) ListTemplates(w http.ResponseWriter, r *http.Request) {
	limit := int32(queryInt(r, "limit", 20))
	offset := int32(queryInt(r, "offset", 0))

	templates, err := h.queries.ListPodSecurityTemplates(r.Context(), sqlc.ListPodSecurityTemplatesParams{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "list_error", "Failed to list security templates")
		return
	}

	total, err := h.queries.CountPodSecurityTemplates(r.Context())
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "count_error", "Failed to count security templates")
		return
	}

	RespondPaginated(w, r, templates, total)
}

// CreateTemplate handles POST /api/v1/security/templates/.
func (h *SecurityHandler) CreateTemplate(w http.ResponseWriter, r *http.Request) {
	var req CreateTemplateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}

	if req.Name == "" {
		RespondError(w, http.StatusBadRequest, "validation_error", "Template name is required")
		return
	}

	exemptUsernames := req.ExemptUsernames
	if exemptUsernames == nil {
		exemptUsernames = json.RawMessage(`[]`)
	}
	exemptRuntimeClasses := req.ExemptRuntimeClasses
	if exemptRuntimeClasses == nil {
		exemptRuntimeClasses = json.RawMessage(`[]`)
	}
	exemptNamespaces := req.ExemptNamespaces
	if exemptNamespaces == nil {
		exemptNamespaces = json.RawMessage(`[]`)
	}

	template, err := h.queries.CreatePodSecurityTemplate(r.Context(), sqlc.CreatePodSecurityTemplateParams{
		Name:                 req.Name,
		Description:          req.Description,
		IsDefault:            req.IsDefault,
		EnforceLevel:         req.EnforceLevel,
		EnforceVersion:       req.EnforceVersion,
		AuditLevel:           req.AuditLevel,
		AuditVersion:         req.AuditVersion,
		WarnLevel:            req.WarnLevel,
		WarnVersion:          req.WarnVersion,
		ExemptUsernames:      exemptUsernames,
		ExemptRuntimeClasses: exemptRuntimeClasses,
		ExemptNamespaces:     exemptNamespaces,
		CreatedByID:          pgtype.UUID{}, // TODO: extract from auth context
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "create_error", "Failed to create security template")
		return
	}

	RespondJSON(w, http.StatusCreated, template)
}

// GetTemplate handles GET /api/v1/security/templates/{id}/.
func (h *SecurityHandler) GetTemplate(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid template ID")
		return
	}

	template, err := h.queries.GetPodSecurityTemplateByID(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Security template not found")
		return
	}

	RespondJSON(w, http.StatusOK, template)
}

// DeleteTemplate handles DELETE /api/v1/security/templates/{id}/.
func (h *SecurityHandler) DeleteTemplate(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid template ID")
		return
	}

	if err := h.queries.DeletePodSecurityTemplate(r.Context(), id); err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Security template not found")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// GetPolicy handles GET /api/v1/clusters/{cluster_id}/security/policy/.
func (h *SecurityHandler) GetPolicy(w http.ResponseWriter, r *http.Request) {
	clusterID, err := uuid.Parse(chi.URLParam(r, "cluster_id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid cluster ID")
		return
	}

	policy, err := h.queries.GetPolicyByCluster(r.Context(), clusterID)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Security policy not found for cluster")
		return
	}

	RespondJSON(w, http.StatusOK, policy)
}

// ListScans handles GET /api/v1/clusters/{cluster_id}/security/scans/.
func (h *SecurityHandler) ListScans(w http.ResponseWriter, r *http.Request) {
	clusterID, err := uuid.Parse(chi.URLParam(r, "cluster_id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid cluster ID")
		return
	}

	limit := int32(queryInt(r, "limit", 20))
	offset := int32(queryInt(r, "offset", 0))

	scans, err := h.queries.ListScansByCluster(r.Context(), sqlc.ListScansByClusterParams{
		ClusterID: clusterID,
		Limit:     limit,
		Offset:    offset,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "list_error", "Failed to list security scans")
		return
	}

	total, err := h.queries.CountSecurityScanResults(r.Context())
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "count_error", "Failed to count security scans")
		return
	}

	RespondPaginated(w, r, scans, total)
}

// GetScan handles GET /api/v1/clusters/{cluster_id}/security/scans/{id}/.
func (h *SecurityHandler) GetScan(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid scan ID")
		return
	}

	scan, err := h.queries.GetSecurityScanResultByID(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Security scan not found")
		return
	}

	RespondJSON(w, http.StatusOK, scan)
}
