package handler

import (
	"context"
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

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

// UserMutationTx is the transaction-bound identity administration surface.
// Password/session/privilege-adjacent user state and its audit intent must be
// one PostgreSQL commit decision.
type UserMutationTx interface {
	audit.OutboxQuerier
	GetUserByIDForUpdate(context.Context, uuid.UUID) (sqlc.User, error)
	ListSSOSessionsByUser(context.Context, uuid.UUID) ([]sqlc.SsoSession, error)
	CreateUser(context.Context, sqlc.CreateUserParams) (sqlc.User, error)
	UpdateUser(context.Context, sqlc.UpdateUserParams) (sqlc.User, error)
	DeleteUser(context.Context, uuid.UUID) error
	UpdateUserPassword(context.Context, sqlc.UpdateUserPasswordParams) error
	UnlockUser(context.Context, uuid.UUID) error
	InvalidateAllTokens(context.Context, sqlc.InvalidateAllTokensParams) error
	DeleteSSOSessionsByUser(context.Context, uuid.UUID) error
}

type userRunTxFunc func(context.Context, func(UserMutationTx) error) error

func (h *ResourceHandler) SetUserRunTx(runTx userRunTxFunc) {
	if h != nil {
		h.userRunTx = runTx
	}
}

func (h *ResourceHandler) TransactionalUserAuditWired() bool {
	return h != nil && h.userRunTx != nil
}

var _ UserMutationTx = (*sqlc.Queries)(nil)

func (h *ResourceHandler) requireUserMutationRunner(w http.ResponseWriter, r *http.Request) bool {
	if h == nil || h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.UsersError, "user store not configured")
		return false
	}
	if h.userRunTx == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.RunnerUnwired, "user administration transaction runner is not configured")
		return false
	}
	return true
}

