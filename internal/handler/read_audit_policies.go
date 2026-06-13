// Migration 063 — read-side audit policy CRUD (superuser-only).
//
// Surface (mounted at /api/v1/admin/read-audit-policies/):
//
//   GET    /            — list all policies (enabled + disabled)
//   POST   /            — create
//   GET    /{id}/       — get
//   PUT    /{id}/       — update (invalidates the in-process cache)
//   DELETE /{id}/       — delete (invalidates the in-process cache)
//
// Writes emit admin.read_audit_policy.{created,updated,deleted} audit
// rows. Every mutation invalidates the PolicyEvaluator cache so changes
// take effect immediately, not after the 30s TTL.

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
)

// ReadAuditPolicyQuerier is the narrow DB surface the handler needs.
// *sqlc.Queries satisfies this; tests pass a narrow fake.
type ReadAuditPolicyQuerier interface {
	ListReadAuditPolicies(ctx context.Context) ([]sqlc.ReadAuditPolicy, error)
	GetReadAuditPolicy(ctx context.Context, id uuid.UUID) (sqlc.ReadAuditPolicy, error)
	CreateReadAuditPolicy(ctx context.Context, arg sqlc.CreateReadAuditPolicyParams) (sqlc.ReadAuditPolicy, error)
	UpdateReadAuditPolicy(ctx context.Context, arg sqlc.UpdateReadAuditPolicyParams) (sqlc.ReadAuditPolicy, error)
	DeleteReadAuditPolicy(ctx context.Context, id uuid.UUID) error
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlc.User, error)
}

// CacheInvalidator is the optional callback fired after every write so
// the PolicyEvaluator's 30s TTL doesn't gate operator changes. Wire it
// at construction time; nil is fine in tests.
type CacheInvalidator interface {
	Invalidate()
}

// ReadAuditPolicyHandler owns /api/v1/admin/read-audit-policies/*.
type ReadAuditPolicyHandler struct {
	queries     ReadAuditPolicyQuerier
	invalidator CacheInvalidator
	audit       AuthAuditWriter
	log         *slog.Logger
}

// NewReadAuditPolicyHandler wires the production handler.
func NewReadAuditPolicyHandler(queries ReadAuditPolicyQuerier, log *slog.Logger) *ReadAuditPolicyHandler {
	if log == nil {
		log = slog.Default()
	}
	return &ReadAuditPolicyHandler{queries: queries, log: log}
}

// SetAuditWriter attaches the audit-log writer for admin.* rows.
func (h *ReadAuditPolicyHandler) SetAuditWriter(a AuthAuditWriter) { h.audit = a }

// SetCacheInvalidator attaches the PolicyEvaluator (or any
// CacheInvalidator) so writes invalidate the in-process cache.
func (h *ReadAuditPolicyHandler) SetCacheInvalidator(c CacheInvalidator) { h.invalidator = c }

func (h *ReadAuditPolicyHandler) requireSuperuser(r *http.Request) error {
	return requireSuperuserFromContext(r, h.queries)
}

