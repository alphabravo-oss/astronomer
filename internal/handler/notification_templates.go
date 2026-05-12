// Notification-template admin endpoints — superuser-gated CRUD over
// the notification_templates table (migration 059) on top of the
// built-in template registry in internal/notify.
//
// Surface (mounted at /api/v1/admin/notification-templates/):
//
//   GET    /                 — list registry entries + has_override
//   GET    /{key}/           — get merged subject/body + defaults
//   PUT    /{key}/           — upsert override
//   DELETE /{key}/           — drop override (revert to default)
//   POST   /{key}/preview/   — render against operator-supplied sample
//   GET    /{key}/variables/ — variable spec for the key
//
// The handler ONLY reads/writes the override row; the dispatcher
// (email/webhook) is what consumes the override at delivery time.
// Mutations are audited under admin.notification_template.{updated,
// reset, previewed}.

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/notify"
)

// NotificationTemplateQuerier is the narrow DB surface the handler
// needs. *sqlc.Queries satisfies this; tests pass a narrow fake.
type NotificationTemplateQuerier interface {
	GetNotificationTemplate(ctx context.Context, key string) (sqlc.NotificationTemplate, error)
	ListNotificationTemplates(ctx context.Context) ([]sqlc.NotificationTemplate, error)
	UpsertNotificationTemplate(ctx context.Context, arg sqlc.UpsertNotificationTemplateParams) (sqlc.NotificationTemplate, error)
	DeleteNotificationTemplate(ctx context.Context, key string) error
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlc.User, error)
}

// NotificationTemplateHandler owns /api/v1/admin/notification-templates/*.
type NotificationTemplateHandler struct {
	queries NotificationTemplateQuerier
	audit   AuthAuditWriter
	log     *slog.Logger
}

// NewNotificationTemplateHandler wires the production handler.
func NewNotificationTemplateHandler(queries NotificationTemplateQuerier, log *slog.Logger) *NotificationTemplateHandler {
	if log == nil {
		log = slog.Default()
	}
	return &NotificationTemplateHandler{queries: queries, log: log}
}

// SetAuditWriter attaches the audit-log writer.
func (h *NotificationTemplateHandler) SetAuditWriter(a AuthAuditWriter) { h.audit = a }

func (h *NotificationTemplateHandler) requireSuperuser(r *http.Request) error {
	return requireSuperuserFromContext(r, h.queries)
}

// templateListItem is the response shape for List. has_override
// surfaces whether the operator has saved a row at all (regardless of
// enabled state). enabled exposes the runtime gate.
type templateListItem struct {
	Key         string `json:"key"`
	Channel     string `json:"channel"`
	Description string `json:"description"`
	BodyFormat  string `json:"body_format"`
	HasOverride bool   `json:"has_override"`
	Enabled     bool   `json:"enabled"`
	UpdatedAt   string `json:"updated_at,omitempty"`
}

// List handles GET /api/v1/admin/notification-templates/.
func (h *NotificationTemplateHandler) List(w http.ResponseWriter, r *http.Request) {
	if err := h.requireSuperuser(r); err != nil {
		RespondError(w, http.StatusForbidden, "forbidden", err.Error())
		return
	}
	registry := notify.Registry()
	rows, err := h.queries.ListNotificationTemplates(r.Context())
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "read_error", "Failed to list notification templates")
		return
	}
	overrideByKey := map[string]sqlc.NotificationTemplate{}
	for _, row := range rows {
		overrideByKey[row.TemplateKey] = row
	}
	items := make([]templateListItem, 0, len(registry))
	for _, def := range registry {
		item := templateListItem{
			Key:         def.Key,
			Channel:     def.Channel,
			Description: def.Description,
			BodyFormat:  def.BodyFormat,
			Enabled:     true,
		}
		if row, ok := overrideByKey[def.Key]; ok {
			item.HasOverride = true
			item.Enabled = row.Enabled
			item.UpdatedAt = row.UpdatedAt.UTC().Format(time.RFC3339)
		}
		items = append(items, item)
	}
	RespondJSON(w, http.StatusOK, map[string]any{
		"items": items,
		"total": len(items),
	})
}

// templateDetail is the GET-by-key response.
type templateDetail struct {
	Key             string                `json:"key"`
	Channel         string                `json:"channel"`
	Description     string                `json:"description"`
	BodyFormat      string                `json:"body_format"`
	DefaultSubject  string                `json:"default_subject"`
	DefaultBody     string                `json:"default_body"`
	Subject         string                `json:"subject"`
	Body            string                `json:"body"`
	HasOverride     bool                  `json:"has_override"`
	Enabled         bool                  `json:"enabled"`
	UpdatedAt       string                `json:"updated_at,omitempty"`
	UpdatedBy       string                `json:"updated_by,omitempty"`
	Variables       []notify.VariableSpec `json:"variables"`
}

