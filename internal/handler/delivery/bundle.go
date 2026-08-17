package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

const maxBundleDependencies = 128

// BundleQueries is the narrow generated-query surface used by BundleHandler.
type BundleQueries interface {
	CountComponentBundles(context.Context, uuid.UUID) (int64, error)
	ListComponentBundles(context.Context, sqlc.ListComponentBundlesParams) ([]sqlc.ComponentBundle, error)
	CreateComponentBundle(context.Context, sqlc.CreateComponentBundleParams) (sqlc.ComponentBundle, error)
	GetComponentBundle(context.Context, sqlc.GetComponentBundleParams) (sqlc.ComponentBundle, error)
	UpdateComponentBundle(context.Context, sqlc.UpdateComponentBundleParams) (sqlc.ComponentBundle, error)
	DeleteComponentBundle(context.Context, sqlc.DeleteComponentBundleParams) (int64, error)
	CreateComponentBundleVersion(context.Context, sqlc.CreateComponentBundleVersionParams) (sqlc.ComponentBundleVersion, error)
	ListComponentBundleVersions(context.Context, sqlc.ListComponentBundleVersionsParams) ([]sqlc.ComponentBundleVersion, error)
	GetComponentBundleVersion(context.Context, sqlc.GetComponentBundleVersionParams) (sqlc.ComponentBundleVersion, error)
	GetDeliverySource(context.Context, sqlc.GetDeliverySourceParams) (sqlc.GetDeliverySourceRow, error)
	CreateDeliverySourceResolutionAndOutbox(context.Context, sqlc.CreateDeliverySourceResolutionAndOutboxParams) (sqlc.CreateDeliverySourceResolutionAndOutboxRow, error)
	FailComponentBundleVersion(context.Context, sqlc.FailComponentBundleVersionParams) (sqlc.ComponentBundleVersion, error)
}

// BundleHandler serves stable bundle identities and append-only immutable
// versions. Methods expect chi parameters named "id"/"bundleID" and
// "versionId"/"versionID" on nested routes.
type BundleHandler struct {
	queries BundleQueries
}

func NewBundleHandler(queries BundleQueries) *BundleHandler {
	return &BundleHandler{queries: queries}
}

// openapi:request DeliveryBundleWrite
type createBundleRequest struct {
	ProjectID   uuid.UUID `json:"project_id,omitempty"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
}

// openapi:request DeliveryBundleVersionWrite
type createBundleVersionRequest struct {
	ProjectID          uuid.UUID                `json:"project_id,omitempty"`
	Version            string                   `json:"version"`
	Spec               model.BundleVersionDraft `json:"spec"`
	DependencyBundleID []uuid.UUID              `json:"dependency_bundle_ids,omitempty"`
}

type bundleResponse struct {
	ID          uuid.UUID `json:"id"`
	ProjectID   uuid.UUID `json:"project_id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type bundleVersionResponse struct {
	ID                   uuid.UUID                     `json:"id"`
	BundleID             uuid.UUID                     `json:"bundle_id"`
	SourceID             uuid.UUID                     `json:"source_id"`
	Version              string                        `json:"version"`
	Renderer             model.RendererKind            `json:"renderer"`
	Scope                model.Scope                   `json:"scope"`
	RequestedRevision    string                        `json:"requested_revision"`
	ResolvedRevision     string                        `json:"resolved_revision,omitempty"`
	ArtifactDigest       string                        `json:"artifact_digest,omitempty"`
	RendererSpec         model.RendererSpec            `json:"renderer_spec"`
	ReconciliationPolicy model.ReconciliationPolicy    `json:"reconciliation_policy"`
	RequiredCapabilities []model.CapabilityRequirement `json:"required_capabilities"`
	DependencyBundleID   []uuid.UUID                   `json:"dependency_bundle_ids"`
	SpecDigest           model.Digest                  `json:"spec_digest"`
	VerificationStatus   string                        `json:"verification_status"`
	VerificationIdentity string                        `json:"verification_identity,omitempty"`
	State                string                        `json:"state"`
	LastErrorCode        string                        `json:"last_error_code,omitempty"`
	CreatedAt            time.Time                     `json:"created_at"`
}

