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

// CatalogQuerier abstracts the catalog-related database queries needed by CatalogHandler.
type CatalogQuerier interface {
	// Repositories
	GetHelmRepositoryByID(ctx context.Context, id uuid.UUID) (sqlc.HelmRepository, error)
	ListHelmRepositories(ctx context.Context, arg sqlc.ListHelmRepositoriesParams) ([]sqlc.HelmRepository, error)
	CreateHelmRepository(ctx context.Context, arg sqlc.CreateHelmRepositoryParams) (sqlc.HelmRepository, error)
	UpdateHelmRepository(ctx context.Context, arg sqlc.UpdateHelmRepositoryParams) (sqlc.HelmRepository, error)
	DeleteHelmRepository(ctx context.Context, id uuid.UUID) error
	CountHelmRepositories(ctx context.Context) (int64, error)
	// Charts
	ListHelmCharts(ctx context.Context, arg sqlc.ListHelmChartsParams) ([]sqlc.HelmChart, error)
	ListChartsByRepository(ctx context.Context, arg sqlc.ListChartsByRepositoryParams) ([]sqlc.HelmChart, error)
	GetHelmChartByID(ctx context.Context, id uuid.UUID) (sqlc.HelmChart, error)
	CountHelmCharts(ctx context.Context) (int64, error)
	// Chart Versions
	GetLatestChartVersion(ctx context.Context, chartID uuid.UUID) (sqlc.HelmChartVersion, error)
	// Installed Charts
	ListInstalledChartsByCluster(ctx context.Context, arg sqlc.ListInstalledChartsByClusterParams) ([]sqlc.InstalledChart, error)
	GetInstalledChartByID(ctx context.Context, id uuid.UUID) (sqlc.InstalledChart, error)
	CreateInstalledChart(ctx context.Context, arg sqlc.CreateInstalledChartParams) (sqlc.InstalledChart, error)
	DeleteInstalledChart(ctx context.Context, id uuid.UUID) error
	CountInstalledChartsByCluster(ctx context.Context, clusterID uuid.UUID) (int64, error)
}

// CatalogHandler handles catalog endpoints (helm repositories, charts, installations).
type CatalogHandler struct {
	queries CatalogQuerier
}

// NewCatalogHandler creates a new catalog handler.
func NewCatalogHandler(queries CatalogQuerier) *CatalogHandler {
	return &CatalogHandler{queries: queries}
}

// --- Helm Repositories ---

// ListRepos handles GET /api/v1/catalog/repositories/.
func (h *CatalogHandler) ListRepos(w http.ResponseWriter, r *http.Request) {
	limit := int32(queryInt(r, "limit", 20))
	offset := int32(queryInt(r, "offset", 0))

	repos, err := h.queries.ListHelmRepositories(r.Context(), sqlc.ListHelmRepositoriesParams{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "list_error", "Failed to list repositories")
		return
	}

	total, err := h.queries.CountHelmRepositories(r.Context())
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "count_error", "Failed to count repositories")
		return
	}

	RespondPaginated(w, r, repos, total)
}

// CreateRepoRequest represents the request body for creating a helm repository.
type CreateRepoRequest struct {
	Name        string          `json:"name"`
	URL         string          `json:"url"`
	RepoType    string          `json:"repo_type"`
	Description string          `json:"description"`
	IsDefault   bool            `json:"is_default"`
	AuthType    string          `json:"auth_type"`
	AuthConfig  json.RawMessage `json:"auth_config"`
	Enabled     bool            `json:"enabled"`
}

// CreateRepo handles POST /api/v1/catalog/repositories/.
func (h *CatalogHandler) CreateRepo(w http.ResponseWriter, r *http.Request) {
	var req CreateRepoRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}

	if req.Name == "" {
		RespondError(w, http.StatusBadRequest, "validation_error", "Repository name is required")
		return
	}
	if req.URL == "" {
		RespondError(w, http.StatusBadRequest, "validation_error", "Repository URL is required")
		return
	}

	if req.AuthConfig == nil {
		req.AuthConfig = json.RawMessage(`{}`)
	}

	repo, err := h.queries.CreateHelmRepository(r.Context(), sqlc.CreateHelmRepositoryParams{
		Name:        req.Name,
		Url:         req.URL,
		RepoType:    req.RepoType,
		Description: req.Description,
		IsDefault:   req.IsDefault,
		AuthType:    req.AuthType,
		AuthConfig:  req.AuthConfig,
		Enabled:     req.Enabled,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "create_error", "Failed to create repository")
		return
	}

	RespondJSON(w, http.StatusCreated, repo)
}

// GetRepo handles GET /api/v1/catalog/repositories/{id}/.
func (h *CatalogHandler) GetRepo(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid repository ID")
		return
	}

	repo, err := h.queries.GetHelmRepositoryByID(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Repository not found")
		return
	}

	RespondJSON(w, http.StatusOK, repo)
}

// UpdateRepoRequest represents the request body for updating a helm repository.
type UpdateRepoRequest struct {
	Name        string          `json:"name"`
	URL         string          `json:"url"`
	RepoType    string          `json:"repo_type"`
	Description string          `json:"description"`
	IsDefault   bool            `json:"is_default"`
	AuthType    string          `json:"auth_type"`
	AuthConfig  json.RawMessage `json:"auth_config"`
	Enabled     bool            `json:"enabled"`
}

