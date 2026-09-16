package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type scimCreateUserRequest struct {
	UserName string      `json:"userName"`
	Name     scimName    `json:"name"`
	Emails   []scimEmail `json:"emails"`
	Active   *bool       `json:"active"`
}

var (
	errSCIMUserNotFound     = errors.New("SCIM user not found")
	errSCIMUserConflict     = errors.New("SCIM user conflict")
	errSCIMGroupNotFound    = errors.New("SCIM group not found")
	errSCIMPrivilegedUser   = errors.New("SCIM privileged user is immutable")
	errSCIMInvalidGroupRole = errors.New("invalid SCIM group role")
	errSCIMInvalidPatch     = errors.New("invalid SCIM patch")
)

type scimUserMutationResult struct {
	user                sqlc.User
	status              int
	sessionsInvalidated bool
}

func (h *SCIMHandler) handleMutationError(w http.ResponseWriter, err error, fallbackStatus int, fallbackDetail string) {
	switch {
	case errors.Is(err, audit.ErrOutboxUnavailable):
		h.scimError(w, http.StatusServiceUnavailable, "mandatory audit storage is unavailable; mutation was not committed")
	case errors.Is(err, errSCIMUserNotFound):
		h.scimError(w, http.StatusNotFound, "user not found")
	case errors.Is(err, errSCIMGroupNotFound):
		h.scimError(w, http.StatusNotFound, "group not found")
	case errors.Is(err, errSCIMPrivilegedUser):
		h.scimError(w, http.StatusForbidden, "cannot modify privileged user")
	case errors.Is(err, errSCIMUserConflict):
		h.scimError(w, http.StatusConflict, "user already exists or could not be created")
	case errors.Is(err, errSCIMInvalidGroupRole), errors.Is(err, errSCIMInvalidPatch):
		h.scimError(w, http.StatusBadRequest, err.Error())
	default:
		h.scimError(w, fallbackStatus, fallbackDetail)
	}
}

func invalidateCommittedSCIMSessions(ctx context.Context, jwt *auth.JWTManager, id uuid.UUID, reason string) {
	auth.SessionRevocationsTotal.WithLabelValues(observability.MetricValues("user", reason)...).Inc()
	if jwt != nil {
		jwt.InvalidateUser(ctx, id)
	}
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

	result, err := executeMutation(r, h.runTx, func(q SCIMMutationTx) (scimUserMutationResult, error) {
		existing, lookupErr := q.GetUserByUsername(r.Context(), userName)
		if lookupErr == nil {
			existing, lookupErr = q.GetUserByIDForUpdate(r.Context(), existing.ID)
			if lookupErr != nil {
				return scimUserMutationResult{}, lookupErr
			}
			if existing.IsSuperuser || existing.IsStaff {
				return scimUserMutationResult{}, errSCIMPrivilegedUser
			}
			updated, updateErr := q.UpdateUser(r.Context(), sqlc.UpdateUserParams{
				ID: existing.ID, Email: email, Username: userName,
				FirstName: req.Name.GivenName, LastName: req.Name.FamilyName, IsActive: active,
			})
			if updateErr != nil {
				return scimUserMutationResult{}, updateErr
			}
			invalidated := existing.IsActive && !active
			if invalidated {
				if invalidateErr := invalidateSCIMTokens(r.Context(), q, existing.ID); invalidateErr != nil {
					return scimUserMutationResult{}, invalidateErr
				}
			}
			return scimUserMutationResult{user: updated, status: http.StatusOK, sessionsInvalidated: invalidated}, nil
		}
		if !errors.Is(lookupErr, pgx.ErrNoRows) {
			return scimUserMutationResult{}, lookupErr
		}
		created, createErr := q.CreateUser(r.Context(), sqlc.CreateUserParams{
			Email: email, Username: userName, FirstName: req.Name.GivenName,
			LastName: req.Name.FamilyName, Password: "", IsActive: active,
		})
		if createErr != nil {
			return scimUserMutationResult{}, fmt.Errorf("%w: %v", errSCIMUserConflict, createErr)
		}
		return scimUserMutationResult{user: created, status: http.StatusCreated}, nil
	}, func(result scimUserMutationResult) mutationAuditEvent {
		return mutationAuditEvent{
			action: "scim.user.create", resourceType: "scim_user",
			resourceID: result.user.ID.String(), resourceName: result.user.Username, status: result.status,
			detail: scimMutationDetail(r, map[string]any{"active": result.user.IsActive}),
		}
	})
	if err != nil {
		h.handleMutationError(w, err, http.StatusInternalServerError, "failed to provision user")
		return
	}
	if result.sessionsInvalidated {
		invalidateCommittedSCIMSessions(r.Context(), h.jwt, result.user.ID, "scim_deactivated")
	}
	h.writeSCIM(w, result.status, toSCIMUser(result.user))
}