// List returns stable bundle identities in a project.
func (h *BundleHandler) List(w http.ResponseWriter, r *http.Request) {
	projectID, err := projectIDFromRequest(r, uuid.Nil)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_project_scope", err.Error())
		return
	}
	limit, offset, err := parsePagination(r)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_pagination", err.Error())
		return
	}
	if h == nil || h.queries == nil {
		respondError(w, http.StatusServiceUnavailable, "service_unavailable", "delivery bundle persistence is unavailable")
		return
	}
	rows, err := h.queries.ListComponentBundles(r.Context(), sqlc.ListComponentBundlesParams{ProjectID: projectID, QueryOffset: offset, QueryLimit: limit})
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	total, err := h.queries.CountComponentBundles(r.Context(), projectID)
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	items := make([]bundleResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, bundleFromRow(row))
	}
	respondPage(w, r, items, total, limit, offset, int64(offset)+int64(len(items)) < total, true)
}

// Create adds a stable bundle identity. Versions are created separately and
// can never mutate this identity.
func (h *BundleHandler) Create(w http.ResponseWriter, r *http.Request) {
	if err := validateIdempotencyKey(r); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_idempotency_key", err.Error())
		return
	}
	var request createBundleRequest
	if err := decodeRequest(w, r, &request); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	projectID, err := projectIDFromRequest(r, request.ProjectID)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_project_scope", err.Error())
		return
	}
	if err := validateDisplayFields(request.Name, request.Description); err != nil {
		respondError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	if h == nil || h.queries == nil {
		respondError(w, http.StatusServiceUnavailable, "service_unavailable", "delivery bundle persistence is unavailable")
		return
	}
	actor := middleware.AuthenticatedUserUUID(r.Context())
	row, err := h.queries.CreateComponentBundle(r.Context(), sqlc.CreateComponentBundleParams{
		ProjectID: projectID, Name: request.Name, Description: request.Description, CreatedBy: actor, UpdatedBy: actor,
	})
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	recordAudit(r, h.queries, "delivery.bundle.created", "component_bundle", row.ID.String(), row.Name, map[string]any{
		"project_id": projectID.String(),
	})
	respondData(w, http.StatusCreated, bundleFromRow(row))
}

// Get returns one project-scoped bundle identity.
func (h *BundleHandler) Get(w http.ResponseWriter, r *http.Request) {
	projectID, bundleID, ok := bundleScope(w, r)
	if !ok {
		return
	}
	if h == nil || h.queries == nil {
		respondError(w, http.StatusServiceUnavailable, "service_unavailable", "delivery bundle persistence is unavailable")
		return
	}
	row, err := h.queries.GetComponentBundle(r.Context(), sqlc.GetComponentBundleParams{ID: bundleID, ProjectID: projectID})
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	respondData(w, http.StatusOK, bundleFromRow(row))
}

// Update replaces stable bundle metadata. Immutable versions are append-only
// and cannot be mutated through this path.
func (h *BundleHandler) Update(w http.ResponseWriter, r *http.Request) {
	if err := validateIdempotencyKey(r); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_idempotency_key", err.Error())
		return
	}
	var request createBundleRequest
	if err := decodeRequest(w, r, &request); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	projectID, err := projectIDFromRequest(r, request.ProjectID)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_project_scope", err.Error())
		return
	}
	bundleID, err := pathUUID(r, "id", "bundleID", "bundle_id")
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_resource_id", err.Error())
		return
	}
	if h == nil || h.queries == nil {
		respondError(w, http.StatusServiceUnavailable, "service_unavailable", "delivery bundle persistence is unavailable")
		return
	}
	current, err := h.queries.GetComponentBundle(r.Context(), sqlc.GetComponentBundleParams{ID: bundleID, ProjectID: projectID})
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	description := request.Description
	if strings.TrimSpace(request.Name) != "" && request.Name != current.Name {
		respondError(w, http.StatusBadRequest, "validation_error", "bundle name is immutable; create a new bundle identity")
		return
	}
	if err := validateDisplayFields(current.Name, description); err != nil {
		respondError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	row, err := h.queries.UpdateComponentBundle(r.Context(), sqlc.UpdateComponentBundleParams{
		Description: description, UpdatedBy: middleware.AuthenticatedUserUUID(r.Context()),
		ID: bundleID, ProjectID: projectID,
	})
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	recordAudit(r, h.queries, "delivery.bundle.updated", "component_bundle", row.ID.String(), row.Name, map[string]any{
		"project_id": projectID.String(),
	})
	respondData(w, http.StatusOK, bundleFromRow(row))
}

