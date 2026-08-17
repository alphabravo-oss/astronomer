package delivery

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/placement"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/rollout"
	"github.com/alphabravocompany/astronomer-go/internal/events"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

type TargetQueries interface {
	CountDeliveryTargets(context.Context, uuid.UUID) (int64, error)
	ListDeliveryTargets(context.Context, sqlc.ListDeliveryTargetsParams) ([]sqlc.DeliveryTarget, error)
	CreateDeliveryTarget(context.Context, sqlc.CreateDeliveryTargetParams) (sqlc.DeliveryTarget, error)
	GetDeliveryTarget(context.Context, sqlc.GetDeliveryTargetParams) (sqlc.DeliveryTarget, error)
	GetDeliveryTargetByName(context.Context, sqlc.GetDeliveryTargetByNameParams) (sqlc.DeliveryTarget, error)
	UpdateDeliveryTargetCAS(context.Context, sqlc.UpdateDeliveryTargetCASParams) (sqlc.DeliveryTarget, error)
	RequestDeliveryTargetDeletionCAS(context.Context, sqlc.RequestDeliveryTargetDeletionCASParams) (sqlc.RequestDeliveryTargetDeletionCASRow, error)
	MarkDeliveryTargetOrphaned(context.Context, sqlc.MarkDeliveryTargetOrphanedParams) (sqlc.MarkDeliveryTargetOrphanedRow, error)
	GetComponentBundleVersion(context.Context, sqlc.GetComponentBundleVersionParams) (sqlc.ComponentBundleVersion, error)
}

type TargetPreviewer interface {
	Preview(context.Context, uuid.UUID) (rollout.PlanningSnapshot, placement.Result, error)
}

type PlatformScopeChecker interface {
	GetUserByID(context.Context, uuid.UUID) (sqlc.User, error)
}

type TargetHandler struct {
	queries   TargetQueries
	previewer TargetPreviewer
	bus       *events.Bus
	platform  PlatformScopeChecker
}

func (h *TargetHandler) SetPlatformScopeChecker(checker PlatformScopeChecker) {
	if h != nil {
		h.platform = checker
	}
}

func NewTargetHandler(queries TargetQueries, previewer TargetPreviewer, bus *events.Bus) *TargetHandler {
	return &TargetHandler{queries: queries, previewer: previewer, bus: bus}
}

type rolloutPolicy struct {
	ApprovalRequired bool `json:"approval_required"`
}

// openapi:request DeliveryTargetWrite
type targetRequest struct {
	ProjectID               uuid.UUID                  `json:"project_id,omitempty"`
	Name                    string                     `json:"name"`
	Description             string                     `json:"description,omitempty"`
	BundleVersionID         uuid.UUID                  `json:"bundle_version_id"`
	Placement               model.Placement            `json:"placement"`
	RolloutPolicy           rolloutPolicy              `json:"rollout_policy"`
	ReconciliationPolicy    model.ReconciliationPolicy `json:"reconciliation_policy"`
	MaintenanceWindowPolicy json.RawMessage            `json:"maintenance_window_policy,omitempty"`
	Suspended               bool                       `json:"suspended"`
}

// openapi:request DeliveryTargetPatch
type updateTargetRequest struct {
	ProjectID               uuid.UUID                   `json:"project_id,omitempty"`
	Description             *string                     `json:"description,omitempty"`
	BundleVersionID         *uuid.UUID                  `json:"bundle_version_id,omitempty"`
	Placement               *model.Placement            `json:"placement,omitempty"`
	RolloutPolicy           *rolloutPolicy              `json:"rollout_policy,omitempty"`
	ReconciliationPolicy    *model.ReconciliationPolicy `json:"reconciliation_policy,omitempty"`
	MaintenanceWindowPolicy json.RawMessage             `json:"maintenance_window_policy,omitempty"`
	Suspended               *bool                       `json:"suspended,omitempty"`
}

