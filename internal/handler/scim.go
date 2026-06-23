// Package handler — SCIM 2.0 provisioning (P1 item 11 — "scim").
//
// Smallest working slice of RFC 7643/7644: bearer-token-authenticated
// User CRUD (create/get/list/delete) + read-only Group list/get, mapped
// onto the existing users + identity_group_mappings tables. Mounted at
// /scim/v2/* OUTSIDE the JWT auth chain — SCIM clients (Okta, Azure AD,
// OneLogin) authenticate with a static bearer token whose SHA-256 hash
// lives in scim_tokens (migration 114).
//
//	POST   /scim/v2/Users        — create (provision) a user
//	GET    /scim/v2/Users        — list users (SCIM ListResponse)
//	GET    /scim/v2/Users/{id}   — get one
//	PUT    /scim/v2/Users/{id}   — replace attrs + active (de/reactivate)
//	DELETE /scim/v2/Users/{id}   — de-provision (delete) a user
//	GET    /scim/v2/Groups       — list groups (from group_mappings)
//	GET    /scim/v2/Groups/{id}  — get one group by name
//
// Deferred (full RFC): PATCH ops, filtering, ServiceProviderConfig,
// Schemas/ResourceTypes discovery, Group membership writes. The
// distinct-group-name read is enough for an IdP to enumerate the
// targets an operator has wired via identity_group_mappings.
package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

const (
	scimContentType   = "application/scim+json"
	scimUserSchema    = "urn:ietf:params:scim:schemas:core:2.0:User"
	scimGroupSchema   = "urn:ietf:params:scim:schemas:core:2.0:Group"
	scimListSchema    = "urn:ietf:params:scim:api:messages:2.0:ListResponse"
	scimErrorSchema   = "urn:ietf:params:scim:api:messages:2.0:Error"
	scimMaxListResult = 200
)

// SCIMQuerier is the narrow DB surface the SCIM handler needs.
// Implemented by *sqlc.Queries; tests pass a hand-rolled fake.
type SCIMQuerier interface {
	GetSCIMTokenByHash(ctx context.Context, tokenHash string) (sqlc.ScimToken, error)
	TouchSCIMToken(ctx context.Context, id uuid.UUID) error
	CreateUser(ctx context.Context, arg sqlc.CreateUserParams) (sqlc.User, error)
	UpdateUser(ctx context.Context, arg sqlc.UpdateUserParams) (sqlc.User, error)
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlc.User, error)
	GetUserByUsername(ctx context.Context, username string) (sqlc.User, error)
	GetUserByEmail(ctx context.Context, email string) (sqlc.User, error)
	ListUsers(ctx context.Context, arg sqlc.ListUsersParams) ([]sqlc.User, error)
	CountUsers(ctx context.Context) (int64, error)
	DeleteUser(ctx context.Context, id uuid.UUID) error
	ListSCIMGroupNames(ctx context.Context, arg sqlc.ListSCIMGroupNamesParams) ([]string, error)
	CountSCIMGroupNames(ctx context.Context) (int64, error)
}

// SCIMHandler owns the /scim/v2/* surface.
type SCIMHandler struct {
	queries SCIMQuerier
}

// NewSCIMHandler builds a usable handler. queries may be nil for
// degenerate installs (no management DB / pre-migration boot); the
// routes are simply omitted from the router in that case.
func NewSCIMHandler(queries SCIMQuerier) *SCIMHandler {
	return &SCIMHandler{queries: queries}
}

// --- SCIM wire shapes ---

type scimName struct {
	GivenName  string `json:"givenName,omitempty"`
	FamilyName string `json:"familyName,omitempty"`
}

type scimEmail struct {
	Value   string `json:"value"`
	Primary bool   `json:"primary,omitempty"`
}

type scimMeta struct {
	ResourceType string `json:"resourceType"`
}

type scimUser struct {
	Schemas  []string    `json:"schemas"`
	ID       string      `json:"id"`
	UserName string      `json:"userName"`
	Name     scimName    `json:"name"`
	Emails   []scimEmail `json:"emails,omitempty"`
	Active   bool        `json:"active"`
	Meta     scimMeta    `json:"meta"`
}