func (h *ResourceHandler) mutateUser(
	w http.ResponseWriter,
	r *http.Request,
	status int,
	code string,
	message string,
	fn func(UserMutationTx) error,
) bool {
	if err := h.userRunTx(r.Context(), fn); err != nil {
		respondTransactionalMutationError(w, r, err, status, code, message)
		return false
	}
	return true
}

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
// openapi:request-operation postUsers
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
// openapi:request UsersSettingsUpdateUserRequest
type UpdateUserRequest struct {
	Email     string `json:"email"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	IsActive  *bool  `json:"is_active"`
}

// ResetPasswordRequest represents the request body for password reset.
// openapi:request-operation postUsersByIdResetPassword
type ResetPasswordRequest struct {
	Password string `json:"password"`
}

// CreateUser handles POST /api/v1/users/.
func (h *ResourceHandler) CreateUser(w http.ResponseWriter, r *http.Request) {
	if !h.requireUserMutationRunner(w, r) {
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
	// DIR-04 / AUTH-R01: enforce live platform password policy when settings
	// are wired; fall back to DefaultPasswordPolicy.
	if err := auth.ValidatePassword(req.Password, h.passwordPolicy(r.Context())); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, err.Error())
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
	params := sqlc.CreateUserParams{
		Email:       req.Email,
		Username:    req.Username,
		FirstName:   req.FirstName,
		LastName:    req.LastName,
		Password:    string(hashed),
		IsActive:    isActive,
		IsStaff:     req.IsStaff,
		IsSuperuser: req.IsSuperuser,
	}
	var user sqlc.User
	if !h.mutateUser(w, r, http.StatusInternalServerError, apierror.CreateError, "Failed to create user", func(q UserMutationTx) error {
		var createErr error
		user, createErr = q.CreateUser(r.Context(), params)
		if createErr != nil {
			return createErr
		}
		return recordAuditOutbox(r, q, "user.create", "user", user.ID.String(), user.Username, http.StatusCreated, map[string]any{
			"email": user.Email, "is_active": user.IsActive, "is_staff": user.IsStaff, "is_superuser": user.IsSuperuser,
		})
	}) {
		return
	}
	w.Header().Set("Location", "/api/v1/users/"+user.ID.String()+"/")
	RespondJSON(w, http.StatusCreated, mapUser(user))
}

// UpdateUser handles PUT/PATCH /api/v1/users/{id}/.
func (h *ResourceHandler) UpdateUser(w http.ResponseWriter, r *http.Request) {
	if !h.requireUserMutationRunner(w, r) {
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
	params := sqlc.UpdateUserParams{
		ID:        id,
		Email:     email,
		Username:  username,
		FirstName: firstName,
		LastName:  lastName,
		IsActive:  isActive,
	}
	var user sqlc.User
	var revokedSessions []sqlc.SsoSession
	sessionsRevoked := false
	revokedAt := time.Now()
	if !h.mutateUser(w, r, http.StatusInternalServerError, apierror.UpdateError, "Failed to update user", func(q UserMutationTx) error {
		locked, lockErr := q.GetUserByIDForUpdate(r.Context(), id)
		if lockErr != nil {
			return lockErr
		}
		// Re-resolve omitted PATCH-like fields from the locked row so a
		// concurrent administrator cannot restore stale values.
		txParams := params
		if strings.TrimSpace(req.Email) == "" {
			txParams.Email = locked.Email
		}
		if strings.TrimSpace(req.Username) == "" {
			txParams.Username = locked.Username
		}
		if req.FirstName == "" {
			txParams.FirstName = locked.FirstName
		}
		if req.LastName == "" {
			txParams.LastName = locked.LastName
		}
		if req.IsActive == nil {
			txParams.IsActive = locked.IsActive
		}
		var updateErr error
		user, updateErr = q.UpdateUser(r.Context(), txParams)
		if updateErr != nil {
			return updateErr
		}
		if locked.IsActive && !user.IsActive {
			revokedSessions, updateErr = invalidateUserSessionsTx(r.Context(), q, id, revokedAt)
			if updateErr != nil {
				return updateErr
			}
			sessionsRevoked = true
		}
		return recordAuditOutbox(r, q, "user.update", "user", user.ID.String(), user.Username, http.StatusOK, map[string]any{
			"email": user.Email, "is_active": user.IsActive, "sessions_revoked": sessionsRevoked,
			"sso_sessions_cleared": len(revokedSessions),
		})
	}) {
		return
	}
	if sessionsRevoked {
		h.finalizeUserSessionRevocation(r.Context(), id, "user_deactivated", revokedSessions)
	}
	RespondJSON(w, http.StatusOK, mapUser(user))
}

// DeleteUser handles DELETE /api/v1/users/{id}/.
func (h *ResourceHandler) DeleteUser(w http.ResponseWriter, r *http.Request) {
	if !h.requireUserMutationRunner(w, r) {
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
	var revokedSessions []sqlc.SsoSession
	revokedAt := time.Now()
	if !h.mutateUser(w, r, http.StatusInternalServerError, apierror.DeleteError, "Failed to delete user", func(q UserMutationTx) error {
		locked, lockErr := q.GetUserByIDForUpdate(r.Context(), id)
		if lockErr != nil {
			return lockErr
		}
		existing = locked
		var revokeErr error
		revokedSessions, revokeErr = invalidateUserSessionsTx(r.Context(), q, id, revokedAt)
		if revokeErr != nil {
			return revokeErr
		}
		if deleteErr := q.DeleteUser(r.Context(), id); deleteErr != nil {
			return deleteErr
		}
		return recordAuditOutbox(r, q, "user.delete", "user", existing.ID.String(), existing.Username, http.StatusNoContent, map[string]any{
			"email": existing.Email, "sessions_revoked": true, "sso_sessions_cleared": len(revokedSessions),
		})
	}) {
		return
	}
	h.finalizeUserSessionRevocation(r.Context(), id, "user_deleted", revokedSessions)
	// ON DELETE CASCADE on the role-binding tables means every binding for
	// this user just vanished. Invalidate so the cache doesn't keep handing
	// out the old set for up to one TTL.
	if h.rbacCache != nil {
		h.rbacCache.Invalidate(existing.ID.String())
	}
	w.WriteHeader(http.StatusNoContent)
}

// ResetUserPassword handles POST /api/v1/users/{id}/reset-password/.
func (h *ResourceHandler) ResetUserPassword(w http.ResponseWriter, r *http.Request) {
	if !h.requireUserMutationRunner(w, r) {
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
	} else if err := auth.ValidatePassword(req.Password, h.passwordPolicy(r.Context())); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, err.Error())
		return
	}
	hashed, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.HashError, "Failed to hash password")
		return
	}
	passwordParams := sqlc.UpdateUserPasswordParams{
		ID:       id,
		Password: string(hashed),
	}
	var revokedSessions []sqlc.SsoSession
	revokedAt := time.Now()
	if !h.mutateUser(w, r, http.StatusInternalServerError, apierror.UpdateError, "Failed to reset password and invalidate sessions", func(q UserMutationTx) error {
		locked, lockErr := q.GetUserByIDForUpdate(r.Context(), id)
		if lockErr != nil {
			return lockErr
		}
		existing = locked
		if updateErr := q.UpdateUserPassword(r.Context(), passwordParams); updateErr != nil {
			return updateErr
		}
		var revokeErr error
		revokedSessions, revokeErr = invalidateUserSessionsTx(r.Context(), q, id, revokedAt)
		if revokeErr != nil {
			return revokeErr
		}
		return recordAuditOutbox(r, q, "user.reset_password", "user", existing.ID.String(), existing.Username, http.StatusOK, map[string]any{
			"generated": generated, "sso_sessions_cleared": len(revokedSessions),
		})
	}) {
		return
	}
	h.finalizeUserSessionRevocation(r.Context(), id, "admin_password_reset", revokedSessions)
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
	if !h.requireUserMutationRunner(w, r) {
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
	if !h.mutateUser(w, r, http.StatusInternalServerError, apierror.UpdateError, "Failed to unlock user", func(q UserMutationTx) error {
		locked, lockErr := q.GetUserByIDForUpdate(r.Context(), id)
		if lockErr != nil {
			return lockErr
		}
		existing = locked
		if unlockErr := q.UnlockUser(r.Context(), id); unlockErr != nil {
			return unlockErr
		}
		return recordAuditOutbox(r, q, "admin.user.unlocked", "user", existing.ID.String(), existing.Username, http.StatusOK, map[string]any{
			"previous_locked_until": formatTimestamptz(existing.LockedUntil), "previous_locked_reason": existing.LockedReason,
			"previous_failed_count": existing.FailedLoginCount,
		})
	}) {
		return
	}
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
	if !h.requireUserMutationRunner(w, r) {
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
	var sessions []sqlc.SsoSession
	if !h.mutateUser(w, r, http.StatusInternalServerError, apierror.UpdateError, "Failed to invalidate sessions", func(q UserMutationTx) error {
		locked, lockErr := q.GetUserByIDForUpdate(r.Context(), id)
		if lockErr != nil {
			return lockErr
		}
		existing = locked
		var revokeErr error
		sessions, revokeErr = invalidateUserSessionsTx(r.Context(), q, id, now)
		if revokeErr != nil {
			return revokeErr
		}
		return recordAuditOutbox(r, q, "admin.user.force_logged_out", "user", existing.ID.String(), existing.Username, http.StatusOK, map[string]any{
			"tokens_invalidated_at": now.UTC().Format(time.RFC3339), "sso_sessions_cleared": len(sessions),
		})
	}) {
		return
	}
	backchannelOK, backchannelFailed := h.finalizeUserSessionRevocation(r.Context(), id, "admin_force_logout", sessions)
	RespondJSONUnwrapped(w, http.StatusOK, map[string]any{
		"success":                true,
		"message":                "All active sessions invalidated",
		"tokens_invalidated_at":  now.UTC().Format(time.RFC3339),
		"sso_sessions_cleared":   len(sessions),
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

// invalidateUserSessionsTx captures upstream sessions for post-commit logout,
// advances the local JWT cutoff, and deletes the session credentials in one
// database transaction.
func invalidateUserSessionsTx(ctx context.Context, q UserMutationTx, id uuid.UUID, invalidatedAt time.Time) ([]sqlc.SsoSession, error) {
	sessions, err := q.ListSSOSessionsByUser(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := q.InvalidateAllTokens(ctx, sqlc.InvalidateAllTokensParams{
		ID:                  id,
		TokensInvalidatedAt: pgtype.Timestamptz{Time: invalidatedAt, Valid: true},
	}); err != nil {
		return nil, err
	}
	if err := q.DeleteSSOSessionsByUser(ctx, id); err != nil {
		return nil, err
	}
	return sessions, nil
}

// finalizeUserSessionRevocation performs effects that must never happen before
// the database commit: local cache eviction, metrics, and upstream logout.
func (h *ResourceHandler) finalizeUserSessionRevocation(ctx context.Context, id uuid.UUID, reason string, sessions []sqlc.SsoSession) (int, int) {
	auth.SessionRevocationsTotal.WithLabelValues(observability.MetricValues("user", reason)...).Inc()
	if h.jwt != nil {
		h.jwt.InvalidateUser(ctx, id)
	}
	if h.ssoBackchannel == nil || h.encryptor == nil {
		return 0, 0
	}

	succeeded := 0
	failed := 0
	for _, session := range sessions {
		if session.EndSessionEndpoint == "" {
			continue
		}
		idToken, err := h.encryptor.Decrypt(session.UpstreamIDTokenEncrypted)
		if err != nil {
			failed++
			auth.SSOLogoutsTotal.WithLabelValues(observability.MetricValues(session.ProviderName, "encrypt_error")...).Inc()
			continue
		}
		if err := h.ssoBackchannel.PostEndSession(ctx, session.EndSessionEndpoint, idToken); err != nil {
			failed++
			auth.SSOLogoutsTotal.WithLabelValues(observability.MetricValues(session.ProviderName, "backchannel_failed")...).Inc()
			continue
		}
		succeeded++
		auth.SSOLogoutsTotal.WithLabelValues(observability.MetricValues(session.ProviderName, "backchannel_ok")...).Inc()
	}
	return succeeded, failed
}

func formatTimestamptz(t pgtype.Timestamptz) string {
	if !t.Valid {
		return ""
	}
	return t.Time.UTC().Format(time.RFC3339)
}