type targetResponse struct {
	ID                      uuid.UUID                  `json:"id"`
	ProjectID               uuid.UUID                  `json:"project_id"`
	Name                    string                     `json:"name"`
	Description             string                     `json:"description,omitempty"`
	BundleVersionID         uuid.UUID                  `json:"bundle_version_id"`
	Placement               model.Placement            `json:"placement"`
	RolloutPolicy           rolloutPolicy              `json:"rollout_policy"`
	ReconciliationPolicy    model.ReconciliationPolicy `json:"reconciliation_policy"`
	MaintenanceWindowPolicy json.RawMessage            `json:"maintenance_window_policy"`
	Suspended               bool                       `json:"suspended"`
	Generation              int64                      `json:"generation"`
	ResourceVersion         int64                      `json:"resource_version"`
	DeletionState           string                     `json:"deletion_state"`
	CreatedAt               time.Time                  `json:"created_at"`
	UpdatedAt               time.Time                  `json:"updated_at"`
}

func (h *TargetHandler) List(w http.ResponseWriter, r *http.Request) {
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
		respondError(w, http.StatusServiceUnavailable, "service_unavailable", "delivery target persistence is unavailable")
		return
	}
	if name := strings.TrimSpace(r.URL.Query().Get("name")); name != "" {
		row, err := h.queries.GetDeliveryTargetByName(r.Context(), sqlc.GetDeliveryTargetByNameParams{ProjectID: projectID, Name: name})
		if errors.Is(err, pgx.ErrNoRows) {
			respondPage(w, r, []targetResponse{}, 0, limit, offset, false, true)
			return
		}
		if err != nil {
			respondDatabaseError(w, err)
			return
		}
		item, err := targetFromRow(row)
		if err != nil {
			respondError(w, http.StatusInternalServerError, "invalid_persisted_state", "stored delivery target is invalid")
			return
		}
		setEntityTag(w, row.ResourceVersion)
		respondPage(w, r, []targetResponse{item}, 1, limit, offset, false, true)
		return
	}
	rows, err := h.queries.ListDeliveryTargets(r.Context(), sqlc.ListDeliveryTargetsParams{ProjectID: projectID, QueryLimit: limit, QueryOffset: offset})
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	total, err := h.queries.CountDeliveryTargets(r.Context(), projectID)
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	items := make([]targetResponse, 0, len(rows))
	for _, row := range rows {
		item, err := targetFromRow(row)
		if err != nil {
			respondError(w, http.StatusInternalServerError, "invalid_persisted_state", "stored delivery target is invalid")
			return
		}
		items = append(items, item)
	}
	respondPage(w, r, items, total, limit, offset, int64(offset)+int64(len(items)) < total, true)
}

func (h *TargetHandler) Create(w http.ResponseWriter, r *http.Request) {
	if err := validateIdempotencyKey(r); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_idempotency_key", err.Error())
		return
	}
	var request targetRequest
	if err := decodeRequest(w, r, &request); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	projectID, err := projectIDFromRequest(r, request.ProjectID)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_project_scope", err.Error())
		return
	}
	if err := validateTargetRequest(request); err != nil {
		respondError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	if h == nil || h.queries == nil {
		respondError(w, http.StatusServiceUnavailable, "service_unavailable", "delivery target persistence is unavailable")
		return
	}
	if err := h.requireReadyBundle(r.Context(), projectID, request.BundleVersionID); err != nil {
		respondTargetBundleError(w, err)
		return
	}
	placementJSON, _ := json.Marshal(request.Placement)
	rolloutJSON, _ := json.Marshal(request.RolloutPolicy)
	reconcileJSON, _ := json.Marshal(request.ReconciliationPolicy)
	maintenanceJSON, err := canonicalJSONObject(request.MaintenanceWindowPolicy)
	if err != nil {
		respondError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	actor := middleware.AuthenticatedUserUUID(r.Context())
	row, err := h.queries.CreateDeliveryTarget(r.Context(), sqlc.CreateDeliveryTargetParams{
		ProjectID: projectID, Name: request.Name, Description: request.Description,
		BundleVersionID: request.BundleVersionID, Placement: placementJSON,
		RolloutPolicy: rolloutJSON, ReconciliationPolicy: reconcileJSON,
		MaintenanceWindowPolicy: maintenanceJSON, Suspended: request.Suspended,
		CreatedBy: actor, UpdatedBy: actor,
	})
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	response, err := targetFromRow(row)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "invalid_persisted_state", "stored delivery target is invalid")
		return
	}
	setEntityTag(w, row.ResourceVersion)
	events.PublishChanged(h.bus, "delivery_target", "", row.ID.String(), map[string]any{"project_id": projectID.String(), "action": "created"})
	recordAudit(r, h.queries, "delivery.target.created", "delivery_target", row.ID.String(), row.Name, map[string]any{
		"project_id":        projectID.String(),
		"bundle_version_id": row.BundleVersionID.String(),
		"approval_required": request.RolloutPolicy.ApprovalRequired,
		"suspended":         row.Suspended,
		"generation":        row.Generation,
		"resource_version":  row.ResourceVersion,
	})
	respondData(w, http.StatusCreated, response)
}