// Get handles GET /api/v1/admin/notification-templates/{key}/.
func (h *NotificationTemplateHandler) Get(w http.ResponseWriter, r *http.Request) {
	if err := h.requireSuperuser(r); err != nil {
		RespondError(w, http.StatusForbidden, "forbidden", err.Error())
		return
	}
	key := chi.URLParam(r, "key")
	def, ok := notify.Lookup(key)
	if !ok {
		RespondError(w, http.StatusNotFound, "not_found", "Unknown template key")
		return
	}
	resp := templateDetail{
		Key:            def.Key,
		Channel:        def.Channel,
		Description:    def.Description,
		BodyFormat:     def.BodyFormat,
		DefaultSubject: def.Subject,
		DefaultBody:    def.Body,
		Subject:        def.Subject,
		Body:           def.Body,
		Enabled:        true,
		Variables:      def.Variables,
	}
	row, err := h.queries.GetNotificationTemplate(r.Context(), key)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// No override — defaults are the merged view.
	case err != nil:
		RespondError(w, http.StatusInternalServerError, "read_error", "Failed to read template override")
		return
	default:
		resp.HasOverride = true
		resp.Enabled = row.Enabled
		resp.UpdatedAt = row.UpdatedAt.UTC().Format(time.RFC3339)
		if row.UpdatedBy.Valid {
			resp.UpdatedBy = uuidString(row.UpdatedBy)
		}
		if row.SubjectTpl != "" {
			resp.Subject = row.SubjectTpl
		}
		resp.Body = row.BodyTpl
		if row.BodyFormat != "" {
			resp.BodyFormat = row.BodyFormat
		}
	}
	RespondJSON(w, http.StatusOK, resp)
}

// templateUpsert is the PUT body. body_format is optional; when
// omitted the registry default is used so the operator doesn't have
// to remember the per-template default.
type templateUpsert struct {
	Subject    *string `json:"subject"`
	Body       *string `json:"body"`
	BodyFormat *string `json:"body_format"`
	Enabled    *bool   `json:"enabled"`
}

// Update handles PUT /api/v1/admin/notification-templates/{key}/.
func (h *NotificationTemplateHandler) Update(w http.ResponseWriter, r *http.Request) {
	if err := h.requireSuperuser(r); err != nil {
		RespondError(w, http.StatusForbidden, "forbidden", err.Error())
		return
	}
	key := chi.URLParam(r, "key")
	def, ok := notify.Lookup(key)
	if !ok {
		RespondError(w, http.StatusNotFound, "not_found", "Unknown template key")
		return
	}
	var req templateUpsert
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}
	// Body is required on create — there's no point upserting a row
	// with the default body verbatim.
	if req.Body == nil {
		RespondError(w, http.StatusBadRequest, "validation_error", "body is required")
		return
	}
	subject := ""
	if req.Subject != nil {
		subject = *req.Subject
	}
	bodyFormat := def.BodyFormat
	if req.BodyFormat != nil && *req.BodyFormat != "" {
		bodyFormat = *req.BodyFormat
		if !validBodyFormat(bodyFormat) {
			RespondError(w, http.StatusBadRequest, "validation_error", "body_format must be one of text|markdown|html|json")
			return
		}
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	caller := currentUserUUID(r)
	row, err := h.queries.UpsertNotificationTemplate(r.Context(), sqlc.UpsertNotificationTemplateParams{
		TemplateKey: key,
		Channel:     def.Channel,
		SubjectTpl:  subject,
		BodyTpl:     *req.Body,
		BodyFormat:  bodyFormat,
		Enabled:     enabled,
		UpdatedBy:   caller,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "write_error", "Failed to save template override")
		return
	}
	recordAudit(r, h.audit, "admin.notification_template.updated", "notification_template", row.ID.String(), key, map[string]any{
		"channel":     row.Channel,
		"enabled":     row.Enabled,
		"body_format": row.BodyFormat,
		"body_size":   len(row.BodyTpl),
		"subject_set": row.SubjectTpl != "",
	})
	RespondJSON(w, http.StatusOK, templateRowToDetail(def, row))
}

// Delete handles DELETE /api/v1/admin/notification-templates/{key}/.
func (h *NotificationTemplateHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if err := h.requireSuperuser(r); err != nil {
		RespondError(w, http.StatusForbidden, "forbidden", err.Error())
		return
	}
	key := chi.URLParam(r, "key")
	if _, ok := notify.Lookup(key); !ok {
		RespondError(w, http.StatusNotFound, "not_found", "Unknown template key")
		return
	}
	if err := h.queries.DeleteNotificationTemplate(r.Context(), key); err != nil {
		RespondError(w, http.StatusInternalServerError, "write_error", "Failed to delete template override")
		return
	}
	recordAudit(r, h.audit, "admin.notification_template.reset", "notification_template", "", key, nil)
	w.WriteHeader(http.StatusNoContent)
}