// Delete removes an unreferenced bundle identity. Versions or targets that
// still point at it surface as a typed 409.
func (h *BundleHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if err := validateIdempotencyKey(r); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_idempotency_key", err.Error())
		return
	}
	projectID, bundleID, ok := bundleScope(w, r)
	if !ok {
		return
	}
	if h == nil || h.queries == nil {
		respondError(w, http.StatusServiceUnavailable, "service_unavailable", "delivery bundle persistence is unavailable")
		return
	}
	deleted, err := h.queries.DeleteComponentBundle(r.Context(), sqlc.DeleteComponentBundleParams{ID: bundleID, ProjectID: projectID})
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	if deleted == 0 {
		respondDatabaseError(w, pgx.ErrNoRows)
		return
	}
	recordAudit(r, h.queries, "delivery.bundle.deleted", "component_bundle", bundleID.String(), "", map[string]any{
		"project_id": projectID.String(),
	})
	w.WriteHeader(http.StatusNoContent)
}

// ListVersions returns a bounded page only after proving the parent bundle is
// in the requested project. The generated query currently has no count twin;
// total_known=false makes count explicitly mean this page's item count.
func (h *BundleHandler) ListVersions(w http.ResponseWriter, r *http.Request) {
	projectID, bundleID, ok := bundleScope(w, r)
	if !ok {
		return
	}
	limit, offset, err := parsePagination(r)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_pagination", err.Error())
		return
	}
	if h == nil || h.queries == nil {
		respondError(w, http.StatusServiceUnavailable, "service_unavailable", "delivery bundle persistence is unavailable")
		return
	}
	if _, err := h.queries.GetComponentBundle(r.Context(), sqlc.GetComponentBundleParams{ID: bundleID, ProjectID: projectID}); err != nil {
		respondDatabaseError(w, err)
		return
	}
	rows, err := h.queries.ListComponentBundleVersions(r.Context(), sqlc.ListComponentBundleVersionsParams{
		BundleID: bundleID, QueryOffset: offset, QueryLimit: limit + 1,
	})
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	hasMore := len(rows) > int(limit)
	if hasMore {
		rows = rows[:limit]
	}
	items := make([]bundleVersionResponse, 0, len(rows))
	for _, row := range rows {
		converted, err := bundleVersionFromRow(row)
		if err != nil {
			respondError(w, http.StatusInternalServerError, "invalid_persisted_state", "stored delivery bundle version is invalid")
			return
		}
		items = append(items, converted)
	}
	respondPage(w, r, items, int64(len(items)), limit, offset, hasMore, false)
}