func (h *TargetHandler) Get(w http.ResponseWriter, r *http.Request) {
	projectID, targetID, ok := targetScope(w, r)
	if !ok {
		return
	}
	if h == nil || h.queries == nil {
		respondError(w, http.StatusServiceUnavailable, "service_unavailable", "delivery target persistence is unavailable")
		return
	}
	row, err := h.queries.GetDeliveryTarget(r.Context(), sqlc.GetDeliveryTargetParams{ID: targetID, ProjectID: projectID})
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	response, err := targetFromRow(row)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "invalid_persisted_state", "stored delivery target is invalid")
		return
	}
	setEntityTag(w, row.ResourceVersion)
	respondData(w, http.StatusOK, response)
}

func (h *TargetHandler) Update(w http.ResponseWriter, r *http.Request) {
	expected, err := requireIfMatch(r)
	if err != nil {
		respondError(w, http.StatusPreconditionRequired, "if_match_required", err.Error())
		return
	}
	if err := validateIdempotencyKey(r); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_idempotency_key", err.Error())
		return
	}
	var request updateTargetRequest
	if err := decodeRequest(w, r, &request); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	projectID, err := projectIDFromRequest(r, request.ProjectID)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_project_scope", err.Error())
		return
	}
	targetID, err := pathUUID(r, "id", "targetID")
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_resource_id", err.Error())
		return
	}
	if h == nil || h.queries == nil {
		respondError(w, http.StatusServiceUnavailable, "service_unavailable", "delivery target persistence is unavailable")
		return
	}
	current, err := h.queries.GetDeliveryTarget(r.Context(), sqlc.GetDeliveryTargetParams{ID: targetID, ProjectID: projectID})
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	if current.ResourceVersion != expected {
		respondError(w, http.StatusPreconditionFailed, "stale_resource_version", "If-Match does not match the current target resource version")
		return
	}
	merged, err := mergeTargetUpdate(current, request)
	if err != nil {
		respondError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	if err := h.requireReadyBundle(r.Context(), projectID, merged.BundleVersionID); err != nil {
		respondTargetBundleError(w, err)
		return
	}
	placementJSON, _ := json.Marshal(merged.Placement)
	rolloutJSON, _ := json.Marshal(merged.RolloutPolicy)
	reconcileJSON, _ := json.Marshal(merged.ReconciliationPolicy)
	row, err := h.queries.UpdateDeliveryTargetCAS(r.Context(), sqlc.UpdateDeliveryTargetCASParams{
		Description: merged.Description, BundleVersionID: merged.BundleVersionID,
		Placement: placementJSON, RolloutPolicy: rolloutJSON,
		ReconciliationPolicy: reconcileJSON, MaintenanceWindowPolicy: merged.MaintenanceWindowPolicy,
		Suspended: merged.Suspended, UpdatedBy: middleware.AuthenticatedUserUUID(r.Context()),
		ID: targetID, ProjectID: projectID, ExpectedResourceVersion: expected,
	})
	if err != nil {
		respondCASError(w, err)
		return
	}
	response, err := targetFromRow(row)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "invalid_persisted_state", "stored delivery target is invalid")
		return
	}
	setEntityTag(w, row.ResourceVersion)
	events.PublishChanged(h.bus, "delivery_target", "", row.ID.String(), map[string]any{"project_id": projectID.String(), "action": "updated"})
	recordAudit(r, h.queries, "delivery.target.updated", "delivery_target", row.ID.String(), row.Name, map[string]any{
		"project_id":        projectID.String(),
		"bundle_version_id": row.BundleVersionID.String(),
		"changed_fields":    targetChangedFields(request),
		"generation":        row.Generation,
		"resource_version":  row.ResourceVersion,
	})
	respondData(w, http.StatusOK, response)
}

