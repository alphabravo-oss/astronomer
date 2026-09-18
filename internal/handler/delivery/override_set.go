package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	deliveryconfig "github.com/alphabravocompany/astronomer-go/internal/delivery/configuration"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

type OverrideSetQueries interface {
	CountDeliveryOverrideSets(context.Context, uuid.UUID) (int64, error)
	ListDeliveryOverrideSets(context.Context, sqlc.ListDeliveryOverrideSetsParams) ([]sqlc.DeliveryOverrideSet, error)
	ListDeliveryOverrideSetsByIDs(context.Context, sqlc.ListDeliveryOverrideSetsByIDsParams) ([]sqlc.DeliveryOverrideSet, error)
	GetDeliveryOverrideSet(context.Context, sqlc.GetDeliveryOverrideSetParams) (sqlc.DeliveryOverrideSet, error)
	GetDeliveryConfigurationTemplate(context.Context, sqlc.GetDeliveryConfigurationTemplateParams) (sqlc.DeliveryConfigurationTemplate, error)
	CreateDeliveryOverrideSet(context.Context, sqlc.CreateDeliveryOverrideSetParams) (sqlc.DeliveryOverrideSet, error)
	UpdateDeliveryOverrideSet(context.Context, sqlc.UpdateDeliveryOverrideSetParams) (sqlc.DeliveryOverrideSet, error)
	DeleteDeliveryOverrideSet(context.Context, sqlc.DeleteDeliveryOverrideSetParams) (uuid.UUID, error)
}

type OverrideSetHandler struct{ queries OverrideSetQueries }

func NewOverrideSetHandler(queries OverrideSetQueries) *OverrideSetHandler {
	return &OverrideSetHandler{queries: queries}
}

// openapi:request DeliveryOverrideSetWrite
type overrideSetWrite struct {
	ProjectID  uuid.UUID            `json:"project_id,omitempty"`
	TemplateID *uuid.UUID           `json:"template_id,omitempty"`
	Name       string               `json:"name"`
	Scope      deliveryconfig.Scope `json:"scope"`
	ScopeID    *uuid.UUID           `json:"scope_id,omitempty"`
	Precedence int32                `json:"precedence"`
	Values     json.RawMessage      `json:"values"`
	Patches    json.RawMessage      `json:"patches,omitempty"`
	Enabled    *bool                `json:"enabled,omitempty"`
}

type overrideSetResponse struct {
	ID         uuid.UUID            `json:"id"`
	ProjectID  uuid.UUID            `json:"project_id"`
	TemplateID *uuid.UUID           `json:"template_id,omitempty"`
	Name       string               `json:"name"`
	Scope      deliveryconfig.Scope `json:"scope"`
	ScopeID    *uuid.UUID           `json:"scope_id,omitempty"`
	Precedence int32                `json:"precedence"`
	Values     json.RawMessage      `json:"values"`
	Patches    json.RawMessage      `json:"patches"`
	Enabled    bool                 `json:"enabled"`
	Generation int64                `json:"generation"`
	CreatedAt  time.Time            `json:"created_at"`
	UpdatedAt  time.Time            `json:"updated_at"`
}

func overrideSetView(row sqlc.DeliveryOverrideSet) overrideSetResponse {
	result := overrideSetResponse{ID: row.ID, ProjectID: row.ProjectID, Name: row.Name, Scope: deliveryconfig.Scope(row.ScopeType), Precedence: row.Precedence, Values: row.ValuesDocument, Patches: row.Patches, Enabled: row.Enabled, Generation: row.Generation, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
	if row.TemplateID.Valid {
		value := uuid.UUID(row.TemplateID.Bytes)
		result.TemplateID = &value
	}
	if row.ScopeID.Valid {
		value := uuid.UUID(row.ScopeID.Bytes)
		result.ScopeID = &value
	}
	return result
}

func (h *OverrideSetHandler) List(w http.ResponseWriter, r *http.Request) {
	projectID, err := projectIDFromRequest(r, uuid.Nil)
	if err != nil {
		respondError(w, 400, "invalid_project_scope", err.Error())
		return
	}
	limit, offset, err := parsePagination(r)
	if err != nil {
		respondError(w, 400, "invalid_pagination", err.Error())
		return
	}
	rows, err := h.queries.ListDeliveryOverrideSets(r.Context(), sqlc.ListDeliveryOverrideSetsParams{ProjectID: projectID, Limit: limit, Offset: offset})
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	total, err := h.queries.CountDeliveryOverrideSets(r.Context(), projectID)
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	items := make([]overrideSetResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, overrideSetView(row))
	}
	respondPage(w, r, items, total, limit, offset, int64(offset)+int64(len(rows)) < total, true)
}

