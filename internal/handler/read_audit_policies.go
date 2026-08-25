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

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
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

type ReadAuditPolicyMutationTx interface {
	ReadAuditPolicyQuerier
	audit.OutboxQuerier
}

type readAuditPolicyRunTxFunc func(context.Context, func(ReadAuditPolicyMutationTx) error) error

func executeReadAuditPolicyMutation(r *http.Request, h *ReadAuditPolicyHandler, mutate func(ReadAuditPolicyQuerier) (sqlc.ReadAuditPolicy, error), describe func(sqlc.ReadAuditPolicy) clusterAuditEvent) (sqlc.ReadAuditPolicy, error) {
	if h.runTx != nil {
		var row sqlc.ReadAuditPolicy
		err := h.runTx(r.Context(), func(q ReadAuditPolicyMutationTx) error {
			var mutationErr error
			row, mutationErr = mutate(q)
			if mutationErr != nil {
				return mutationErr
			}
			event := describe(row)
			return recordAuditOutbox(r, q, event.action, event.resourceType, event.resourceID, event.resourceName, event.status, event.detail)
		})
		return row, err
	}
	row, err := mutate(h.queries)
	if err != nil {
		return sqlc.ReadAuditPolicy{}, err
	}
	event := describe(row)
	writer := any(h.audit)
	if h.audit == nil {
		writer = h.queries
	}
	recordAudit(r, writer, event.action, event.resourceType, event.resourceID, event.resourceName, event.detail)
	return row, nil
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
	runTx       readAuditPolicyRunTxFunc
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

func (h *ReadAuditPolicyHandler) SetRunTx(runTx readAuditPolicyRunTxFunc) {
	if h != nil {
		h.runTx = runTx
	}
}

func (h *ReadAuditPolicyHandler) TransactionalAuditWired() bool { return h != nil && h.runTx != nil }

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
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, err.Error())
		return
	}
	rows, err := h.queries.ListReadAuditPolicies(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ReadError, "Failed to list policies")
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
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, err.Error())
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid policy id")
		return
	}
	row, err := h.queries.GetReadAuditPolicy(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Policy not found")
		return
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ReadError, "Failed to read policy")
		return
	}
	RespondJSON(w, http.StatusOK, policyToResponse(row))
}

// readAuditPolicyCreate is the POST body.
// openapi:request ReadAuditPolicyCreateRequest
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
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, err.Error())
		return
	}
	var req readAuditPolicyCreate
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.PathPattern = strings.TrimSpace(req.PathPattern)
	req.Verbs = strings.TrimSpace(req.Verbs)
	if req.Name == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "name is required")
		return
	}
	if req.PathPattern == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "path_pattern is required")
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
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "sample_rate must be between 0.0 and 1.0")
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	createdBy := currentUserPGUUID(r)
	row, err := executeReadAuditPolicyMutation(r, h,
		func(q ReadAuditPolicyQuerier) (sqlc.ReadAuditPolicy, error) {
			return q.CreateReadAuditPolicy(r.Context(), sqlc.CreateReadAuditPolicyParams{
				Name: req.Name, Description: req.Description, PathPattern: req.PathPattern,
				Verbs: req.Verbs, SampleRate: sample, Enabled: enabled, CreatedBy: createdBy,
			})
		},
		readAuditPolicyEvent("admin.read_audit_policy.created", http.StatusCreated),
	)
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.WriteError, "Failed to create policy")
		return
	}
	if h.invalidator != nil {
		h.invalidator.Invalidate()
	}
	w.Header().Set("Location", "/api/v1/admin/read-audit-policies/"+row.ID.String()+"/")
	RespondJSON(w, http.StatusCreated, policyToResponse(row))
}