func (h *TargetHandler) Delete(w http.ResponseWriter, r *http.Request) {
	expected, err := requireIfMatch(r)
	if err != nil {
		respondError(w, http.StatusPreconditionRequired, "if_match_required", err.Error())
		return
	}
	projectID, targetID, ok := targetScope(w, r)
	if !ok {
		return
	}
	if h == nil || h.queries == nil {
		respondError(w, http.StatusServiceUnavailable, "service_unavailable", "delivery target persistence is unavailable")
		return
	}
	row, err := h.queries.RequestDeliveryTargetDeletionCAS(r.Context(), sqlc.RequestDeliveryTargetDeletionCASParams{
		UpdatedBy: middleware.AuthenticatedUserUUID(r.Context()), ID: targetID, ProjectID: projectID,
		ExpectedResourceVersion: expected,
	})
	if err != nil {
		respondCASError(w, err)
		return
	}
	setEntityTag(w, row.ResourceVersion)
	events.PublishChanged(h.bus, "delivery_target", "", row.ID.String(), map[string]any{"project_id": projectID.String(), "action": "deleting", "deployment_count": row.DeploymentCount})
	recordAudit(r, h.queries, "delivery.target.deletion_requested", "delivery_target", row.ID.String(), "", map[string]any{
		"project_id":       projectID.String(),
		"deployment_count": row.DeploymentCount,
		"deletion_state":   row.DeletionState,
		"resource_version": row.ResourceVersion,
	})
	respondData(w, http.StatusAccepted, map[string]any{
		"id": row.ID, "deletion_state": row.DeletionState, "resource_version": row.ResourceVersion,
		"deployment_count": row.DeploymentCount,
	})
}

func (h *TargetHandler) Orphan(w http.ResponseWriter, r *http.Request) {
	expected, err := requireIfMatch(r)
	if err != nil {
		respondError(w, http.StatusPreconditionRequired, "if_match_required", err.Error())
		return
	}
	projectID, targetID, ok := targetScope(w, r)
	if !ok {
		return
	}
	if h == nil || h.queries == nil {
		respondError(w, http.StatusServiceUnavailable, "service_unavailable", "delivery target persistence is unavailable")
		return
	}
	row, err := h.queries.MarkDeliveryTargetOrphaned(r.Context(), sqlc.MarkDeliveryTargetOrphanedParams{
		UpdatedBy: middleware.AuthenticatedUserUUID(r.Context()), ID: targetID, ProjectID: projectID,
		ExpectedResourceVersion: expected,
	})
	if err != nil {
		respondCASError(w, err)
		return
	}
	setEntityTag(w, row.ResourceVersion)
	events.PublishChanged(h.bus, "delivery_target", "", row.ID.String(), map[string]any{"project_id": projectID.String(), "action": "orphaned"})
	recordAudit(r, h.queries, "delivery.target.orphaned", "delivery_target", row.ID.String(), "", map[string]any{
		"project_id":       projectID.String(),
		"deletion_state":   row.DeletionState,
		"resource_version": row.ResourceVersion,
	})
	respondData(w, http.StatusOK, map[string]any{"id": row.ID, "deletion_state": row.DeletionState, "resource_version": row.ResourceVersion})
}

