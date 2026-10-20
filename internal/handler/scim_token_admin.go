// Package handler — SCIM provisioning-token admin surface.
//
// The /scim/v2/* provisioning chain (scim.go) authenticates with a static
// bearer token whose SHA-256 hash and lifecycle live in scim_tokens.
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
// display prefix, lifecycle timestamps — never the secret.
package handler

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/api/apiutil"
	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
)

// SCIMTokenAdminQuerier is the narrow DB surface this handler needs.
// Production wires *sqlc.Queries; tests pass a hand-rolled fake.
type SCIMTokenAdminQuerier interface {
	UserByIDQuerier
	CreateSCIMToken(ctx context.Context, arg sqlc.CreateSCIMTokenParams) (sqlc.ScimToken, error)
	ListSCIMTokenMetadata(ctx context.Context, arg sqlc.ListSCIMTokenMetadataParams) ([]sqlc.ListSCIMTokenMetadataRow, error)
	CountSCIMTokens(ctx context.Context) (int64, error)
	RevokeSCIMToken(ctx context.Context, id uuid.UUID) (int64, error)
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
	ExpiresAt  string  `json:"expires_at"`
	RevokedAt  *string `json:"revoked_at"`
}

func toSCIMTokenMeta(t sqlc.ScimToken) scimTokenMeta {
	out := scimTokenMeta{
		ID:        t.ID.String(),
		Name:      t.Name,
		Prefix:    t.Prefix,
		CreatedAt: t.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		ExpiresAt: t.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
	}
	if t.LastUsedAt.Valid {
		s := t.LastUsedAt.Time.UTC().Format("2006-01-02T15:04:05Z07:00")
		out.LastUsedAt = &s
	}
	if t.RevokedAt.Valid {
		s := t.RevokedAt.Time.UTC().Format("2006-01-02T15:04:05Z07:00")
		out.RevokedAt = &s
	}
	return out
}

func toSCIMTokenListMeta(t sqlc.ListSCIMTokenMetadataRow) scimTokenMeta {
	out := scimTokenMeta{
		ID:        t.ID.String(),
		Name:      t.Name,
		Prefix:    t.Prefix,
		CreatedAt: t.CreatedAt.UTC().Format(time.RFC3339),
		ExpiresAt: t.ExpiresAt.UTC().Format(time.RFC3339),
	}
	if t.LastUsedAt.Valid {
		s := t.LastUsedAt.Time.UTC().Format(time.RFC3339)
		out.LastUsedAt = &s
	}
	if t.RevokedAt.Valid {
		s := t.RevokedAt.Time.UTC().Format(time.RFC3339)
		out.RevokedAt = &s
	}
	return out
}

func (h *SCIMTokenAdminHandler) superuser(w http.ResponseWriter, r *http.Request) bool {
	if h.queries == nil {
		apiutil.WriteError(w, r, http.StatusServiceUnavailable, apiutil.NotConfigured, "SCIM token store not configured")
		return false
	}
	_, ok := RequireSuperuser(w, r, h.queries, SuperuserGateConfig{
		ForbiddenMessage: "Managing SCIM tokens requires superuser privileges",
	})
	return ok
}

// openapi:request-operation postAdminScimTokens
type createSCIMTokenRequest struct {
	Name          string `json:"name"`
	ExpiresInDays int    `json:"expires_in_days"`
}

// Create mints a fresh SCIM provisioning token. The plaintext is returned
// ONCE in the response under "token"; only its hash is persisted.
func (h *SCIMTokenAdminHandler) Create(w http.ResponseWriter, r *http.Request) {
	if !h.superuser(w, r) {
		return
	}

	var req createSCIMTokenRequest
	if err := decodeV1JSONBody(r, &req); err != nil {
		apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.InvalidBody, "Invalid JSON body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.ValidationError, "name is required")
		return
	}
	if req.ExpiresInDays == 0 {
		req.ExpiresInDays = 90
	}
	if req.ExpiresInDays < 1 || req.ExpiresInDays > 365 {
		apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.ValidationError, "expires_in_days must be between 1 and 365")
		return
	}

	token, err := auth.GenerateSCIMToken()
	if err != nil {
		apiutil.WriteError(w, r, http.StatusInternalServerError, apiutil.InternalError, "Failed to generate token")
		return
	}

	row, err := h.queries.CreateSCIMToken(r.Context(), sqlc.CreateSCIMTokenParams{
		Name:      req.Name,
		TokenHash: auth.HashSCIMToken(token),
		Prefix:    auth.SCIMTokenDisplayPrefix(token),
		ExpiresAt: time.Now().UTC().AddDate(0, 0, req.ExpiresInDays),
	})
	if err != nil {
		apiutil.WriteError(w, r, http.StatusInternalServerError, apiutil.DBError, err.Error())
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

// List returns one bounded page of SCIM token metadata. Neither the plaintext
// token nor its hash is selected from the database.
func (h *SCIMTokenAdminHandler) List(w http.ResponseWriter, r *http.Request) {
	if !validateLegacyPage(w, r) {
		return
	}

	if !h.superuser(w, r) {
		return
	}

	limit, offset := queryLimitOffset(r, 20)
	rows, err := h.queries.ListSCIMTokenMetadata(r.Context(), sqlc.ListSCIMTokenMetadataParams{
		QueryLimit: queryLimit32(r, 20), QueryOffset: queryOffset32(r),
	})
	if err != nil {
		apiutil.WriteError(w, r, http.StatusInternalServerError, apiutil.DBError, err.Error())
		return
	}
	out := make([]scimTokenMeta, 0, len(rows))
	for _, t := range rows {
		out = append(out, toSCIMTokenListMeta(t))
	}
	total, err := h.queries.CountSCIMTokens(r.Context())
	if err != nil {
		apiutil.WriteError(w, r, http.StatusInternalServerError, apiutil.DBError, err.Error())
		return
	}
	RespondJSON(w, http.StatusOK, struct {
		Tokens     []scimTokenMeta `json:"tokens"`
		Pagination paging.Metadata `json:"pagination"`
	}{Tokens: out, Pagination: paging.Exact(total, limit, offset, len(out))})
}

// Delete revokes a SCIM token by id.
func (h *SCIMTokenAdminHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if !h.superuser(w, r) {
		return
	}

	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.ValidationError, "Invalid token id")
		return
	}
	changed, err := h.queries.RevokeSCIMToken(r.Context(), id)
	if err != nil {
		apiutil.WriteError(w, r, http.StatusInternalServerError, apiutil.DBError, err.Error())
		return
	}
	if changed == 0 {
		apiutil.WriteError(w, r, http.StatusNotFound, apiutil.NotFound, "SCIM token not found or already revoked")
		return
	}

	RecordAuditFromRequest(r, h.queries, "admin.scim_token.revoked", "scim_token", id.String(), "", nil)

	w.WriteHeader(http.StatusNoContent)
}