// UpdateRepo handles PUT /api/v1/catalog/repositories/{id}/.
func (h *CatalogHandler) UpdateRepo(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid repository ID")
		return
	}

	var req UpdateRepoRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}

	if req.AuthConfig == nil {
		req.AuthConfig = json.RawMessage(`{}`)
	}

	repo, err := h.queries.UpdateHelmRepository(r.Context(), sqlc.UpdateHelmRepositoryParams{
		ID:          id,
		Name:        req.Name,
		Url:         req.URL,
		RepoType:    req.RepoType,
		Description: req.Description,
		IsDefault:   req.IsDefault,
		AuthType:    req.AuthType,
		AuthConfig:  req.AuthConfig,
		Enabled:     req.Enabled,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "update_error", "Failed to update repository")
		return
	}

	RespondJSON(w, http.StatusOK, repo)
}

// DeleteRepo handles DELETE /api/v1/catalog/repositories/{id}/.
func (h *CatalogHandler) DeleteRepo(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid repository ID")
		return
	}

	if err := h.queries.DeleteHelmRepository(r.Context(), id); err != nil {
		RespondError(w, http.StatusInternalServerError, "delete_error", "Failed to delete repository")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// --- Helm Charts ---

// ListCharts handles GET /api/v1/catalog/charts/.
func (h *CatalogHandler) ListCharts(w http.ResponseWriter, r *http.Request) {
	limit := int32(queryInt(r, "limit", 20))
	offset := int32(queryInt(r, "offset", 0))

	charts, err := h.queries.ListHelmCharts(r.Context(), sqlc.ListHelmChartsParams{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "list_error", "Failed to list charts")
		return
	}

	total, err := h.queries.CountHelmCharts(r.Context())
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "count_error", "Failed to count charts")
		return
	}

	RespondPaginated(w, r, charts, total)
}

// GetChart handles GET /api/v1/catalog/charts/{id}/.
func (h *CatalogHandler) GetChart(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid chart ID")
		return
	}

	chart, err := h.queries.GetHelmChartByID(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Chart not found")
		return
	}

	RespondJSON(w, http.StatusOK, chart)
}

// --- Installed Charts (Installations) ---

// ListInstallations handles GET /api/v1/clusters/{cluster_id}/installations/.
func (h *CatalogHandler) ListInstallations(w http.ResponseWriter, r *http.Request) {
	clusterID, err := uuid.Parse(chi.URLParam(r, "cluster_id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid cluster ID")
		return
	}

	limit := int32(queryInt(r, "limit", 20))
	offset := int32(queryInt(r, "offset", 0))

	installations, err := h.queries.ListInstalledChartsByCluster(r.Context(), sqlc.ListInstalledChartsByClusterParams{
		ClusterID: clusterID,
		Limit:     limit,
		Offset:    offset,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "list_error", "Failed to list installations")
		return
	}

	total, err := h.queries.CountInstalledChartsByCluster(r.Context(), clusterID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "count_error", "Failed to count installations")
		return
	}

	RespondPaginated(w, r, installations, total)
}

// CreateInstallationRequest represents the request body for creating an installation.
type CreateInstallationRequest struct {
	ChartVersionID string `json:"chart_version_id"`
	ReleaseName    string `json:"release_name"`
	Namespace      string `json:"namespace"`
	ValuesOverride string `json:"values_override"`
	Notes          string `json:"notes"`
	ToolSlug       string `json:"tool_slug"`
	PresetUsed     string `json:"preset_used"`
}

// CreateInstallation handles POST /api/v1/clusters/{cluster_id}/installations/.
func (h *CatalogHandler) CreateInstallation(w http.ResponseWriter, r *http.Request) {
	clusterID, err := uuid.Parse(chi.URLParam(r, "cluster_id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid cluster ID")
		return
	}

	var req CreateInstallationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}

	if req.ReleaseName == "" {
		RespondError(w, http.StatusBadRequest, "validation_error", "Release name is required")
		return
	}
	if req.Namespace == "" {
		RespondError(w, http.StatusBadRequest, "validation_error", "Namespace is required")
		return
	}

	params := sqlc.CreateInstalledChartParams{
		ClusterID:      clusterID,
		ReleaseName:    req.ReleaseName,
		Namespace:      req.Namespace,
		ValuesOverride: req.ValuesOverride,
		Status:         "pending",
		Revision:       1,
		Notes:          req.Notes,
	}

	if req.ChartVersionID != "" {
		cvID, err := uuid.Parse(req.ChartVersionID)
		if err != nil {
			RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid chart version ID")
			return
		}
		params.ChartVersionID = pgtype.UUID{Bytes: cvID, Valid: true}
	}

	if req.ToolSlug != "" {
		params.ToolSlug = pgtype.Text{String: req.ToolSlug, Valid: true}
	}
	if req.PresetUsed != "" {
		params.PresetUsed = pgtype.Text{String: req.PresetUsed, Valid: true}
	}

	installation, err := h.queries.CreateInstalledChart(r.Context(), params)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "create_error", "Failed to create installation")
		return
	}

	RespondJSON(w, http.StatusCreated, installation)
}

// DeleteInstallation handles DELETE /api/v1/clusters/{cluster_id}/installations/{id}/.
func (h *CatalogHandler) DeleteInstallation(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid installation ID")
		return
	}

	if err := h.queries.DeleteInstalledChart(r.Context(), id); err != nil {
		RespondError(w, http.StatusInternalServerError, "delete_error", "Failed to delete installation")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