func targetChangedFields(request updateTargetRequest) []string {
	fields := make([]string, 0, 6)
	if request.Description != nil {
		fields = append(fields, "description")
	}
	if request.BundleVersionID != nil {
		fields = append(fields, "bundle_version_id")
	}
	if request.Placement != nil {
		fields = append(fields, "placement")
	}
	if request.RolloutPolicy != nil {
		fields = append(fields, "rollout_policy")
	}
	if request.ReconciliationPolicy != nil {
		fields = append(fields, "reconciliation_policy")
	}
	if request.MaintenanceWindowPolicy != nil {
		fields = append(fields, "maintenance_window_policy")
	}
	if request.Suspended != nil {
		fields = append(fields, "suspended")
	}
	return fields
}

func (h *TargetHandler) Preview(w http.ResponseWriter, r *http.Request) {
	projectID, targetID, ok := targetScope(w, r)
	if !ok {
		return
	}
	if h == nil || h.previewer == nil {
		respondError(w, http.StatusServiceUnavailable, "service_unavailable", "delivery placement preview is unavailable")
		return
	}
	snapshot, result, err := h.previewer.Preview(r.Context(), targetID)
	if err != nil {
		respondRolloutError(w, err)
		return
	}
	if snapshot.ProjectID != projectID {
		respondError(w, http.StatusNotFound, "not_found", "delivery resource not found")
		return
	}
	pageSize, cursor, err := previewPageRequest(r)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_pagination", err.Error())
		return
	}
	offset := 0
	if cursor != nil {
		if cursor.PreviewDigest != result.PreviewDigest {
			respondError(w, http.StatusConflict, "preview_cursor_stale", "placement changed; request a fresh preview")
			return
		}
		offset = cursor.Offset
	}
	if offset > len(result.Decisions) {
		respondError(w, http.StatusBadRequest, "invalid_pagination", "preview cursor offset is outside the decision set")
		return
	}
	end := offset + pageSize
	if end > len(result.Decisions) {
		end = len(result.Decisions)
	}
	decisions := result.Decisions[offset:end]
	hasMore := end < len(result.Decisions)
	nextCursor := ""
	if hasMore {
		nextCursor = encodePreviewCursor(previewCursor{PreviewDigest: result.PreviewDigest, Offset: end})
	}
	risks := previewRisks(result)
	events.PublishChanged(h.bus, "delivery_target", "", targetID.String(), map[string]any{"project_id": projectID.String(), "action": "previewed"})
	respondData(w, http.StatusOK, map[string]any{
		"target_id": targetID, "target_generation": snapshot.TargetGeneration,
		"bundle_version_id": snapshot.Desired.BundleVersionID,
		"preview_digest":    result.PreviewDigest, "selected_count": result.SelectedCount,
		"excluded_count": result.ExcludedCount, "requires_all_confirmation": result.RequiresAllConfirmation,
		"decisions": decisions, "decision_count": len(result.Decisions), "decision_offset": offset,
		"decision_page_size": pageSize, "has_more_decisions": hasMore, "next_cursor": nextCursor,
		"risks": risks,
	})
}

const (
	defaultPreviewPageSize = 100
	maxPreviewPageSize     = 500
)

type previewCursor struct {
	PreviewDigest model.Digest `json:"preview_digest"`
	Offset        int          `json:"offset"`
}

func previewPageRequest(r *http.Request) (int, *previewCursor, error) {
	pageSize := defaultPreviewPageSize
	if values, ok := r.URL.Query()["page_size"]; ok {
		if len(values) != 1 {
			return 0, nil, errors.New("page_size must occur exactly once")
		}
		parsed, err := strconv.Atoi(values[0])
		if err != nil || parsed < 1 || parsed > maxPreviewPageSize {
			return 0, nil, fmt.Errorf("page_size must be between 1 and %d", maxPreviewPageSize)
		}
		pageSize = parsed
	}
	values, ok := r.URL.Query()["cursor"]
	if !ok {
		return pageSize, nil, nil
	}
	if len(values) != 1 || values[0] == "" || len(values[0]) > 4096 {
		return 0, nil, errors.New("cursor must be one bounded opaque value")
	}
	raw, err := base64.RawURLEncoding.DecodeString(values[0])
	if err != nil {
		return 0, nil, errors.New("cursor is malformed")
	}
	var cursor previewCursor
	if err := decodeStrictJSON(raw, &cursor); err != nil || cursor.PreviewDigest.Validate() != nil || cursor.Offset < 0 || cursor.Offset > model.MaxPlacementIDs {
		return 0, nil, errors.New("cursor is malformed")
	}
	return pageSize, &cursor, nil
}