// CreateVersion appends a credential-free immutable version snapshot and a
// durable source-resolution request. A failed resolution enqueue marks the new
// version failed instead of leaving an unclaimable resolving row.
func (h *BundleHandler) CreateVersion(w http.ResponseWriter, r *http.Request) {
	if err := validateIdempotencyKey(r); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_idempotency_key", err.Error())
		return
	}
	var request createBundleVersionRequest
	if err := decodeRequest(w, r, &request); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	projectID, err := projectIDFromRequest(r, request.ProjectID)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_project_scope", err.Error())
		return
	}
	bundleID, err := pathUUID(r, "id", "bundleID", "bundle_id")
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_resource_id", err.Error())
		return
	}
	if h == nil || h.queries == nil {
		respondError(w, http.StatusServiceUnavailable, "service_unavailable", "delivery bundle persistence is unavailable")
		return
	}
	if err := validateVersionLabel(request.Version); err != nil {
		respondError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	if err := request.Spec.Validate(); err != nil {
		respondError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	if len(request.Spec.RequestedRevision) > 256 {
		respondError(w, http.StatusBadRequest, "validation_error", "requested_revision must be at most 256 bytes")
		return
	}
	if _, err := h.queries.GetComponentBundle(r.Context(), sqlc.GetComponentBundleParams{ID: bundleID, ProjectID: projectID}); err != nil {
		respondDatabaseError(w, err)
		return
	}
	source, err := h.queries.GetDeliverySource(r.Context(), sqlc.GetDeliverySourceParams{ID: request.Spec.SourceID, ProjectID: projectID})
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	if source.Status == "revoked" {
		respondError(w, http.StatusConflict, "source_revoked", "a revoked source cannot create bundle versions")
		return
	}
	dependencies, err := h.validateDependencies(r.Context(), projectID, bundleID, request.DependencyBundleID)
	if err != nil {
		if errors.Is(err, errInvalidDependencies) {
			respondError(w, http.StatusBadRequest, "validation_error", err.Error())
		} else {
			respondDatabaseError(w, err)
		}
		return
	}
	var trust model.TrustPolicy
	if err := decodeStrictJSON(source.TrustPolicy, &trust); err != nil {
		respondError(w, http.StatusInternalServerError, "invalid_persisted_state", "stored delivery source metadata is invalid")
		return
	}
	sourceSpecJSON, err := json.Marshal(struct {
		SourceID uuid.UUID         `json:"source_id"`
		Type     model.SourceType  `json:"type"`
		URL      string            `json:"url"`
		AuthMode model.AuthMode    `json:"auth_mode"`
		Trust    model.TrustPolicy `json:"trust_policy"`
	}{source.ID, model.SourceType(source.SourceType), source.Url, model.AuthMode(source.AuthMode), trust})
	if err != nil {
		respondError(w, http.StatusInternalServerError, "internal_error", "delivery bundle serialization failed")
		return
	}
	rendererJSON, _ := json.Marshal(request.Spec.Renderer)
	reconciliationJSON, _ := json.Marshal(request.Spec.Reconciliation)
	requirementsJSON, _ := json.Marshal(request.Spec.RequiredCapabilities)
	dependenciesJSON, _ := json.Marshal(dependencies)
	specDigest, err := model.CanonicalDigest(struct {
		Spec         model.BundleVersionDraft `json:"spec"`
		Dependencies []uuid.UUID              `json:"dependency_bundle_ids"`
	}{Spec: request.Spec, Dependencies: dependencies})
	if err != nil {
		respondError(w, http.StatusBadRequest, "validation_error", "delivery bundle version cannot be canonicalized")
		return
	}
	actor := middleware.AuthenticatedUserUUID(r.Context())
	row, err := h.queries.CreateComponentBundleVersion(r.Context(), sqlc.CreateComponentBundleVersionParams{
		BundleID: bundleID, SourceID: request.Spec.SourceID, Version: request.Version,
		Renderer: string(request.Spec.Renderer.Kind), Scope: string(request.Spec.Scope), RequestedRevision: request.Spec.RequestedRevision,
		SourceSpec: sourceSpecJSON, RendererSpec: rendererJSON, ReconciliationPolicy: reconciliationJSON,
		HealthPolicy: json.RawMessage(`{}`), Requirements: requirementsJSON, DependencyBundleIds: dependenciesJSON,
		SpecDigest: specDigest.String(), CreatedBy: actor,
	})
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	_, err = h.queries.CreateDeliverySourceResolutionAndOutbox(r.Context(), sqlc.CreateDeliverySourceResolutionAndOutboxParams{
		SourceID:          request.Spec.SourceID,
		BundleVersionID:   pgtype.UUID{Bytes: row.ID, Valid: true},
		RequestedRevision: request.Spec.RequestedRevision,
		ChartName:         bundleDraftChart(request.Spec),
	})
	if err != nil {
		_, _ = h.queries.FailComponentBundleVersion(r.Context(), sqlc.FailComponentBundleVersionParams{
			VerificationStatus: "failed", LastErrorCode: "resolution_enqueue_failed", ID: row.ID,
		})
		respondError(w, http.StatusInternalServerError, "resolution_enqueue_failed", "bundle version source resolution could not be scheduled")
		return
	}
	response, err := bundleVersionFromRow(row)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "invalid_persisted_state", "stored delivery bundle version is invalid")
		return
	}
	recordAudit(r, h.queries, "delivery.bundle.version_created", "component_bundle_version", row.ID.String(), request.Version, map[string]any{
		"project_id":       projectID.String(),
		"bundle_id":        bundleID.String(),
		"source_id":        request.Spec.SourceID.String(),
		"renderer":         string(request.Spec.Renderer.Kind),
		"scope":            string(request.Spec.Scope),
		"spec_digest":      specDigest.String(),
		"dependency_count": len(dependencies),
	})
	respondData(w, http.StatusCreated, response)
}

func bundleDraftChart(spec model.BundleVersionDraft) string {
	if spec.Renderer.Helm == nil {
		return ""
	}
	return spec.Renderer.Helm.Chart
}

// GetVersion retrieves a nested version and verifies that it belongs to the
// bundle in the URL, preventing within-project nested-resource confusion.
func (h *BundleHandler) GetVersion(w http.ResponseWriter, r *http.Request) {
	projectID, bundleID, ok := bundleScope(w, r)
	if !ok {
		return
	}
	versionID, err := pathUUID(r, "versionId", "versionID", "version_id")
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_resource_id", err.Error())
		return
	}
	if h == nil || h.queries == nil {
		respondError(w, http.StatusServiceUnavailable, "service_unavailable", "delivery bundle persistence is unavailable")
		return
	}
	row, err := h.queries.GetComponentBundleVersion(r.Context(), sqlc.GetComponentBundleVersionParams{ID: versionID, ProjectID: projectID})
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	if row.BundleID != bundleID {
		respondError(w, http.StatusNotFound, "not_found", "delivery resource not found")
		return
	}
	response, err := bundleVersionFromRow(row)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "invalid_persisted_state", "stored delivery bundle version is invalid")
		return
	}
	respondData(w, http.StatusOK, response)
}