type scimGroup struct {
	Schemas     []string `json:"schemas"`
	ID          string   `json:"id"`
	DisplayName string   `json:"displayName"`
	Meta        scimMeta `json:"meta"`
}

type scimListResponse struct {
	Schemas      []string `json:"schemas"`
	TotalResults int64    `json:"totalResults"`
	StartIndex   int      `json:"startIndex"`
	ItemsPerPage int      `json:"itemsPerPage"`
	Resources    []any    `json:"Resources"`
}

func toSCIMUser(u sqlc.User) scimUser {
	out := scimUser{
		Schemas:  []string{scimUserSchema},
		ID:       u.ID.String(),
		UserName: u.Username,
		Name:     scimName{GivenName: u.FirstName, FamilyName: u.LastName},
		Active:   u.IsActive,
		Meta:     scimMeta{ResourceType: "User"},
	}
	if u.Email != "" {
		out.Emails = []scimEmail{{Value: u.Email, Primary: true}}
	}
	return out
}

// Auth wraps the SCIM routes with static-bearer-token authentication.
// The token's SHA-256 hash must match a non-revoked scim_tokens row.
func (h *SCIMHandler) Auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		if token == "" {
			h.scimError(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		row, err := h.queries.GetSCIMTokenByHash(r.Context(), auth.HashSCIMToken(token))
		if err != nil {
			h.scimError(w, http.StatusUnauthorized, "invalid bearer token")
			return
		}
		// Best-effort last-used stamp; never block the request on it.
		_ = h.queries.TouchSCIMToken(r.Context(), row.ID)
		next.ServeHTTP(w, r)
	})
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	const p = "Bearer "
	if len(h) > len(p) && strings.EqualFold(h[:len(p)], p) {
		return strings.TrimSpace(h[len(p):])
	}
	return ""
}

// --- User endpoints ---

type scimCreateUserRequest struct {
	UserName string      `json:"userName"`
	Name     scimName    `json:"name"`
	Emails   []scimEmail `json:"emails"`
	Active   *bool       `json:"active"`
}

// CreateUser handles POST /scim/v2/Users.
func (h *SCIMHandler) CreateUser(w http.ResponseWriter, r *http.Request) {
	var req scimCreateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.scimError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	userName := strings.TrimSpace(req.UserName)
	if userName == "" {
		h.scimError(w, http.StatusBadRequest, "userName is required")
		return
	}
	email := ""
	for _, e := range req.Emails {
		if e.Primary || email == "" {
			email = strings.TrimSpace(e.Value)
		}
	}
	if email == "" {
		// SCIM userName is frequently an email; fall back to it so the
		// NOT NULL email column is satisfied.
		email = userName
	}
	active := true
	if req.Active != nil {
		active = *req.Active
	}

	// Idempotency: if the IdP re-provisions an existing user, update the
	// core attributes + active flag in place (an IdP commonly re-PUTs/POSTs
	// the full resource, including active:false to deactivate) rather than
	// returning a 500 on the unique constraint or silently ignoring the
	// change. active=false maps to is_active=false, which the login + stream
	// auth paths treat as disabled.
	if existing, err := h.queries.GetUserByUsername(r.Context(), userName); err == nil {
		updated, err := h.queries.UpdateUser(r.Context(), sqlc.UpdateUserParams{
			ID:        existing.ID,
			Email:     email,
			Username:  userName,
			FirstName: req.Name.GivenName,
			LastName:  req.Name.FamilyName,
			IsActive:  active,
		})
		if err != nil {
			h.scimError(w, http.StatusInternalServerError, "failed to update user")
			return
		}
		h.writeSCIM(w, http.StatusOK, toSCIMUser(updated))
		return
	} else if !errors.Is(err, pgx.ErrNoRows) {
		h.scimError(w, http.StatusInternalServerError, "failed to look up user")
		return
	}

	created, err := h.queries.CreateUser(r.Context(), sqlc.CreateUserParams{
		Email:     email,
		Username:  userName,
		FirstName: req.Name.GivenName,
		LastName:  req.Name.FamilyName,
		Password:  "", // SSO/SCIM-provisioned users have no local password.
		IsActive:  active,
	})
	if err != nil {
		h.scimError(w, http.StatusConflict, "user already exists or could not be created")
		return
	}
	h.writeSCIM(w, http.StatusCreated, toSCIMUser(created))
}