func encodePreviewCursor(cursor previewCursor) string {
	raw, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func validateTargetRequest(request targetRequest) error {
	if err := validateDisplayFields(request.Name, request.Description); err != nil {
		return err
	}
	if request.BundleVersionID == uuid.Nil {
		return errors.New("bundle_version_id must be a non-zero UUID")
	}
	if err := request.Placement.Validate(); err != nil {
		return err
	}
	if err := request.ReconciliationPolicy.Validate(); err != nil {
		return err
	}
	_, err := canonicalJSONObject(request.MaintenanceWindowPolicy)
	return err
}

func (h *TargetHandler) requireReadyBundle(ctx context.Context, projectID, versionID uuid.UUID) error {
	row, err := h.queries.GetComponentBundleVersion(ctx, sqlc.GetComponentBundleVersionParams{ID: versionID, ProjectID: projectID})
	if err != nil {
		return err
	}
	if row.State != "ready" || row.VerificationStatus != "verified" {
		return errBundleNotReady
	}
	if row.Scope == string(model.ScopePlatform) {
		actor := middleware.AuthenticatedUserUUID(ctx)
		if !actor.Valid || h.platform == nil {
			return errPlatformScopeForbidden
		}
		user, err := h.platform.GetUserByID(ctx, uuid.UUID(actor.Bytes))
		if err != nil || !user.IsSuperuser {
			return errPlatformScopeForbidden
		}
	}
	return nil
}

var (
	errBundleNotReady         = errors.New("bundle version must be ready and verified")
	errPlatformScopeForbidden = errors.New("platform-scoped bundle targets require a superuser")
)

func respondTargetBundleError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errBundleNotReady):
		respondError(w, http.StatusConflict, "bundle_not_ready", err.Error())
	case errors.Is(err, errPlatformScopeForbidden):
		respondError(w, http.StatusForbidden, "platform_scope_forbidden", err.Error())
	default:
		respondDatabaseError(w, err)
	}
}

func targetFromRow(row sqlc.DeliveryTarget) (targetResponse, error) {
	var placementValue model.Placement
	var rolloutValue rolloutPolicy
	var reconciliation model.ReconciliationPolicy
	if err := decodeStrictJSON(row.Placement, &placementValue); err != nil {
		return targetResponse{}, err
	}
	if err := decodeStrictJSON(row.RolloutPolicy, &rolloutValue); err != nil {
		return targetResponse{}, err
	}
	if err := decodeStrictJSON(row.ReconciliationPolicy, &reconciliation); err != nil {
		return targetResponse{}, err
	}
	maintenance, err := canonicalJSONObject(row.MaintenanceWindowPolicy)
	if err != nil {
		return targetResponse{}, err
	}
	return targetResponse{
		ID: row.ID, ProjectID: row.ProjectID, Name: row.Name, Description: row.Description,
		BundleVersionID: row.BundleVersionID, Placement: placementValue, RolloutPolicy: rolloutValue,
		ReconciliationPolicy: reconciliation, MaintenanceWindowPolicy: maintenance,
		Suspended: row.Suspended, Generation: row.Generation, ResourceVersion: row.ResourceVersion,
		DeletionState: row.DeletionState, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}, nil
}