// readAuditPolicyUpdate is the PUT body. All fields optional; omitted
// keys are preserved.
// openapi:request ReadAuditPolicyUpdateRequest
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
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, err.Error())
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid policy id")
		return
	}
	var req readAuditPolicyUpdate
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}

	if req.Description != nil {
		trimmed := strings.TrimSpace(*req.Description)
		req.Description = &trimmed
	}
	if req.PathPattern != nil {
		trimmed := strings.TrimSpace(*req.PathPattern)
		if trimmed == "" {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "path_pattern cannot be empty")
			return
		}
		req.PathPattern = &trimmed
	}
	if req.Verbs != nil {
		trimmed := strings.TrimSpace(*req.Verbs)
		if trimmed == "" {
			trimmed = "GET"
		}
		req.Verbs = &trimmed
	}
	if req.SampleRate != nil {
		if *req.SampleRate < 0 || *req.SampleRate > 1 {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "sample_rate must be between 0.0 and 1.0")
			return
		}
	}

	row, err := executeReadAuditPolicyMutation(r, h,
		func(q ReadAuditPolicyQuerier) (sqlc.ReadAuditPolicy, error) {
			existing, getErr := q.GetReadAuditPolicy(r.Context(), id)
			if getErr != nil {
				return sqlc.ReadAuditPolicy{}, getErr
			}
			args := sqlc.UpdateReadAuditPolicyParams{
				ID: id, Description: existing.Description, PathPattern: existing.PathPattern,
				Verbs: existing.Verbs, SampleRate: existing.SampleRate, Enabled: existing.Enabled,
			}
			if req.Description != nil {
				args.Description = *req.Description
			}
			if req.PathPattern != nil {
				args.PathPattern = *req.PathPattern
			}
			if req.Verbs != nil {
				args.Verbs = *req.Verbs
			}
			if req.SampleRate != nil {
				args.SampleRate = *req.SampleRate
			}
			if req.Enabled != nil {
				args.Enabled = *req.Enabled
			}
			return q.UpdateReadAuditPolicy(r.Context(), args)
		},
		readAuditPolicyEvent("admin.read_audit_policy.updated", http.StatusOK),
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Policy not found")
			return
		}
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.WriteError, "Failed to update policy")
		return
	}
	if h.invalidator != nil {
		h.invalidator.Invalidate()
	}
	RespondJSON(w, http.StatusOK, policyToResponse(row))
}

// Delete handles DELETE /api/v1/admin/read-audit-policies/{id}/.
func (h *ReadAuditPolicyHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if err := h.requireSuperuser(r); err != nil {
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, err.Error())
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid policy id")
		return
	}
	_, err = executeReadAuditPolicyMutation(r, h,
		func(q ReadAuditPolicyQuerier) (sqlc.ReadAuditPolicy, error) {
			existing, getErr := q.GetReadAuditPolicy(r.Context(), id)
			if getErr != nil {
				return sqlc.ReadAuditPolicy{}, getErr
			}
			return existing, q.DeleteReadAuditPolicy(r.Context(), id)
		},
		readAuditPolicyEvent("admin.read_audit_policy.deleted", http.StatusNoContent),
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Policy not found")
			return
		}
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.WriteError, "Failed to delete policy")
		return
	}
	if h.invalidator != nil {
		h.invalidator.Invalidate()
	}
	w.WriteHeader(http.StatusNoContent)
}

func readAuditPolicyEvent(action string, status int) func(sqlc.ReadAuditPolicy) clusterAuditEvent {
	return func(row sqlc.ReadAuditPolicy) clusterAuditEvent {
		return clusterAuditEvent{
			action: action, resourceType: "read_audit_policy", resourceID: row.ID.String(), resourceName: row.Name, status: status,
			detail: map[string]any{"path_pattern": row.PathPattern, "verbs": row.Verbs, "sample_rate": row.SampleRate, "enabled": row.Enabled},
		}
	}
}

// currentUserPGUUID returns the authenticated user's UUID as
// pgtype.UUID, or an empty (Invalid) value when no user is present.
func currentUserPGUUID(r *http.Request) pgtype.UUID {
	id := currentUserUUID(r)
	return id
}