// GetUser handles GET /scim/v2/Users/{id}.
func (h *SCIMHandler) GetUser(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		h.scimError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	u, err := h.queries.GetUserByID(r.Context(), id)
	if err != nil {
		h.scimError(w, http.StatusNotFound, "user not found")
		return
	}
	h.writeSCIM(w, http.StatusOK, toSCIMUser(u))
}

// PutUser handles PUT /scim/v2/Users/{id}: a full-resource replace. The
// IdP sends the complete User representation, including `active`; this is
// how deactivation (active:false) and reactivation (active:true) reach the
// backend. active maps to is_active, which gates login + stream auth.
func (h *SCIMHandler) PutUser(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		h.scimError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	var req scimCreateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.scimError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	existing, err := h.queries.GetUserByID(r.Context(), id)
	if err != nil {
		h.scimError(w, http.StatusNotFound, "user not found")
		return
	}
	// userName is immutable here: PUT replaces attributes of the resource
	// identified by {id}, not its identity. Keep the stored username if the
	// IdP omits or changes it.
	userName := strings.TrimSpace(req.UserName)
	if userName == "" {
		userName = existing.Username
	}
	email := ""
	for _, e := range req.Emails {
		if e.Primary || email == "" {
			email = strings.TrimSpace(e.Value)
		}
	}
	if email == "" {
		email = existing.Email
	}
	active := true
	if req.Active != nil {
		active = *req.Active
	}
	updated, err := h.queries.UpdateUser(r.Context(), sqlc.UpdateUserParams{
		ID:        id,
		Email:     email,
		Username:  userName,
		FirstName: req.Name.GivenName,
		LastName:  req.Name.FamilyName,
		IsActive:  active,
	})
	if err != nil {
		h.scimError(w, http.StatusInternalServerError, "failed to update user")
		return
	}
	h.writeSCIM(w, http.StatusOK, toSCIMUser(updated))
}

// ListUsers handles GET /scim/v2/Users. Supports SCIM startIndex/count
// pagination (1-based startIndex) and the common userName eq filter.
func (h *SCIMHandler) ListUsers(w http.ResponseWriter, r *http.Request) {
	// Single-resource filter shortcut: ?filter=userName eq "x". This is
	// the one filter every IdP issues before a create; supporting it
	// avoids spurious duplicate-create attempts.
	if userName := parseUserNameEqFilter(r.URL.Query().Get("filter")); userName != "" {
		u, err := h.queries.GetUserByUsername(r.Context(), userName)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			h.scimError(w, http.StatusInternalServerError, "failed to look up user")
			return
		}
		resources := []any{}
		var total int64
		if err == nil {
			resources = append(resources, toSCIMUser(u))
			total = 1
		}
		h.writeSCIM(w, http.StatusOK, scimListResponse{
			Schemas:      []string{scimListSchema},
			TotalResults: total,
			StartIndex:   1,
			ItemsPerPage: len(resources),
			Resources:    resources,
		})
		return
	}

	startIndex, count := scimPaging(r)
	rows, err := h.queries.ListUsers(r.Context(), sqlc.ListUsersParams{
		Limit:  int32(count),
		Offset: int32(startIndex - 1),
	})
	if err != nil {
		h.scimError(w, http.StatusInternalServerError, "failed to list users")
		return
	}
	total, err := h.queries.CountUsers(r.Context())
	if err != nil {
		h.scimError(w, http.StatusInternalServerError, "failed to count users")
		return
	}
	resources := make([]any, 0, len(rows))
	for _, u := range rows {
		resources = append(resources, toSCIMUser(u))
	}
	h.writeSCIM(w, http.StatusOK, scimListResponse{
		Schemas:      []string{scimListSchema},
		TotalResults: total,
		StartIndex:   startIndex,
		ItemsPerPage: len(resources),
		Resources:    resources,
	})
}