func mergeTargetUpdate(current sqlc.DeliveryTarget, request updateTargetRequest) (targetResponse, error) {
	merged, err := targetFromRow(current)
	if err != nil {
		return targetResponse{}, errors.New("stored target cannot be updated")
	}
	if request.Description != nil {
		merged.Description = *request.Description
	}
	if request.BundleVersionID != nil {
		merged.BundleVersionID = *request.BundleVersionID
	}
	if request.Placement != nil {
		merged.Placement = *request.Placement
	}
	if request.RolloutPolicy != nil {
		merged.RolloutPolicy = *request.RolloutPolicy
	}
	if request.ReconciliationPolicy != nil {
		merged.ReconciliationPolicy = *request.ReconciliationPolicy
	}
	if request.MaintenanceWindowPolicy != nil {
		merged.MaintenanceWindowPolicy, err = canonicalJSONObject(request.MaintenanceWindowPolicy)
		if err != nil {
			return targetResponse{}, err
		}
	}
	if request.Suspended != nil {
		merged.Suspended = *request.Suspended
	}
	if err := validateDisplayFields(merged.Name, merged.Description); err != nil {
		return targetResponse{}, err
	}
	if merged.BundleVersionID == uuid.Nil {
		return targetResponse{}, errors.New("bundle_version_id must be a non-zero UUID")
	}
	if err := merged.Placement.Validate(); err != nil {
		return targetResponse{}, err
	}
	if err := merged.ReconciliationPolicy.Validate(); err != nil {
		return targetResponse{}, err
	}
	return merged, nil
}

func targetScope(w http.ResponseWriter, r *http.Request) (uuid.UUID, uuid.UUID, bool) {
	projectID, err := projectIDFromRequest(r, uuid.Nil)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_project_scope", err.Error())
		return uuid.Nil, uuid.Nil, false
	}
	targetID, err := pathUUID(r, "id", "targetID")
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_resource_id", err.Error())
		return uuid.Nil, uuid.Nil, false
	}
	return projectID, targetID, true
}

func canonicalJSONObject(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "null" {
		return json.RawMessage("{}"), nil
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return nil, errors.New("maintenance_window_policy must be a JSON object")
	}
	encoded, err := json.Marshal(object)
	if err != nil || len(encoded) > 64<<10 {
		return nil, errors.New("maintenance_window_policy must be at most 64 KiB")
	}
	return encoded, nil
}

func previewRisks(result placement.Result) []string {
	risks := make(map[string]struct{})
	if result.RequiresAllConfirmation {
		risks["all_clusters"] = struct{}{}
	}
	if result.SelectedCount > 100 {
		risks["large_blast_radius"] = struct{}{}
	}
	for _, decision := range result.Decisions {
		switch decision.Reason {
		case placement.ReasonDisconnected:
			risks["disconnected_clusters_excluded"] = struct{}{}
		case placement.ReasonIncompatible:
			risks["incompatible_clusters_excluded"] = struct{}{}
		case placement.ReasonMissingCapability:
			risks["capability_blockers"] = struct{}{}
		}
	}
	items := make([]string, 0, len(risks))
	for risk := range risks {
		items = append(items, risk)
	}
	sort.Strings(items)
	return items
}

func respondCASError(w http.ResponseWriter, err error) {
	if errors.Is(err, context.Canceled) {
		respondError(w, http.StatusRequestTimeout, "request_canceled", "delivery operation was canceled")
		return
	}
	// A project-scoped CAS update returning no rows deliberately does not
	// distinguish a missing resource from stale state.
	if errors.Is(err, pgx.ErrNoRows) {
		respondError(w, http.StatusPreconditionFailed, "stale_resource_version", "resource state changed; fetch it and retry with the current ETag")
		return
	}
	respondDatabaseError(w, err)
}

func respondRolloutError(w http.ResponseWriter, err error) {
	switch {
	case rollout.HasCode(err, rollout.CodePreviewStale), rollout.HasCode(err, rollout.CodeTargetChanged), rollout.HasCode(err, rollout.CodeIdempotencyConflict):
		respondError(w, http.StatusConflict, string(extractRolloutCode(err)), err.Error())
	case rollout.HasCode(err, rollout.CodeInvalidInput), rollout.HasCode(err, rollout.CodeNoClusters), rollout.HasCode(err, rollout.CodeInvalidCohorts):
		respondError(w, http.StatusBadRequest, string(extractRolloutCode(err)), err.Error())
	default:
		respondDatabaseError(w, err)
	}
}

func extractRolloutCode(err error) rollout.ErrorCode {
	var typed *rollout.Error
	if errors.As(err, &typed) {
		return typed.Code
	}
	return rollout.CodeInvariant
}
