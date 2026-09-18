package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

type ConfigurationTemplateQueries interface {
	CountDeliveryConfigurationTemplates(context.Context, uuid.UUID) (int64, error)
	ListDeliveryConfigurationTemplates(context.Context, sqlc.ListDeliveryConfigurationTemplatesParams) ([]sqlc.DeliveryConfigurationTemplate, error)
	GetDeliveryConfigurationTemplate(context.Context, sqlc.GetDeliveryConfigurationTemplateParams) (sqlc.DeliveryConfigurationTemplate, error)
	CreateDeliveryConfigurationTemplate(context.Context, sqlc.CreateDeliveryConfigurationTemplateParams) (sqlc.DeliveryConfigurationTemplate, error)
	UpdateDeliveryConfigurationTemplate(context.Context, sqlc.UpdateDeliveryConfigurationTemplateParams) (sqlc.DeliveryConfigurationTemplate, error)
	DeleteDeliveryConfigurationTemplate(context.Context, sqlc.DeleteDeliveryConfigurationTemplateParams) (uuid.UUID, error)
}

type ConfigurationTemplateHandler struct{ queries ConfigurationTemplateQueries }

func NewConfigurationTemplateHandler(queries ConfigurationTemplateQueries) *ConfigurationTemplateHandler {
	return &ConfigurationTemplateHandler{queries: queries}
}