func invalidateSCIMTokens(ctx context.Context, q SCIMMutationTx, id uuid.UUID) error {
	return q.InvalidateAllTokens(ctx, sqlc.InvalidateAllTokensParams{
		ID: id, TokensInvalidatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
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
	result, err := executeMutation(r, h.runTx, func(q SCIMMutationTx) (scimUserMutationResult, error) {
		existing, lookupErr := q.GetUserByIDForUpdate(r.Context(), id)
		if errors.Is(lookupErr, pgx.ErrNoRows) {
			return scimUserMutationResult{}, errSCIMUserNotFound
		}
		if lookupErr != nil {
			return scimUserMutationResult{}, lookupErr
		}
		if existing.IsSuperuser || existing.IsStaff {
			return scimUserMutationResult{}, errSCIMPrivilegedUser
		}
		userName := strings.TrimSpace(req.UserName)
		if userName == "" {
			userName = existing.Username
		}
		email := primaryEmail(req.Emails)
		if email == "" {
			email = existing.Email
		}
		active := true
		if req.Active != nil {
			active = *req.Active
		}
		updated, updateErr := q.UpdateUser(r.Context(), sqlc.UpdateUserParams{
			ID: id, Email: email, Username: userName, FirstName: req.Name.GivenName,
			LastName: req.Name.FamilyName, IsActive: active,
		})
		if updateErr != nil {
			return scimUserMutationResult{}, updateErr
		}
		invalidated := existing.IsActive && !active
		if invalidated {
			if invalidateErr := invalidateSCIMTokens(r.Context(), q, id); invalidateErr != nil {
				return scimUserMutationResult{}, invalidateErr
			}
		}
		return scimUserMutationResult{user: updated, status: http.StatusOK, sessionsInvalidated: invalidated}, nil
	}, func(result scimUserMutationResult) mutationAuditEvent {
		return mutationAuditEvent{
			action: "scim.user.replace", resourceType: "scim_user",
			resourceID: result.user.ID.String(), resourceName: result.user.Username, status: result.status,
			detail: scimMutationDetail(r, map[string]any{"active": result.user.IsActive}),
		}
	})
	if err != nil {
		h.handleMutationError(w, err, http.StatusInternalServerError, "failed to update user")
		return
	}
	if result.sessionsInvalidated {
		invalidateCommittedSCIMSessions(r.Context(), h.jwt, id, "scim_deactivated")
	}
	h.writeSCIM(w, http.StatusOK, toSCIMUser(result.user))
}

// scimPatchRequest is the RFC 7644 §3.5.2 PatchOp envelope.
// openapi:request-operation patchScimUsersById
type scimPatchRequest struct {
	Schemas    []string      `json:"schemas"`
	Operations []scimPatchOp `json:"Operations"`
}

// scimPatchOp is a single modification. value is left as raw JSON because
// its shape depends on path: a scalar (active), a string (displayName), an
// array (emails), or — for a path-less op — the partial User object itself.
type scimPatchOp struct {
	Op    string          `json:"op"`
	Path  string          `json:"path"`
	Value json.RawMessage `json:"value"`
}

// PatchUser handles PATCH /scim/v2/Users/{id} (RFC 7644 §3.5.2). It supports
// the operations real IdPs emit: `replace` of `active` (Okta/Azure AD's
// primary deactivation mechanism — mapped to is_active) and `replace` of the
// core attributes (displayName/name, emails, userName), both as targeted
// (path-set) ops and as the path-less merge of a partial User object. `op` is
// matched case-insensitively. add/remove and other ops are accepted but
// ignored — an IdP reads the returned resource as the source of truth.
func (h *SCIMHandler) PatchUser(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		h.scimError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	var req scimPatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.scimError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	// Tolerate a missing/lowercased schemas list (some IdPs omit it), but
	// reject an explicit non-PatchOp schema rather than silently misapplying.
	for _, s := range req.Schemas {
		if !strings.EqualFold(s, scimPatchSchema) {
			h.scimError(w, http.StatusBadRequest, "unsupported patch schema")
			return
		}
	}
	result, err := executeMutation(r, h.runTx, func(q SCIMMutationTx) (scimUserMutationResult, error) {
		existing, lookupErr := q.GetUserByIDForUpdate(r.Context(), id)
		if errors.Is(lookupErr, pgx.ErrNoRows) {
			return scimUserMutationResult{}, errSCIMUserNotFound
		}
		if lookupErr != nil {
			return scimUserMutationResult{}, lookupErr
		}
		if existing.IsSuperuser || existing.IsStaff {
			return scimUserMutationResult{}, errSCIMPrivilegedUser
		}
		params := sqlc.UpdateUserParams{
			ID: id, Email: existing.Email, Username: existing.Username,
			FirstName: existing.FirstName, LastName: existing.LastName, IsActive: existing.IsActive,
		}
		for _, op := range req.Operations {
			if !strings.EqualFold(strings.TrimSpace(op.Op), "replace") {
				continue
			}
			if !applyPatchReplace(&params, strings.TrimSpace(op.Path), op.Value) {
				return scimUserMutationResult{}, fmt.Errorf("%w: invalid patch value", errSCIMInvalidPatch)
			}
		}
		updated, updateErr := q.UpdateUser(r.Context(), params)
		if updateErr != nil {
			return scimUserMutationResult{}, updateErr
		}
		invalidated := existing.IsActive && !params.IsActive
		if invalidated {
			if invalidateErr := invalidateSCIMTokens(r.Context(), q, id); invalidateErr != nil {
				return scimUserMutationResult{}, invalidateErr
			}
		}
		return scimUserMutationResult{user: updated, status: http.StatusOK, sessionsInvalidated: invalidated}, nil
	}, func(result scimUserMutationResult) mutationAuditEvent {
		return mutationAuditEvent{
			action: "scim.user.update", resourceType: "scim_user",
			resourceID: result.user.ID.String(), resourceName: result.user.Username, status: result.status,
			detail: scimMutationDetail(r, map[string]any{"active": result.user.IsActive}),
		}
	})
	if err != nil {
		h.handleMutationError(w, err, http.StatusInternalServerError, "failed to update user")
		return
	}
	if result.sessionsInvalidated {
		invalidateCommittedSCIMSessions(r.Context(), h.jwt, id, "scim_deactivated")
	}
	h.writeSCIM(w, http.StatusOK, toSCIMUser(result.user))
}

// applyPatchReplace mutates params for one `replace` op. A path-less op
// merges a partial User object (the value is the User attrs); a path-set op
// replaces the single named attribute. Unknown paths are ignored (safe SCIM
// degradation). Returns false only when the value JSON cannot be decoded.
func applyPatchReplace(params *sqlc.UpdateUserParams, path string, value json.RawMessage) bool {
	if len(value) == 0 {
		return true
	}
	switch strings.ToLower(path) {
	case "":
		// Path-less replace: value is a partial User object. Decode the
		// attributes we manage and merge any that are present.
		var patch struct {
			UserName    *string     `json:"userName"`
			DisplayName *string     `json:"displayName"`
			Name        *scimName   `json:"name"`
			Emails      []scimEmail `json:"emails"`
			Active      *bool       `json:"active"`
		}
		if err := json.Unmarshal(value, &patch); err != nil {
			return false
		}
		if patch.UserName != nil && strings.TrimSpace(*patch.UserName) != "" {
			params.Username = strings.TrimSpace(*patch.UserName)
		}
		if patch.Name != nil {
			params.FirstName = patch.Name.GivenName
			params.LastName = patch.Name.FamilyName
		} else if patch.DisplayName != nil {
			setNameFromDisplay(params, *patch.DisplayName)
		}
		if e := primaryEmail(patch.Emails); e != "" {
			params.Email = e
		}
		if patch.Active != nil {
			params.IsActive = *patch.Active
		}
		return true
	case "active":
		var active bool
		if err := json.Unmarshal(value, &active); err != nil {
			return false
		}
		params.IsActive = active
	case "displayname":
		var s string
		if err := json.Unmarshal(value, &s); err != nil {
			return false
		}
		setNameFromDisplay(params, s)
	case "username":
		var s string
		if err := json.Unmarshal(value, &s); err != nil {
			return false
		}
		if strings.TrimSpace(s) != "" {
			params.Username = strings.TrimSpace(s)
		}
	case "name.givenname":
		var s string
		if err := json.Unmarshal(value, &s); err != nil {
			return false
		}
		params.FirstName = s
	case "name.familyname":
		var s string
		if err := json.Unmarshal(value, &s); err != nil {
			return false
		}
		params.LastName = s
	case "emails":
		// IdPs send emails either as the array or as a single primary value
		// (path `emails[type eq "work"].value`, which we don't sub-parse —
		// callers should target `emails` with the array form).
		var emails []scimEmail
		if err := json.Unmarshal(value, &emails); err != nil {
			return false
		}
		if e := primaryEmail(emails); e != "" {
			params.Email = e
		}
	}
	return true
}

// setNameFromDisplay splits a SCIM displayName into given/family the same way
// the surrounding code stores names: first token => given, remainder => family.
func setNameFromDisplay(params *sqlc.UpdateUserParams, display string) {
	display = strings.TrimSpace(display)
	if display == "" {
		return
	}
	if first, rest, ok := strings.Cut(display, " "); ok {
		params.FirstName = first
		params.LastName = strings.TrimSpace(rest)
		return
	}
	params.FirstName = display
}

// primaryEmail picks the primary email (or the first one) from a SCIM emails
// array, matching CreateUser/PutUser's selection.
func primaryEmail(emails []scimEmail) string {
	out := ""
	for _, e := range emails {
		if e.Primary || out == "" {
			out = strings.TrimSpace(e.Value)
		}
	}
	return out
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
	_, err = executeMutation(r, h.runTx, func(q SCIMMutationTx) (scimUserMutationResult, error) {
		u, lookupErr := q.GetUserByIDForUpdate(r.Context(), id)
		if errors.Is(lookupErr, pgx.ErrNoRows) {
			return scimUserMutationResult{}, errSCIMUserNotFound
		}
		if lookupErr != nil {
			return scimUserMutationResult{}, lookupErr
		}
		if u.IsSuperuser || u.IsStaff {
			return scimUserMutationResult{}, errSCIMPrivilegedUser
		}
		if invalidateErr := invalidateSCIMTokens(r.Context(), q, id); invalidateErr != nil {
			return scimUserMutationResult{}, invalidateErr
		}
		if deleteErr := q.DeleteUser(r.Context(), id); deleteErr != nil {
			return scimUserMutationResult{}, deleteErr
		}
		return scimUserMutationResult{user: u, status: http.StatusNoContent, sessionsInvalidated: true}, nil
	}, func(result scimUserMutationResult) mutationAuditEvent {
		return mutationAuditEvent{
			action: "scim.user.delete", resourceType: "scim_user",
			resourceID: result.user.ID.String(), resourceName: result.user.Username, status: result.status,
			detail: scimMutationDetail(r, nil),
		}
	})
	if err != nil {
		h.handleMutationError(w, err, http.StatusInternalServerError, "failed to delete user")
		return
	}
	invalidateCommittedSCIMSessions(r.Context(), h.jwt, id, "scim_deprovisioned")
	if h.rbacCache != nil {
		h.rbacCache.Invalidate(id.String())
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- Group endpoints ---
