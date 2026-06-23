// Package handler — SCIM provisioning-token admin surface.
//
// The /scim/v2/* provisioning chain (scim.go) authenticates with a static
// bearer token whose SHA-256 hash lives in scim_tokens (migration 114).
// This file is the operator-facing way to MINT, list, and revoke those
// tokens. Without it an operator has to hand-seed a hashed row in the DB.
//
// Endpoints (all under /api/v1/admin, superuser-gated inside the handler
// the same way platform_settings.go / admin_queues.go gate):
//
//	POST   /admin/scim-tokens/        — mint; returns the plaintext ONCE
//	GET    /admin/scim-tokens/        — list metadata (never the token)
//	DELETE /admin/scim-tokens/{id}/   — revoke
//
// The plaintext astro_scim_<random> token is shown exactly once in the
// create response; only its hash is persisted (auth.HashSCIMToken). List
// and the create response otherwise expose only metadata — id, name,
// display prefix, last-used / created timestamps — never the secret.
package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
)

// SCIMTokenAdminQuerier is the narrow DB surface this handler needs.
// Production wires *sqlc.Queries; tests pass a hand-rolled fake.
type SCIMTokenAdminQuerier interface {
	UserByIDQuerier
	CreateSCIMToken(ctx context.Context, arg sqlc.CreateSCIMTokenParams) (sqlc.ScimToken, error)
	ListSCIMTokens(ctx context.Context) ([]sqlc.ScimToken, error)
	DeleteSCIMToken(ctx context.Context, id uuid.UUID) error
}

// SCIMTokenAdminHandler owns the /admin/scim-tokens/* surface.
type SCIMTokenAdminHandler struct {
	queries SCIMTokenAdminQuerier
}

// NewSCIMTokenAdminHandler builds a usable handler. queries may be nil for
// degenerate installs (no management DB / pre-migration boot); the routes
// are omitted from the router in that case.
func NewSCIMTokenAdminHandler(queries SCIMTokenAdminQuerier) *SCIMTokenAdminHandler {
	return &SCIMTokenAdminHandler{queries: queries}
}

// scimTokenMeta is the metadata-only view returned by list and (alongside
// the one-time plaintext) by create. It deliberately omits token_hash.
type scimTokenMeta struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Prefix     string  `json:"prefix"`
	LastUsedAt *string `json:"last_used_at"`
	CreatedAt  string  `json:"created_at"`
}

func toSCIMTokenMeta(t sqlc.ScimToken) scimTokenMeta {
	out := scimTokenMeta{
		ID:        t.ID.String(),
		Name:      t.Name,
		Prefix:    t.Prefix,
		CreatedAt: t.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
	}
	if t.LastUsedAt.Valid {
		s := t.LastUsedAt.Time.UTC().Format("2006-01-02T15:04:05Z07:00")
		out.LastUsedAt = &s
	}
	return out
}

func (h *SCIMTokenAdminHandler) superuser(w http.ResponseWriter, r *http.Request) bool {
	if h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.NotConfigured, "SCIM token store not configured")
		return false
	}
	_, ok := RequireSuperuser(w, r, h.queries, SuperuserGateConfig{
		ForbiddenMessage: "Managing SCIM tokens requires superuser privileges",
	})
	return ok
}

type createSCIMTokenRequest struct {
	Name string `json:"name"`
}

// Create mints a fresh SCIM provisioning token. The plaintext is returned
// ONCE in the response under "token"; only its hash is persisted.
func (h *SCIMTokenAdminHandler) Create(w http.ResponseWriter, r *http.Request) {
	if !h.superuser(w, r) {
		return
	}

	var req createSCIMTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "name is required")
		return
	}

	token, err := auth.GenerateSCIMToken()
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Failed to generate token")
		return
	}

	row, err := h.queries.CreateSCIMToken(r.Context(), sqlc.CreateSCIMTokenParams{
		Name:      req.Name,
		TokenHash: auth.HashSCIMToken(token),
		Prefix:    auth.SCIMTokenDisplayPrefix(token),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return
	}

	RecordAuditFromRequest(r, h.queries, "admin.scim_token.created", "scim_token", row.ID.String(), row.Name, nil)

	// The ONLY time the plaintext is ever returned.
	RespondJSON(w, http.StatusCreated, struct {
		scimTokenMeta
		Token string `json:"token"`
	}{
		scimTokenMeta: toSCIMTokenMeta(row),
		Token:         token,
	})
}

// List returns metadata for every SCIM token. The secret is never
// included — only id, name, display prefix, and timestamps.
func (h *SCIMTokenAdminHandler) List(w http.ResponseWriter, r *http.Request) {
	if !h.superuser(w, r) {
		return
	}

	rows, err := h.queries.ListSCIMTokens(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return
	}
	out := make([]scimTokenMeta, 0, len(rows))
	for _, t := range rows {
		out = append(out, toSCIMTokenMeta(t))
	}
	RespondJSON(w, http.StatusOK, map[string]any{"tokens": out})
}

// Delete revokes a SCIM token by id.
func (h *SCIMTokenAdminHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if !h.superuser(w, r) {
		return
	}

	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Invalid token id")
		return
	}
	if err := h.queries.DeleteSCIMToken(r.Context(), id); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, err.Error())
		return
	}

	RecordAuditFromRequest(r, h.queries, "admin.scim_token.revoked", "scim_token", id.String(), "", nil)

	w.WriteHeader(http.StatusNoContent)
}