// openapi:request DeliveryConfigurationTemplateWrite
type configurationTemplateWrite struct {
	ProjectID   uuid.UUID       `json:"project_id,omitempty"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Renderer    string          `json:"renderer"`
	Values      json.RawMessage `json:"values"`
	Patches     json.RawMessage `json:"patches,omitempty"`
	SecretRefs  json.RawMessage `json:"secret_refs,omitempty"`
	Generation  int64           `json:"generation,omitempty"`
}

type configurationTemplateResponse struct {
	ID          uuid.UUID       `json:"id"`
	ProjectID   uuid.UUID       `json:"project_id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Renderer    string          `json:"renderer"`
	Values      json.RawMessage `json:"values"`
	Patches     json.RawMessage `json:"patches"`
	SecretRefs  json.RawMessage `json:"secret_refs"`
	Generation  int64           `json:"generation"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

func configurationTemplateView(row sqlc.DeliveryConfigurationTemplate) configurationTemplateResponse {
	return configurationTemplateResponse{
		ID: row.ID, ProjectID: row.ProjectID, Name: row.Name,
		Description: row.Description, Renderer: row.Renderer,
		Values: row.ValuesDocument, Patches: row.Patches, SecretRefs: row.SecretRefs,
		Generation: row.Generation, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}

func (h *ConfigurationTemplateHandler) List(w http.ResponseWriter, r *http.Request) {
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
	rows, err := h.queries.ListDeliveryConfigurationTemplates(r.Context(), sqlc.ListDeliveryConfigurationTemplatesParams{ProjectID: projectID, Limit: limit, Offset: offset})
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	count, err := h.queries.CountDeliveryConfigurationTemplates(r.Context(), projectID)
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	items := make([]configurationTemplateResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, configurationTemplateView(row))
	}
	writeJSON(w, http.StatusOK, pageEnvelope{Data: items, Count: count, TotalKnown: true})
}

func (h *ConfigurationTemplateHandler) Get(w http.ResponseWriter, r *http.Request) {
	projectID, id, ok := templateIDs(w, r)
	if !ok {
		return
	}
	row, err := h.queries.GetDeliveryConfigurationTemplate(r.Context(), sqlc.GetDeliveryConfigurationTemplateParams{ProjectID: projectID, ID: id})
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	setEntityTag(w, row.Generation)
	respondData(w, http.StatusOK, configurationTemplateView(row))
}

func (h *ConfigurationTemplateHandler) Create(w http.ResponseWriter, r *http.Request) {
	var input configurationTemplateWrite
	if err := decodeRequest(w, r, &input); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	projectID, err := projectIDFromRequest(r, input.ProjectID)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_project_scope", err.Error())
		return
	}
	if err := validateConfigurationTemplate(&input); err != nil {
		respondError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	row, err := h.queries.CreateDeliveryConfigurationTemplate(r.Context(), sqlc.CreateDeliveryConfigurationTemplateParams{
		ProjectID: projectID, Name: input.Name, Description: input.Description, Renderer: input.Renderer,
		ValuesDocument: input.Values, Patches: input.Patches, SecretRefs: input.SecretRefs,
		CreatedBy: middleware.AuthenticatedUserUUID(r.Context()),
	})
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	recordAudit(r, h.queries, "delivery.configuration_template.created", "delivery_configuration_template", row.ID.String(), row.Name, map[string]any{"renderer": row.Renderer})
	setEntityTag(w, row.Generation)
	respondData(w, http.StatusCreated, configurationTemplateView(row))
}

func (h *ConfigurationTemplateHandler) Update(w http.ResponseWriter, r *http.Request) {
	projectID, id, ok := templateIDs(w, r)
	if !ok {
		return
	}
	var input configurationTemplateWrite
	if err := decodeRequest(w, r, &input); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if input.ProjectID != uuid.Nil && input.ProjectID != projectID {
		respondError(w, http.StatusBadRequest, "invalid_project_scope", "project scopes do not match")
		return
	}
	if err := validateConfigurationTemplate(&input); err != nil {
		respondError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	generation, err := requireIfMatch(r)
	if err != nil {
		respondError(w, http.StatusPreconditionRequired, "precondition_required", "a numeric If-Match generation is required")
		return
	}
	row, err := h.queries.UpdateDeliveryConfigurationTemplate(r.Context(), sqlc.UpdateDeliveryConfigurationTemplateParams{
		ProjectID: projectID, ID: id, Name: input.Name, Description: input.Description, Renderer: input.Renderer,
		ValuesDocument: input.Values, Patches: input.Patches, SecretRefs: input.SecretRefs,
		UpdatedBy: middleware.AuthenticatedUserUUID(r.Context()), Generation: generation,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		respondError(w, http.StatusConflict, "stale_generation", "template changed; reload before saving")
		return
	}
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	recordAudit(r, h.queries, "delivery.configuration_template.updated", "delivery_configuration_template", row.ID.String(), row.Name, map[string]any{"generation": row.Generation})
	setEntityTag(w, row.Generation)
	respondData(w, http.StatusOK, configurationTemplateView(row))
}

func (h *ConfigurationTemplateHandler) Delete(w http.ResponseWriter, r *http.Request) {
	projectID, id, ok := templateIDs(w, r)
	if !ok {
		return
	}
	generation, err := requireIfMatch(r)
	if err != nil {
		respondError(w, http.StatusPreconditionRequired, "precondition_required", "a numeric If-Match generation is required")
		return
	}
	_, err = h.queries.DeleteDeliveryConfigurationTemplate(r.Context(), sqlc.DeleteDeliveryConfigurationTemplateParams{ProjectID: projectID, ID: id, Generation: generation})
	if errors.Is(err, pgx.ErrNoRows) {
		respondError(w, http.StatusConflict, "stale_generation", "template changed; reload before deleting")
		return
	}
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	recordAudit(r, h.queries, "delivery.configuration_template.deleted", "delivery_configuration_template", id.String(), "", nil)
	w.WriteHeader(http.StatusNoContent)
}

func templateIDs(w http.ResponseWriter, r *http.Request) (uuid.UUID, uuid.UUID, bool) {
	projectID, err := projectIDFromRequest(r, uuid.Nil)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_project_scope", err.Error())
		return uuid.Nil, uuid.Nil, false
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil || id == uuid.Nil {
		respondError(w, http.StatusBadRequest, "invalid_id", "template id must be a UUID")
		return uuid.Nil, uuid.Nil, false
	}
	return projectID, id, true
}

var sensitiveTemplateKey = regexp.MustCompile(`(?i)(password|passwd|token|private.?key|credential|api.?key)$`)

func validateConfigurationTemplate(input *configurationTemplateWrite) error {
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	input.Renderer = strings.TrimSpace(input.Renderer)
	if input.Name == "" || utf8.RuneCountInString(input.Name) > 128 {
		return errors.New("name must be between 1 and 128 characters")
	}
	if utf8.RuneCountInString(input.Description) > 4096 {
		return errors.New("description must be at most 4096 characters")
	}
	if input.Renderer != "helm" && input.Renderer != "kustomize" {
		return errors.New("renderer must be helm or kustomize")
	}
	if len(input.Values) == 0 {
		input.Values = json.RawMessage(`{}`)
	}
	if len(input.Patches) == 0 {
		input.Patches = json.RawMessage(`[]`)
	}
	if len(input.SecretRefs) == 0 {
		input.SecretRefs = json.RawMessage(`[]`)
	}
	var values map[string]any
	if len(input.Values) > 256<<10 || json.Unmarshal(input.Values, &values) != nil {
		return errors.New("values must be a JSON object no larger than 256 KiB")
	}
	if containsInlineSecret(values) {
		return errors.New("values contain a write-only secret field; use secret_refs instead")
	}
	var patches []string
	if len(input.Patches) > 256<<10 || json.Unmarshal(input.Patches, &patches) != nil || len(patches) > 64 {
		return errors.New("patches must contain at most 64 strings and 256 KiB")
	}
	var refs []struct {
		Name      string `json:"name"`
		Key       string `json:"key"`
		ValuePath string `json:"value_path"`
	}
	if json.Unmarshal(input.SecretRefs, &refs) != nil || len(refs) > 64 {
		return errors.New("secret_refs must contain at most 64 Secret references")
	}
	if input.Renderer != "helm" && len(refs) != 0 {
		return errors.New("secret_refs are supported only for Helm templates")
	}
	for _, ref := range refs {
		if strings.TrimSpace(ref.Name) == "" || strings.TrimSpace(ref.Key) == "" || strings.TrimSpace(ref.ValuePath) == "" {
			return errors.New("every secret_ref requires name, key, and value_path")
		}
	}
	return nil
}

func containsInlineSecret(value any) bool {
	object, ok := value.(map[string]any)
	if !ok {
		return false
	}
	for key, child := range object {
		if sensitiveTemplateKey.MatchString(key) && child != nil && child != "" {
			return true
		}
		if containsInlineSecret(child) {
			return true
		}
		if list, ok := child.([]any); ok {
			for _, item := range list {
				if containsInlineSecret(item) {
					return true
				}
			}
		}
	}
	return false
}