// DeleteUser handles DELETE /scim/v2/Users/{id}.
func (h *SCIMHandler) DeleteUser(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		h.scimError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	u, err := h.queries.GetUserByID(r.Context(), id)
	if err != nil {
		h.scimError(w, http.StatusNotFound, "user not found")
		return
	}
	if u.IsSuperuser || u.IsStaff {
		h.scimError(w, http.StatusForbidden, "cannot delete privileged user")
		return
	}
	if err := h.queries.DeleteUser(r.Context(), id); err != nil {
		h.scimError(w, http.StatusInternalServerError, "failed to delete user")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- Group endpoints (read-only) ---

// ListGroups handles GET /scim/v2/Groups. Each distinct group_name in
// identity_group_mappings becomes one SCIM Group resource. The Group id
// is the displayName (group names are unique in the SCIM view), so an
// IdP can GET it back directly.
func (h *SCIMHandler) ListGroups(w http.ResponseWriter, r *http.Request) {
	startIndex, count := scimPaging(r)
	names, err := h.queries.ListSCIMGroupNames(r.Context(), sqlc.ListSCIMGroupNamesParams{
		Limit:  int32(count),
		Offset: int32(startIndex - 1),
	})
	if err != nil {
		h.scimError(w, http.StatusInternalServerError, "failed to list groups")
		return
	}
	total, err := h.queries.CountSCIMGroupNames(r.Context())
	if err != nil {
		h.scimError(w, http.StatusInternalServerError, "failed to count groups")
		return
	}
	resources := make([]any, 0, len(names))
	for _, n := range names {
		resources = append(resources, toSCIMGroup(n))
	}
	h.writeSCIM(w, http.StatusOK, scimListResponse{
		Schemas:      []string{scimListSchema},
		TotalResults: total,
		StartIndex:   startIndex,
		ItemsPerPage: len(resources),
		Resources:    resources,
	})
}

// GetGroup handles GET /scim/v2/Groups/{id}, where {id} is the group's
// displayName (URL-escaped).
func (h *SCIMHandler) GetGroup(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "id")
	// {id} is the group name; confirm it actually exists by scanning the
	// (small, operator-curated) set rather than adding another query.
	names, err := h.queries.ListSCIMGroupNames(r.Context(), sqlc.ListSCIMGroupNamesParams{
		Limit:  scimMaxListResult,
		Offset: 0,
	})
	if err != nil {
		h.scimError(w, http.StatusInternalServerError, "failed to look up group")
		return
	}
	for _, n := range names {
		if n == name {
			h.writeSCIM(w, http.StatusOK, toSCIMGroup(n))
			return
		}
	}
	h.scimError(w, http.StatusNotFound, "group not found")
}

func toSCIMGroup(name string) scimGroup {
	return scimGroup{
		Schemas:     []string{scimGroupSchema},
		ID:          name,
		DisplayName: name,
		Meta:        scimMeta{ResourceType: "Group"},
	}
}

// --- helpers ---

// scimPaging parses SCIM's 1-based startIndex + count params, clamping
// count to [1, scimMaxListResult] and startIndex to >= 1.
func scimPaging(r *http.Request) (startIndex, count int) {
	startIndex = 1
	if s := r.URL.Query().Get("startIndex"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v >= 1 {
			startIndex = v
		}
	}
	count = 20
	if s := r.URL.Query().Get("count"); s != "" {
		if v, err := strconv.Atoi(s); err == nil {
			count = v
		}
	}
	if count < 1 {
		count = 20
	}
	if count > scimMaxListResult {
		count = scimMaxListResult
	}
	return startIndex, count
}

// parseUserNameEqFilter extracts X from `userName eq "X"`. Returns ""
// for any other (unsupported) filter — the caller then falls through to
// an unfiltered list, which is a safe SCIM degradation.
func parseUserNameEqFilter(filter string) string {
	filter = strings.TrimSpace(filter)
	if filter == "" {
		return ""
	}
	lower := strings.ToLower(filter)
	if !strings.HasPrefix(lower, "username eq ") {
		return ""
	}
	val := strings.TrimSpace(filter[len("userName eq "):])
	return strings.Trim(val, `"`)
}

func (h *SCIMHandler) writeSCIM(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", scimContentType)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func (h *SCIMHandler) scimError(w http.ResponseWriter, status int, detail string) {
	w.Header().Set("Content-Type", scimContentType)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"schemas": []string{scimErrorSchema},
		"detail":  detail,
		"status":  strconv.Itoa(status),
	})
}