func (h *OverrideSetHandler) Get(w http.ResponseWriter, r *http.Request) {
	projectID, id, ok := overrideSetIDs(w, r)
	if !ok {
		return
	}
	row, err := h.queries.GetDeliveryOverrideSet(r.Context(), sqlc.GetDeliveryOverrideSetParams{ProjectID: projectID, ID: id})
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	setEntityTag(w, row.Generation)
	respondData(w, 200, overrideSetView(row))
}

func (h *OverrideSetHandler) Create(w http.ResponseWriter, r *http.Request) {
	var input overrideSetWrite
	if err := decodeRequest(w, r, &input); err != nil {
		respondError(w, 400, "invalid_request", err.Error())
		return
	}
	projectID, err := projectIDFromRequest(r, input.ProjectID)
	if err != nil {
		respondError(w, 400, "invalid_project_scope", err.Error())
		return
	}
	values, patches, err := h.validate(r.Context(), projectID, &input)
	if err != nil {
		respondError(w, 400, "validation_error", err.Error())
		return
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	row, err := h.queries.CreateDeliveryOverrideSet(r.Context(), sqlc.CreateDeliveryOverrideSetParams{
		ProjectID: projectID, TemplateID: nullableUUID(input.TemplateID), Name: input.Name, ScopeType: string(input.Scope),
		ScopeID: nullableUUID(input.ScopeID), Precedence: input.Precedence, ValuesDocument: values, Patches: patches,
		Enabled: enabled, CreatedBy: middleware.AuthenticatedUserUUID(r.Context()),
	})
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	recordAudit(r, h.queries, "delivery.override_set.created", "delivery_override_set", row.ID.String(), row.Name, map[string]any{"scope": row.ScopeType})
	setEntityTag(w, row.Generation)
	respondData(w, 201, overrideSetView(row))
}

func (h *OverrideSetHandler) Update(w http.ResponseWriter, r *http.Request) {
	projectID, id, ok := overrideSetIDs(w, r)
	if !ok {
		return
	}
	var input overrideSetWrite
	if err := decodeRequest(w, r, &input); err != nil {
		respondError(w, 400, "invalid_request", err.Error())
		return
	}
	values, patches, err := h.validate(r.Context(), projectID, &input)
	if err != nil {
		respondError(w, 400, "validation_error", err.Error())
		return
	}
	generation, err := requireIfMatch(r)
	if err != nil {
		respondError(w, 428, "precondition_required", "a numeric If-Match generation is required")
		return
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	row, err := h.queries.UpdateDeliveryOverrideSet(r.Context(), sqlc.UpdateDeliveryOverrideSetParams{
		ProjectID: projectID, ID: id, TemplateID: nullableUUID(input.TemplateID), Name: input.Name, ScopeType: string(input.Scope),
		ScopeID: nullableUUID(input.ScopeID), Precedence: input.Precedence, ValuesDocument: values, Patches: patches,
		Enabled: enabled, UpdatedBy: middleware.AuthenticatedUserUUID(r.Context()), Generation: generation,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		respondError(w, 409, "stale_generation", "override set changed; reload before saving")
		return
	}
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	recordAudit(r, h.queries, "delivery.override_set.updated", "delivery_override_set", row.ID.String(), row.Name, map[string]any{"generation": row.Generation})
	setEntityTag(w, row.Generation)
	respondData(w, 200, overrideSetView(row))
}

func (h *OverrideSetHandler) Delete(w http.ResponseWriter, r *http.Request) {
	projectID, id, ok := overrideSetIDs(w, r)
	if !ok {
		return
	}
	generation, err := requireIfMatch(r)
	if err != nil {
		respondError(w, 428, "precondition_required", "a numeric If-Match generation is required")
		return
	}
	_, err = h.queries.DeleteDeliveryOverrideSet(r.Context(), sqlc.DeleteDeliveryOverrideSetParams{ProjectID: projectID, ID: id, Generation: generation})
	if errors.Is(err, pgx.ErrNoRows) {
		respondError(w, 409, "stale_generation", "override set changed; reload before deleting")
		return
	}
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	recordAudit(r, h.queries, "delivery.override_set.deleted", "delivery_override_set", id.String(), "", nil)
	w.WriteHeader(204)
}

// openapi:request DeliveryEffectiveConfigurationRequest
type effectiveConfigurationRequest struct {
	ProjectID   uuid.UUID       `json:"project_id,omitempty"`
	BaseValues  json.RawMessage `json:"base_values"`
	OverrideIDs []uuid.UUID     `json:"override_ids"`
}

func (h *OverrideSetHandler) Effective(w http.ResponseWriter, r *http.Request) {
	var input effectiveConfigurationRequest
	if err := decodeRequest(w, r, &input); err != nil {
		respondError(w, 400, "invalid_request", err.Error())
		return
	}
	projectID, err := projectIDFromRequest(r, input.ProjectID)
	if err != nil {
		respondError(w, 400, "invalid_project_scope", err.Error())
		return
	}
	if len(input.OverrideIDs) > 64 {
		respondError(w, 400, "validation_error", "at most 64 override sets may be resolved")
		return
	}
	var baseObject map[string]any
	if len(input.BaseValues) != 0 && json.Unmarshal(input.BaseValues, &baseObject) != nil {
		respondError(w, 400, "validation_error", "base_values must be a JSON object")
		return
	}
	if containsInlineSecret(baseObject) {
		respondError(w, 400, "secret_not_allowed", "base_values contain an inline secret field; use Kubernetes Secret references")
		return
	}
	rows, err := h.queries.ListDeliveryOverrideSetsByIDs(r.Context(), sqlc.ListDeliveryOverrideSetsByIDsParams{ProjectID: projectID, Column2: input.OverrideIDs})
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	if len(rows) != len(input.OverrideIDs) {
		respondError(w, 409, "override_set_unavailable", "an override set is missing, disabled, or outside the project")
		return
	}
	layers := make([]deliveryconfig.Layer, 0, len(rows))
	for _, row := range rows {
		var patches []string
		if json.Unmarshal(row.Patches, &patches) != nil {
			respondError(w, 500, "invalid_persisted_state", "stored override patches are invalid")
			return
		}
		layers = append(layers, deliveryconfig.Layer{ID: row.ID, Name: row.Name, Scope: deliveryconfig.Scope(row.ScopeType), Precedence: int(row.Precedence), Values: row.ValuesDocument, Patches: patches})
	}
	result, err := deliveryconfig.Merge(input.BaseValues, layers)
	if err != nil {
		var conflict *deliveryconfig.ConflictError
		if errors.As(err, &conflict) {
			respondError(w, 409, "override_conflict", err.Error())
			return
		}
		respondError(w, 400, "validation_error", err.Error())
		return
	}
	respondData(w, 200, result)
}

func (h *OverrideSetHandler) validate(ctx context.Context, projectID uuid.UUID, input *overrideSetWrite) (json.RawMessage, json.RawMessage, error) {
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || utf8.RuneCountInString(input.Name) > 128 {
		return nil, nil, errors.New("name must be between 1 and 128 characters")
	}
	if _, ok := map[deliveryconfig.Scope]bool{deliveryconfig.ScopeOrganization: true, deliveryconfig.ScopeProject: true, deliveryconfig.ScopeEnvironment: true, deliveryconfig.ScopeGroup: true, deliveryconfig.ScopeCluster: true, deliveryconfig.ScopeRollout: true}[input.Scope]; !ok {
		return nil, nil, errors.New("scope is unsupported")
	}
	if input.Scope != deliveryconfig.ScopeOrganization && input.Scope != deliveryconfig.ScopeProject && (input.ScopeID == nil || *input.ScopeID == uuid.Nil) {
		return nil, nil, errors.New("scope_id is required for environment, group, cluster, and rollout scopes")
	}
	if (input.Scope == deliveryconfig.ScopeOrganization || input.Scope == deliveryconfig.ScopeProject) && input.ScopeID != nil {
		return nil, nil, errors.New("scope_id must be omitted for organization and project scopes")
	}
	if input.TemplateID != nil {
		if *input.TemplateID == uuid.Nil {
			return nil, nil, errors.New("template_id must be a UUID")
		}
		if _, err := h.queries.GetDeliveryConfigurationTemplate(ctx, sqlc.GetDeliveryConfigurationTemplateParams{ProjectID: projectID, ID: *input.TemplateID}); err != nil {
			return nil, nil, errors.New("template_id does not identify a template in this project")
		}
	}
	probe := configurationTemplateWrite{Name: input.Name, Renderer: "helm", Values: input.Values, Patches: input.Patches}
	if err := validateConfigurationTemplate(&probe); err != nil {
		return nil, nil, err
	}
	return probe.Values, probe.Patches, nil
}

func overrideSetIDs(w http.ResponseWriter, r *http.Request) (uuid.UUID, uuid.UUID, bool) {
	projectID, err := projectIDFromRequest(r, uuid.Nil)
	if err != nil {
		respondError(w, 400, "invalid_project_scope", err.Error())
		return uuid.Nil, uuid.Nil, false
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil || id == uuid.Nil {
		respondError(w, 400, "invalid_id", "override set id must be a UUID")
		return uuid.Nil, uuid.Nil, false
	}
	return projectID, id, true
}

func nullableUUID(value *uuid.UUID) pgtype.UUID {
	if value == nil || *value == uuid.Nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: *value, Valid: true}
}
