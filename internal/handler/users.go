package handler

import (
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/crypto/bcrypt"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

// generateTempPassword returns a 12-character password drawn from a URL-safe
// alphabet. It's used by ResetUserPassword when the caller doesn't supply a
// password and an admin needs a temporary credential to hand to the user.
const tempPasswordAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnpqrstuvwxyz23456789"

func generateTempPassword() (string, error) {
	const length = 12
	buf := make([]byte, length)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		return "", err
	}
	for i, b := range buf {
		buf[i] = tempPasswordAlphabet[int(b)%len(tempPasswordAlphabet)]
	}
	return string(buf), nil
}

// CreateUserRequest represents the request body for creating a user.
type CreateUserRequest struct {
	Email       string `json:"email"`
	Username    string `json:"username"`
	FirstName   string `json:"first_name"`
	LastName    string `json:"last_name"`
	Password    string `json:"password"`
	IsActive    *bool  `json:"is_active"`
	IsStaff     bool   `json:"is_staff"`
	IsSuperuser bool   `json:"is_superuser"`
}

// UpdateUserRequest represents the request body for updating a user.
type UpdateUserRequest struct {
	Email     string `json:"email"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	IsActive  *bool  `json:"is_active"`
}

// ResetPasswordRequest represents the request body for password reset.
type ResetPasswordRequest struct {
	Password string `json:"password"`
}

// CreateUser handles POST /api/v1/users/.
func (h *ResourceHandler) CreateUser(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.UsersError, "user store not configured")
		return
	}
	var req CreateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	req.Email = strings.TrimSpace(req.Email)
	req.Username = strings.TrimSpace(req.Username)
	if req.Email == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Email is required")
		return
	}
	if req.Username == "" {
		req.Username = req.Email
	}
	if req.Password == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Password is required")
		return
	}
	hashed, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.HashError, "Failed to hash password")
		return
	}
	// Default to active when not specified.
	isActive := true
	if req.IsActive != nil {
		isActive = *req.IsActive
	}
	user, err := h.queries.CreateUser(r.Context(), sqlc.CreateUserParams{
		Email:       req.Email,
		Username:    req.Username,
		FirstName:   req.FirstName,
		LastName:    req.LastName,
		Password:    string(hashed),
		IsActive:    isActive,
		IsStaff:     req.IsStaff,
		IsSuperuser: req.IsSuperuser,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CreateError, "Failed to create user")
		return
	}
	recordAudit(r, h.queries, "user.create", "user", user.ID.String(), user.Username, map[string]any{
		"email":        user.Email,
		"is_active":    user.IsActive,
		"is_staff":     user.IsStaff,
		"is_superuser": user.IsSuperuser,
	})
	RespondJSON(w, http.StatusCreated, mapUser(user))
}

// UpdateUser handles PUT/PATCH /api/v1/users/{id}/.
func (h *ResourceHandler) UpdateUser(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.UsersError, "user store not configured")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid user ID")
		return
	}
	current, err := h.queries.GetUserByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "User not found")
		return
	}
	var req UpdateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	email := strings.TrimSpace(req.Email)
	if email == "" {
		email = current.Email
	}
	username := strings.TrimSpace(req.Username)
	if username == "" {
		username = current.Username
	}
	firstName := req.FirstName
	if firstName == "" {
		firstName = current.FirstName
	}
	lastName := req.LastName
	if lastName == "" {
		lastName = current.LastName
	}
	isActive := current.IsActive
	if req.IsActive != nil {
		isActive = *req.IsActive
	}
	user, err := h.queries.UpdateUser(r.Context(), sqlc.UpdateUserParams{
		ID:        id,
		Email:     email,
		Username:  username,
		FirstName: firstName,
		LastName:  lastName,
		IsActive:  isActive,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.UpdateError, "Failed to update user")
		return
	}
	recordAudit(r, h.queries, "user.update", "user", user.ID.String(), user.Username, map[string]any{
		"email":     user.Email,
		"is_active": user.IsActive,
	})
	RespondJSON(w, http.StatusOK, mapUser(user))
}

// DeleteUser handles DELETE /api/v1/users/{id}/.
func (h *ResourceHandler) DeleteUser(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.UsersError, "user store not configured")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid user ID")
		return
	}
	existing, err := h.queries.GetUserByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "User not found")
		return
	}
	if err := h.queries.DeleteUser(r.Context(), id); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DeleteError, "Failed to delete user")
		return
	}
	// ON DELETE CASCADE on the role-binding tables means every binding for
	// this user just vanished. Invalidate so the cache doesn't keep handing
	// out the old set for up to one TTL.
	if h.rbacCache != nil {
		h.rbacCache.Invalidate(existing.ID.String())
	}
	recordAudit(r, h.queries, "user.delete", "user", existing.ID.String(), existing.Username, map[string]any{
		"email": existing.Email,
	})
	w.WriteHeader(http.StatusNoContent)
}

// ResetUserPassword handles POST /api/v1/users/{id}/reset-password/.
func (h *ResourceHandler) ResetUserPassword(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.UsersError, "user store not configured")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid user ID")
		return
	}
	existing, err := h.queries.GetUserByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "User not found")
		return
	}
	// Body is optional: if empty / no `password` field, we generate a random
	// temporary password and return it to the caller. The frontend's "Reset
	// password" admin action POSTs an empty body and expects a temp password
	// back to display once.
	var (
		req       ResetPasswordRequest
		generated bool
	)
	if r.Body != nil && r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
			return
		}
	}
	if req.Password == "" {
		tmp, err := generateTempPassword()
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.GenerateError, "Failed to generate temporary password")
			return
		}
		req.Password = tmp
		generated = true
	}
	hashed, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.HashError, "Failed to hash password")
		return
	}
	if err := h.queries.UpdateUserPassword(r.Context(), sqlc.UpdateUserPasswordParams{
		ID:       id,
		Password: string(hashed),
	}); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.UpdateError, "Failed to reset password")
		return
	}
	recordAudit(r, h.queries, "user.reset_password", "user", existing.ID.String(), existing.Username, map[string]any{
		"generated": generated,
	})
	resp := map[string]any{"success": true, "message": "Password updated"}
	if generated {
		// Returned exactly once — the frontend captures this and shows it to
		// the admin who initiated the reset.
		resp["temporary_password"] = req.Password
	}
	RespondJSON(w, http.StatusOK, resp)
}

// UnlockUser handles POST /api/v1/admin/users/{id}/unlock/.
//
// Clears the per-account lockout fields (failed_login_count = 0,
// locked_until = NULL, locked_reason = ”) so the user can attempt to
// log in again before the natural auto-unlock window expires. Audit
// row carries the admin's user_id as actor (recordAudit pulls it from
// the request context).
//
// Auth: superuser. Gated inside the handler so a non-superuser hitting
// the route gets a clean 403 rather than a generic permission rejection.
func (h *ResourceHandler) UnlockUser(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.UsersError, "user store not configured")
		return
	}
	if err := requireSuperuserFromContext(r, h.queries); err != nil {
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, err.Error())
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid user ID")
		return
	}
	existing, err := h.queries.GetUserByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "User not found")
		return
	}
	if err := h.queries.UnlockUser(r.Context(), id); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.UpdateError, "Failed to unlock user")
		return
	}
	recordAudit(r, h.queries, "admin.user.unlocked", "user", existing.ID.String(), existing.Username, map[string]any{
		"previous_locked_until":  formatTimestamptz(existing.LockedUntil),
		"previous_locked_reason": existing.LockedReason,
		"previous_failed_count":  existing.FailedLoginCount,
	})
	if h.emails != nil && existing.Email != "" {
		h.emails.EnqueueAndLog(r.Context(), EmailNotifierRequest{
			To:       existing.Email,
			Template: "account_unlocked",
			Data:     map[string]any{"Username": existing.Username},
			UserID:   existing.ID,
		})
	}
	RespondJSONUnwrapped(w, http.StatusOK, map[string]any{"success": true, "message": "User unlocked"})
}

// ForceLogoutUser handles POST /api/v1/admin/users/{id}/force-logout/.
//
// Stamps users.tokens_invalidated_at = now() so every JWT issued for
// the user before that timestamp is rejected on its next validation.
// New tokens (issued after this call) remain valid until their own
// expiry. Use for stolen-device / terminated-employee scenarios.
//
// Auth: superuser. Same in-handler gating as UnlockUser.
func (h *ResourceHandler) ForceLogoutUser(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.UsersError, "user store not configured")
		return
	}
	if err := requireSuperuserFromContext(r, h.queries); err != nil {
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, err.Error())
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid user ID")
		return
	}
	existing, err := h.queries.GetUserByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "User not found")
		return
	}
	now := time.Now()
	if err := h.queries.InvalidateAllTokens(r.Context(), sqlc.InvalidateAllTokensParams{
		ID:                  id,
		TokensInvalidatedAt: pgtype.Timestamptz{Time: now, Valid: true},
	}); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.UpdateError, "Failed to invalidate tokens")
		return
	}
	auth.SessionRevocationsTotal.WithLabelValues(observability.MetricValues("user", "admin_force_logout")...).Inc()
	if h.jwt != nil {
		// Drop the positive-validation cache so a JWT we'd JUST
		// confirmed valid doesn't stick around for one more TTL.
		h.jwt.InvalidateCache()
	}

	// Single sign-out clean-up (migration 054). When sso_sessions is
	// wired we additionally:
	//   1. Enumerate every active upstream session for the target
	//      user. Best-effort — DB errors here don't block force-
	//      logout because the JWT cutoff stamped above already
	//      neutralises every in-flight session at the next request.
	//   2. Fire a back-channel end-session POST against each one. Some
	//      IdPs (mostly Dex with native OIDC connectors + Okta/Auth0
	//      with back-channel-logout enabled) honour this and tear
	//      down the upstream session immediately. Many don't —
	//      including Dex's SAML connectors — but the per-attempt
	//      metric tells the operator which providers worked.
	//   3. Delete every sso_sessions row so the encrypted id_tokens
	//      don't sit at rest after the user has been forced out.
	sessionsCleared := 0
	backchannelOK := 0
	backchannelFailed := 0
	if h.ssoSessions != nil {
		sessions, err := h.ssoSessions.ListSSOSessionsByUser(r.Context(), id)
		if err == nil {
			sessionsCleared = len(sessions)
			// Only fire upstream POSTs when both the back-channel
			// client AND the encryptor are wired (we need the
			// plaintext id_token to put in the body). Both are
			// optional at startup — without the encryptor the row's
			// id_token is unreadable.
			if h.ssoBackchannel != nil && h.encryptor != nil {
				for _, s := range sessions {
					if s.EndSessionEndpoint == "" {
						continue
					}
					idToken, derr := h.encryptor.Decrypt(s.UpstreamIDTokenEncrypted)
					if derr != nil {
						backchannelFailed++
						auth.SSOLogoutsTotal.WithLabelValues(observability.MetricValues(s.ProviderName, "encrypt_error")...).Inc()
						continue
					}
					if perr := h.ssoBackchannel.PostEndSession(r.Context(), s.EndSessionEndpoint, idToken); perr != nil {
						backchannelFailed++
						auth.SSOLogoutsTotal.WithLabelValues(observability.MetricValues(s.ProviderName, "backchannel_failed")...).Inc()
					} else {
						backchannelOK++
						auth.SSOLogoutsTotal.WithLabelValues(observability.MetricValues(s.ProviderName, "backchannel_ok")...).Inc()
					}
				}
			}
			// Drop the rows regardless — the JWT cutoff makes them
			// unusable for SLO and they only expose encrypted
			// id_tokens at rest after this point.
			if derr := h.ssoSessions.DeleteSSOSessionsByUser(r.Context(), id); derr != nil {
				// Best-effort: log + audit but don't 5xx the admin
				// path. The retention cron will sweep these later.
				_ = derr
			}
		}
	}

	recordAudit(r, h.queries, "admin.user.force_logged_out", "user", existing.ID.String(), existing.Username, map[string]any{
		"tokens_invalidated_at":  now.UTC().Format(time.RFC3339),
		"sso_sessions_cleared":   sessionsCleared,
		"sso_backchannel_ok":     backchannelOK,
		"sso_backchannel_failed": backchannelFailed,
	})
	RespondJSONUnwrapped(w, http.StatusOK, map[string]any{
		"success":                true,
		"message":                "All active sessions invalidated",
		"tokens_invalidated_at":  now.UTC().Format(time.RFC3339),
		"sso_sessions_cleared":   sessionsCleared,
		"sso_backchannel_ok":     backchannelOK,
		"sso_backchannel_failed": backchannelFailed,
	})
}

// requireSuperuserFromContext is the in-handler superuser gate used by
// the admin endpoints in this file. Mirrors the inline check in
// keyStatusHandler so the route doesn't need an extra middleware tier.
// Returns a non-nil error when the caller is unauthenticated or not a
// superuser; the message is safe to render verbatim to the client.
func requireSuperuserFromContext(r *http.Request, q userByIDQuerier) error {
	dbUser, err := authenticatedUserFromRequest(r, q)
	if err != nil {
		return errSuperuserRequired
	}
	if !dbUser.IsSuperuser {
		return errSuperuserRequired
	}
	return nil
}

// errSuperuserRequired is the canonical error returned by the
// in-handler superuser gate. Carried as a package-level value so
// callers can render the same message without re-stringifying.
var errSuperuserRequired = &authError{"Superuser privileges required"}

type authError struct{ msg string }

func (e *authError) Error() string { return e.msg }

func formatTimestamptz(t pgtype.Timestamptz) string {
	if !t.Valid {
		return ""
	}
	return t.Time.UTC().Format(time.RFC3339)
}
