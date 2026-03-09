package handler

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

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

// ClusterHandler handles cluster endpoints.
type ClusterHandler struct {
	queries ClusterQuerier
}

// NewClusterHandler creates a new cluster handler.
func NewClusterHandler(queries ClusterQuerier) *ClusterHandler {
	return &ClusterHandler{queries: queries}
}

// --- Request / Response types ---

// CreateClusterRequest represents the request body for creating a cluster.
type CreateClusterRequest struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	Environment string `json:"environment"`
	Region      string `json:"region"`
	Provider    string `json:"provider"`
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

	RespondPaginated(w, r, clusters, total)
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

	cluster, err := h.queries.CreateCluster(r.Context(), sqlc.CreateClusterParams{
		Name:        req.Name,
		DisplayName: req.DisplayName,
		Description: req.Description,
		Environment: req.Environment,
		Region:      req.Region,
		Provider:    req.Provider,
		CreatedByID: pgtype.UUID{}, // TODO: extract from auth context
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "create_error", "Failed to create cluster")
		return
	}

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

	RespondJSON(w, http.StatusOK, cluster)
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

	RespondJSON(w, http.StatusOK, cluster)
}

// Delete handles DELETE /api/v1/clusters/{id}/.
func (h *ClusterHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid cluster ID")
		return
	}

	if err := h.queries.DeleteCluster(r.Context(), id); err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Cluster not found")
		return
	}

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

	RespondJSON(w, http.StatusOK, config)
}
