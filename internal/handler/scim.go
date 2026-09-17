// Package handler — SCIM 2.0 provisioning (P1 item 11 — "scim").
//
// Smallest working slice of RFC 7643/7644: bearer-token-authenticated
// User CRUD + Group list/get/create/patch/delete, mapped onto the
// existing users + identity_group_mappings tables. Mounted at
// /scim/v2/* OUTSIDE the JWT auth chain — SCIM clients (Okta, Azure AD,
// OneLogin) authenticate with a static bearer token whose SHA-256 hash
// lives in scim_tokens (migration 114).
//
//	POST   /scim/v2/Users        — create (provision) a user
//	GET    /scim/v2/Users        — list users (SCIM ListResponse)
//	GET    /scim/v2/Users/{id}   — get one
//	PUT    /scim/v2/Users/{id}   — replace attrs + active (de/reactivate)
//	PATCH  /scim/v2/Users/{id}   — partial update (replace active/core attrs)
//	DELETE /scim/v2/Users/{id}   — de-provision (delete) a user
//	GET    /scim/v2/Groups       — list groups (from group_mappings)
//	GET    /scim/v2/Groups/{id}  — get one group by name
//	POST   /scim/v2/Groups       — create group mapping
//	PATCH  /scim/v2/Groups/{id}  — rename / membership patch
//	DELETE /scim/v2/Groups/{id}  — delete group mapping + memberships
//	GET    /scim/v2/ServiceProviderConfig — advertise supported features
//	GET    /scim/v2/ResourceTypes — User + Group resource types
//	GET    /scim/v2/Schemas      — core User + Group schema definitions
//
// Group writes (DIR-03): POST/PATCH/DELETE Groups create, update, or
// remove identity_group_mappings rows (group_name → default global role)
// and optional members[] are reflected into user_idp_groups so SSO group
// sync sees IdP-pushed membership.
package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

const (
	scimContentType   = "application/scim+json"
	scimUserSchema    = "urn:ietf:params:scim:schemas:core:2.0:User"
	scimGroupSchema   = "urn:ietf:params:scim:schemas:core:2.0:Group"
	scimListSchema    = "urn:ietf:params:scim:api:messages:2.0:ListResponse"
	scimPatchSchema   = "urn:ietf:params:scim:api:messages:2.0:PatchOp"
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
	// InvalidateAllTokens bumps the per-user JWT cutoff so a SCIM-driven
	// deactivation (active=false) or deprovision (DELETE) terminates the
	// user's live sessions instead of only blocking future logins.
	InvalidateAllTokens(ctx context.Context, arg sqlc.InvalidateAllTokensParams) error
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlc.User, error)
	GetUserByUsername(ctx context.Context, username string) (sqlc.User, error)
	GetUserByEmail(ctx context.Context, email string) (sqlc.User, error)
	ListUsers(ctx context.Context, arg sqlc.ListUsersParams) ([]sqlc.User, error)
	CountUsers(ctx context.Context) (int64, error)
	DeleteUser(ctx context.Context, id uuid.UUID) error
	ListSCIMGroupNames(ctx context.Context, arg sqlc.ListSCIMGroupNamesParams) ([]string, error)
	CountSCIMGroupNames(ctx context.Context) (int64, error)
	SCIMGroupExists(ctx context.Context, groupName string) (bool, error)
	// Group write surface (DIR-03).
	CreateGroupMapping(ctx context.Context, arg sqlc.CreateGroupMappingParams) (sqlc.IdentityGroupMapping, error)
	ListGroupMappings(ctx context.Context, arg sqlc.ListGroupMappingsParams) ([]sqlc.IdentityGroupMapping, error)
	DeleteGroupMapping(ctx context.Context, id uuid.UUID) error
	ListGlobalRoles(ctx context.Context, arg sqlc.ListGlobalRolesParams) ([]sqlc.GlobalRole, error)
	GetUserIDPGroups(ctx context.Context, userID uuid.UUID) (sqlc.UserIdpGroup, error)
	UpsertUserIDPGroups(ctx context.Context, arg sqlc.UpsertUserIDPGroupsParams) (sqlc.UserIdpGroup, error)
}