var errInvalidDependencies = errors.New("dependency bundle IDs are invalid")

func (h *BundleHandler) validateDependencies(ctx context.Context, projectID, bundleID uuid.UUID, values []uuid.UUID) ([]uuid.UUID, error) {
	if len(values) > maxBundleDependencies {
		return nil, errors.Join(errInvalidDependencies, errors.New("dependency_bundle_ids must contain at most 128 entries"))
	}
	result := append([]uuid.UUID(nil), values...)
	sort.Slice(result, func(i, j int) bool { return result[i].String() < result[j].String() })
	for index, dependencyID := range result {
		if dependencyID == uuid.Nil {
			return nil, errors.Join(errInvalidDependencies, errors.New("dependency_bundle_ids must contain only non-zero UUIDs"))
		}
		if dependencyID == bundleID {
			return nil, errors.Join(errInvalidDependencies, errors.New("a bundle cannot depend on itself"))
		}
		if index > 0 && dependencyID == result[index-1] {
			return nil, errors.Join(errInvalidDependencies, errors.New("dependency_bundle_ids must be unique"))
		}
		if _, err := h.queries.GetComponentBundle(ctx, sqlc.GetComponentBundleParams{ID: dependencyID, ProjectID: projectID}); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func bundleScope(w http.ResponseWriter, r *http.Request) (uuid.UUID, uuid.UUID, bool) {
	projectID, err := projectIDFromRequest(r, uuid.Nil)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_project_scope", err.Error())
		return uuid.Nil, uuid.Nil, false
	}
	bundleID, err := pathUUID(r, "id", "bundleID", "bundle_id")
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_resource_id", err.Error())
		return uuid.Nil, uuid.Nil, false
	}
	return projectID, bundleID, true
}

func validateVersionLabel(value string) error {
	if value == "" || value != strings.TrimSpace(value) || len(value) > 128 || !utf8.ValidString(value) || containsControl(value) {
		return errors.New("version must be 1 through 128 UTF-8 bytes without surrounding whitespace or control characters")
	}
	return nil
}

func bundleFromRow(row sqlc.ComponentBundle) bundleResponse {
	return bundleResponse{ID: row.ID, ProjectID: row.ProjectID, Name: row.Name, Description: row.Description, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}

func bundleVersionFromRow(row sqlc.ComponentBundleVersion) (bundleVersionResponse, error) {
	var renderer model.RendererSpec
	if err := decodeStrictJSON(row.RendererSpec, &renderer); err != nil {
		return bundleVersionResponse{}, err
	}
	if renderer.Kind != model.RendererKind(row.Renderer) {
		return bundleVersionResponse{}, errors.New("renderer metadata mismatch")
	}
	if err := renderer.Validate(); err != nil {
		return bundleVersionResponse{}, err
	}
	var reconciliation model.ReconciliationPolicy
	if err := decodeStrictJSON(row.ReconciliationPolicy, &reconciliation); err != nil {
		return bundleVersionResponse{}, err
	}
	if err := reconciliation.Validate(); err != nil {
		return bundleVersionResponse{}, err
	}
	var requirements []model.CapabilityRequirement
	if err := decodeStrictJSON(row.Requirements, &requirements); err != nil {
		return bundleVersionResponse{}, err
	}
	if _, err := model.CapabilityRequirementsCanonical(requirements); err != nil {
		return bundleVersionResponse{}, err
	}
	var dependencies []uuid.UUID
	if err := decodeStrictJSON(row.DependencyBundleIds, &dependencies); err != nil {
		return bundleVersionResponse{}, err
	}
	digest, err := model.ParseDigest(row.SpecDigest)
	if err != nil {
		return bundleVersionResponse{}, err
	}
	return bundleVersionResponse{
		ID: row.ID, BundleID: row.BundleID, SourceID: row.SourceID, Version: row.Version,
		Renderer: model.RendererKind(row.Renderer), Scope: model.Scope(row.Scope),
		RequestedRevision: row.RequestedRevision, ResolvedRevision: row.ResolvedRevision, ArtifactDigest: row.ArtifactDigest,
		RendererSpec: renderer, ReconciliationPolicy: reconciliation, RequiredCapabilities: requirements,
		DependencyBundleID: dependencies, SpecDigest: digest, VerificationStatus: row.VerificationStatus,
		VerificationIdentity: row.VerificationIdentity, State: row.State, LastErrorCode: row.LastErrorCode, CreatedAt: row.CreatedAt,
	}, nil
}