// previewRequest is the body for /preview/. The operator can supply a
// candidate subject/body/body_format (so they can render WITHOUT
// saving first) and the variable map.
type previewRequest struct {
	Subject    string         `json:"subject"`
	Body       string         `json:"body"`
	BodyFormat string         `json:"body_format"`
	Variables  map[string]any `json:"variables"`
}

// previewResponse carries the rendered output.
type previewResponse struct {
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

// Preview handles POST /api/v1/admin/notification-templates/{key}/preview/.
//
// The endpoint renders the OPERATOR'S candidate template against the
// operator-supplied sample variable map. Required variables are
// enforced (per the registry spec) before the render step — a 400
// with the list of missing names lets the UI highlight them.
func (h *NotificationTemplateHandler) Preview(w http.ResponseWriter, r *http.Request) {
	if err := h.requireSuperuser(r); err != nil {
		RespondError(w, http.StatusForbidden, "forbidden", err.Error())
		return
	}
	key := chi.URLParam(r, "key")
	def, ok := notify.Lookup(key)
	if !ok {
		RespondError(w, http.StatusNotFound, "not_found", "Unknown template key")
		return
	}
	var req previewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}
	if strings.TrimSpace(req.Body) == "" {
		// Allow body to fall back to the default so the UI's first
		// /preview/ call (before the operator types anything) still
		// produces a useful render.
		req.Body = def.Body
	}
	if req.Subject == "" {
		req.Subject = def.Subject
	}
	if req.BodyFormat == "" {
		req.BodyFormat = def.BodyFormat
	}
	if !validBodyFormat(req.BodyFormat) {
		RespondError(w, http.StatusBadRequest, "validation_error", "body_format must be one of text|markdown|html|json")
		return
	}
	if missing := notify.CheckRequiredVariables(def, req.Variables); len(missing) > 0 {
		RespondJSON(w, http.StatusBadRequest, map[string]any{
			"error":   "missing_required_variables",
			"message": "sample variables are missing one or more required entries",
			"missing": missing,
		})
		return
	}
	rendered, err := notify.Render(notify.Resolved{
		Key:        def.Key,
		Channel:    def.Channel,
		Subject:    req.Subject,
		Body:       req.Body,
		BodyFormat: req.BodyFormat,
	}, req.Variables)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "render_error", err.Error())
		return
	}
	recordAudit(r, h.audit, "admin.notification_template.previewed", "notification_template", "", key, map[string]any{
		"body_size": len(req.Body),
	})
	RespondJSON(w, http.StatusOK, previewResponse{Subject: rendered.Subject, Body: rendered.Body})
}

// Variables handles GET /api/v1/admin/notification-templates/{key}/variables/.
func (h *NotificationTemplateHandler) Variables(w http.ResponseWriter, r *http.Request) {
	if err := h.requireSuperuser(r); err != nil {
		RespondError(w, http.StatusForbidden, "forbidden", err.Error())
		return
	}
	key := chi.URLParam(r, "key")
	def, ok := notify.Lookup(key)
	if !ok {
		RespondError(w, http.StatusNotFound, "not_found", "Unknown template key")
		return
	}
	RespondJSON(w, http.StatusOK, map[string]any{
		"key":       def.Key,
		"variables": def.Variables,
	})
}

func templateRowToDetail(def notify.TemplateDef, row sqlc.NotificationTemplate) templateDetail {
	out := templateDetail{
		Key:            def.Key,
		Channel:        def.Channel,
		Description:    def.Description,
		BodyFormat:     row.BodyFormat,
		DefaultSubject: def.Subject,
		DefaultBody:    def.Body,
		Subject:        row.SubjectTpl,
		Body:           row.BodyTpl,
		HasOverride:    true,
		Enabled:        row.Enabled,
		UpdatedAt:      row.UpdatedAt.UTC().Format(time.RFC3339),
		Variables:      def.Variables,
	}
	if out.Subject == "" {
		out.Subject = def.Subject
	}
	if out.BodyFormat == "" {
		out.BodyFormat = def.BodyFormat
	}
	if row.UpdatedBy.Valid {
		out.UpdatedBy = uuidString(row.UpdatedBy)
	}
	return out
}

func validBodyFormat(f string) bool {
	switch f {
	case notify.BodyFormatText, notify.BodyFormatMarkdown, notify.BodyFormatHTML, notify.BodyFormatJSON:
		return true
	}
	return false
}

func uuidString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	parsed := uuid.UUID(u.Bytes)
	return parsed.String()
}