// SCIMMutationTx is the complete transaction-bound persistence surface for
// SCIM provisioning. Production passes sqlc.New(tx); tests pass an atomic
// in-memory transaction double. Audit evidence is deliberately part of the
// same interface so no mutation can commit without its outbox row.
type SCIMMutationTx interface {
	SCIMQuerier
	audit.OutboxQuerier
	GetUserByIDForUpdate(ctx context.Context, id uuid.UUID) (sqlc.User, error)
}

type scimRunTxFunc func(context.Context, func(SCIMMutationTx) error) error

// SCIMHandler owns the /scim/v2/* surface.
type SCIMHandler struct {
	queries SCIMQuerier
	// jwt is the optional JWT manager used to flush the positive-
	// validation cache when a SCIM deactivate/deprovision revokes a
	// user's sessions. The database cutoff commits in runTx first; this
	// process-local/broadcast invalidation runs only after that commit.
	jwt       *auth.JWTManager
	rbacCache SSORBACInvalidator
	runTx     scimRunTxFunc
}

func (h *SCIMHandler) SetRBACCacheInvalidator(invalidator SSORBACInvalidator) {
	if h != nil {
		h.rbacCache = invalidator
	}
}

// NewSCIMHandler builds a usable handler. queries may be nil for
// degenerate installs (no management DB / pre-migration boot); the
// routes are simply omitted from the router in that case.
func NewSCIMHandler(queries SCIMQuerier) *SCIMHandler {
	return &SCIMHandler{queries: queries}
}

// SetJWTManager wires the JWT manager so SCIM-driven deactivation /
// deprovision can flush the in-process JWT validation cache (mirrors the
// admin force-logout path). Optional; nil-safe.
func (h *SCIMHandler) SetJWTManager(j *auth.JWTManager) {
	if h == nil {
		return
	}
	h.jwt = j
}

// SetRunTx installs the mandatory transaction adapter used by every SCIM
// write. There is intentionally no direct-query fallback.
func (h *SCIMHandler) SetRunTx(runTx scimRunTxFunc) {
	if h != nil {
		h.runTx = runTx
	}
}

func (h *SCIMHandler) TransactionalAuditWired() bool { return h != nil && h.runTx != nil }

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

type scimGroupMember struct {
	Value   string `json:"value"`
	Display string `json:"display,omitempty"`
}

// openapi:request SCIMGroupCreateRequest
type scimGroupCreateRequest struct {
	DisplayName string            `json:"displayName"`
	Members     []scimGroupMember `json:"members"`
	// RoleID is an Astronomer extension selecting the mapped global role. When
	// omitted, the handler chooses the built-in read-only role.
	RoleID string `json:"roleId,omitempty"`
}

// openapi:request SCIMGroupPatchRequest
type scimGroupPatchRequest struct {
	Schemas    []string                  `json:"schemas"`
	Operations []scimGroupPatchOperation `json:"Operations"`
}

type scimGroupPatchOperation struct {
	Op    string          `json:"op"`
	Path  string          `json:"path"`
	Value json.RawMessage `json:"value"`
}

type scimGroup struct {
	Schemas     []string          `json:"schemas"`
	ID          string            `json:"id"`
	DisplayName string            `json:"displayName"`
	Members     []scimGroupMember `json:"members,omitempty"`
	Meta        scimMeta          `json:"meta"`
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
		if h == nil || h.queries == nil {
			h.scimError(w, http.StatusServiceUnavailable, "SCIM token store unavailable")
			return
		}
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
		now := time.Now()
		if row.RevokedAt.Valid || row.ExpiresAt.IsZero() || !now.Before(row.ExpiresAt) {
			h.scimError(w, http.StatusUnauthorized, "expired or revoked bearer token")
			return
		}
		// Best-effort last-used stamp; never block the request on it.
		_ = h.queries.TouchSCIMToken(r.Context(), row.ID)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), scimTokenContextKey{}, row.ID)))
	})
}

type scimTokenContextKey struct{}

func scimTokenID(ctx context.Context) uuid.UUID {
	id, _ := ctx.Value(scimTokenContextKey{}).(uuid.UUID)
	return id
}

func scimMutationDetail(r *http.Request, detail map[string]any) map[string]any {
	if detail == nil {
		detail = map[string]any{}
	}
	if tokenID := scimTokenID(r.Context()); tokenID != uuid.Nil {
		detail["token_id"] = tokenID.String()
	}
	detail["method"] = r.Method
	detail["path"] = r.URL.Path
	return detail
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