// readAuditPolicyResponse is the API response shape.
type readAuditPolicyResponse struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	PathPattern string  `json:"path_pattern"`
	Verbs       string  `json:"verbs"`
	SampleRate  float64 `json:"sample_rate"`
	Enabled     bool    `json:"enabled"`
	CreatedBy   string  `json:"created_by,omitempty"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
}

func policyToResponse(p sqlc.ReadAuditPolicy) readAuditPolicyResponse {
	resp := readAuditPolicyResponse{
		ID:          p.ID.String(),
		Name:        p.Name,
		Description: p.Description,
		PathPattern: p.PathPattern,
		Verbs:       p.Verbs,
		SampleRate:  p.SampleRate,
		Enabled:     p.Enabled,
		CreatedAt:   p.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:   p.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if p.CreatedBy.Valid {
		resp.CreatedBy = uuid.UUID(p.CreatedBy.Bytes).String()
	}
	return resp
}

// List handles GET /api/v1/admin/read-audit-policies/.
func (h *ReadAuditPolicyHandler) List(w http.ResponseWriter, r *http.Request) {
	if err := h.requireSuperuser(r); err != nil {
		RespondRequestError(w, r, http.StatusForbidden, "forbidden", err.Error())
		return
	}
	rows, err := h.queries.ListReadAuditPolicies(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "read_error", "Failed to list policies")
		return
	}
	items := make([]readAuditPolicyResponse, 0, len(rows))
	for _, p := range rows {
		items = append(items, policyToResponse(p))
	}
	RespondJSON(w, http.StatusOK, map[string]any{
		"items": items,
		"total": len(items),
	})
}

// Get handles GET /api/v1/admin/read-audit-policies/{id}/.
func (h *ReadAuditPolicyHandler) Get(w http.ResponseWriter, r *http.Request) {
	if err := h.requireSuperuser(r); err != nil {
		RespondRequestError(w, r, http.StatusForbidden, "forbidden", err.Error())
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_id", "Invalid policy id")
		return
	}
	row, err := h.queries.GetReadAuditPolicy(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		RespondRequestError(w, r, http.StatusNotFound, "not_found", "Policy not found")
		return
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "read_error", "Failed to read policy")
		return
	}
	RespondJSON(w, http.StatusOK, policyToResponse(row))
}

// readAuditPolicyCreate is the POST body.
type readAuditPolicyCreate struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	PathPattern string   `json:"path_pattern"`
	Verbs       string   `json:"verbs"`
	SampleRate  *float64 `json:"sample_rate"`
	Enabled     *bool    `json:"enabled"`
}

// Create handles POST /api/v1/admin/read-audit-policies/.
func (h *ReadAuditPolicyHandler) Create(w http.ResponseWriter, r *http.Request) {
	if err := h.requireSuperuser(r); err != nil {
		RespondRequestError(w, r, http.StatusForbidden, "forbidden", err.Error())
		return
	}
	var req readAuditPolicyCreate
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.PathPattern = strings.TrimSpace(req.PathPattern)
	req.Verbs = strings.TrimSpace(req.Verbs)
	if req.Name == "" {
		RespondRequestError(w, r, http.StatusBadRequest, "validation_error", "name is required")
		return
	}
	if req.PathPattern == "" {
		RespondRequestError(w, r, http.StatusBadRequest, "validation_error", "path_pattern is required")
		return
	}
	if req.Verbs == "" {
		req.Verbs = "GET"
	}
	sample := 1.0
	if req.SampleRate != nil {
		sample = *req.SampleRate
	}
	if sample < 0 || sample > 1 {
		RespondRequestError(w, r, http.StatusBadRequest, "validation_error", "sample_rate must be between 0.0 and 1.0")
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	createdBy := currentUserPGUUID(r)
	row, err := h.queries.CreateReadAuditPolicy(r.Context(), sqlc.CreateReadAuditPolicyParams{
		Name:        req.Name,
		Description: req.Description,
		PathPattern: req.PathPattern,
		Verbs:       req.Verbs,
		SampleRate:  sample,
		Enabled:     enabled,
		CreatedBy:   createdBy,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "write_error", "Failed to create policy")
		return
	}
	if h.invalidator != nil {
		h.invalidator.Invalidate()
	}
	recordAudit(r, h.queries, "admin.read_audit_policy.created", "read_audit_policy", row.ID.String(), row.Name, map[string]any{
		"path_pattern": row.PathPattern,
		"verbs":        row.Verbs,
		"sample_rate":  row.SampleRate,
		"enabled":      row.Enabled,
	})
	RespondJSON(w, http.StatusCreated, policyToResponse(row))
}

// readAuditPolicyUpdate is the PUT body. All fields optional; omitted
// keys are preserved.
type readAuditPolicyUpdate struct {
	Description *string  `json:"description"`
	PathPattern *string  `json:"path_pattern"`
	Verbs       *string  `json:"verbs"`
	SampleRate  *float64 `json:"sample_rate"`
	Enabled     *bool    `json:"enabled"`
}

// Update handles PUT /api/v1/admin/read-audit-policies/{id}/.
func (h *ReadAuditPolicyHandler) Update(w http.ResponseWriter, r *http.Request) {
	if err := h.requireSuperuser(r); err != nil {
		RespondRequestError(w, r, http.StatusForbidden, "forbidden", err.Error())
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_id", "Invalid policy id")
		return
	}
	existing, err := h.queries.GetReadAuditPolicy(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		RespondRequestError(w, r, http.StatusNotFound, "not_found", "Policy not found")
		return
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "read_error", "Failed to read policy")
		return
	}
	var req readAuditPolicyUpdate
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}

	args := sqlc.UpdateReadAuditPolicyParams{
		ID:          id,
		Description: existing.Description,
		PathPattern: existing.PathPattern,
		Verbs:       existing.Verbs,
		SampleRate:  existing.SampleRate,
		Enabled:     existing.Enabled,
	}
	if req.Description != nil {
		args.Description = *req.Description
	}
	if req.PathPattern != nil {
		args.PathPattern = strings.TrimSpace(*req.PathPattern)
		if args.PathPattern == "" {
			RespondRequestError(w, r, http.StatusBadRequest, "validation_error", "path_pattern cannot be empty")
			return
		}
	}
	if req.Verbs != nil {
		args.Verbs = strings.TrimSpace(*req.Verbs)
		if args.Verbs == "" {
			args.Verbs = "GET"
		}
	}
	if req.SampleRate != nil {
		if *req.SampleRate < 0 || *req.SampleRate > 1 {
			RespondRequestError(w, r, http.StatusBadRequest, "validation_error", "sample_rate must be between 0.0 and 1.0")
			return
		}
		args.SampleRate = *req.SampleRate
	}
	if req.Enabled != nil {
		args.Enabled = *req.Enabled
	}

	row, err := h.queries.UpdateReadAuditPolicy(r.Context(), args)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "write_error", "Failed to update policy")
		return
	}
	if h.invalidator != nil {
		h.invalidator.Invalidate()
	}
	recordAudit(r, h.queries, "admin.read_audit_policy.updated", "read_audit_policy", row.ID.String(), row.Name, map[string]any{
		"path_pattern": row.PathPattern,
		"verbs":        row.Verbs,
		"sample_rate":  row.SampleRate,
		"enabled":      row.Enabled,
	})
	RespondJSON(w, http.StatusOK, policyToResponse(row))
}

// Delete handles DELETE /api/v1/admin/read-audit-policies/{id}/.
func (h *ReadAuditPolicyHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if err := h.requireSuperuser(r); err != nil {
		RespondRequestError(w, r, http.StatusForbidden, "forbidden", err.Error())
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_id", "Invalid policy id")
		return
	}
	existing, err := h.queries.GetReadAuditPolicy(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		RespondRequestError(w, r, http.StatusNotFound, "not_found", "Policy not found")
		return
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "read_error", "Failed to read policy")
		return
	}
	if err := h.queries.DeleteReadAuditPolicy(r.Context(), id); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "write_error", "Failed to delete policy")
		return
	}
	if h.invalidator != nil {
		h.invalidator.Invalidate()
	}
	recordAudit(r, h.queries, "admin.read_audit_policy.deleted", "read_audit_policy", existing.ID.String(), existing.Name, map[string]any{
		"path_pattern": existing.PathPattern,
	})
	w.WriteHeader(http.StatusNoContent)
}

// currentUserPGUUID returns the authenticated user's UUID as
// pgtype.UUID, or an empty (Invalid) value when no user is present.
func currentUserPGUUID(r *http.Request) pgtype.UUID {
	id := currentUserUUID(r)
	return id
}
