package handler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
)

var _ context.Context // imported indirectly by the sqlc method signatures

// passwordResetTTL is the lifetime of an emailed reset link. 30
// minutes matches Rancher; long enough for the email to land + the
// user to act, short enough that a leaked link is hard to exploit.
const passwordResetTTL = 30 * time.Minute

// PasswordResetRequest is the body of POST /auth/password-reset/request/.
type PasswordResetRequest struct {
	Email string `json:"email"`
}

// PasswordResetComplete is the body of POST /auth/password-reset/complete/.
type PasswordResetComplete struct {
	Token       string `json:"token"`
	NewPassword string `json:"new_password"`
}

// PasswordResetRequest handles POST /api/v1/auth/password-reset/request/.
//
// ALWAYS returns 202 — we never reveal whether the email matched a
// user. That removes the easy email-enumeration vector while keeping
// the user-facing UX identical ("check your email").
//
// If the email matches a user AND the auth handler has a reset store
// AND an email notifier, we:
//  1. generate a 32-byte random token, hex-encoded
//  2. persist the shared opaque-token hash + a snapshot of the user's current
//     password hash; a password change before consume invalidates
//     this token
//  3. enqueue a `password_reset` email with the reset URL
//
// On any internal failure we still return 202 — the alternative is
// surfacing the enumeration vector through a status-code split.
func (h *AuthHandler) PasswordResetRequest(w http.ResponseWriter, r *http.Request) {
	var req PasswordResetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		// 202 with a no-op body — never tell the caller their request
		// body was invalid.
		w.WriteHeader(http.StatusAccepted)
		return
	}
	emailAddr := strings.TrimSpace(req.Email)
	if emailAddr == "" {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if h.passwordResets == nil || h.queries == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	// Look up the user. Errors collapse to 202 — every error here
	// would otherwise leak a yes/no signal.
	user, err := h.queries.GetUserByEmail(r.Context(), emailAddr)
	if err != nil {
		// Audit the "request for unknown email" so operators can
		// spot enumeration probes in the audit log. NULL user_id.
		recordAuditAs(r, h.audit, pgtype.UUID{}, "auth.password_reset_request", "user", "", emailAddr, map[string]any{
			"result": "no_user",
		})
		w.WriteHeader(http.StatusAccepted)
		return
	}

	// Generate a 32-byte random token; we store only the hash. The
	// plaintext goes in the email body.
	tokenPlain, err := randomToken()
	if err != nil {
		h.log.Warn("password reset token generation failed", "error", err)
		w.WriteHeader(http.StatusAccepted)
		return
	}
	tokenHash := auth.HashOpaqueToken(tokenPlain)

	if _, err := h.passwordResets.CreatePasswordResetToken(r.Context(), sqlc.CreatePasswordResetTokenParams{
		UserID:              user.ID,
		TokenHash:           tokenHash,
		PasswordHashAtIssue: user.Password,
		ExpiresAt:           time.Now().Add(passwordResetTTL),
	}); err != nil {
		h.log.Warn("password reset persist failed", "error", err)
		w.WriteHeader(http.StatusAccepted)
		return
	}

	// Fire the email best-effort. The reset URL is composed from the
	// platform_configuration server_url so the link points at the
	// dashboard the user actually uses.
	if h.emails != nil {
		resetURL := buildResetURL(h.platformServerURL(r.Context()), tokenPlain)
		h.emails.EnqueueAndLog(r.Context(), EmailNotifierRequest{
			To:       user.Email,
			Template: "password_reset",
			Data: map[string]any{
				"ResetURL": resetURL,
			},
			UserID: user.ID,
		})
	}

	recordAuditAs(r, h.audit, pgtype.UUID{Bytes: user.ID, Valid: true},
		"auth.password_reset_request", "user", user.ID.String(), user.Username, map[string]any{
			"result": "issued",
		})
	w.WriteHeader(http.StatusAccepted)
}

// PasswordResetComplete handles POST /api/v1/auth/password-reset/complete/.
//
// Verifies the token + (snapshotted password hash matches current) +
// not expired + not already used. Updates the password via the same
// rehasher the Login flow uses, wipes every outstanding reset token
// for the user, and bumps the JWT cutoff so existing sessions land in
// the next-validation deny path.
func (h *AuthHandler) PasswordResetComplete(w http.ResponseWriter, r *http.Request) {
	var req PasswordResetComplete
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	if req.Token == "" || req.NewPassword == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "token and new_password are required")
		return
	}
	if h.passwordResets == nil || h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.NotConfigured, "Password reset is not configured")
		return
	}
	// AUTH-R01: enforce the live platform password policy so a reset
	// cannot downgrade below CreateUser/ChangePassword strength. bcrypt
	// also rejects strings > 72 bytes at hash time.
	if len(req.NewPassword) > 72 {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "new_password must be at most 72 characters")
		return
	}
	if err := auth.ValidatePassword(req.NewPassword, h.passwordPolicy(r.Context())); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, err.Error())
		return
	}

	tokenHash := auth.HashOpaqueToken(req.Token)
	row, err := h.passwordResets.GetPasswordResetTokenByHash(r.Context(), tokenHash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidToken, "Reset token is invalid or expired")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ReadError, "Failed to verify token")
		return
	}
	if row.UsedAt.Valid {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidToken, "Reset token has already been used")
		return
	}
	if time.Now().After(row.ExpiresAt) {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidToken, "Reset token has expired")
		return
	}
	user, err := h.queries.GetUserByID(r.Context(), row.UserID)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidToken, "Reset token is invalid")
		return
	}
	// Snapshot check: if the password has changed since the token
	// was issued, refuse — that handles "user changed password
	// already, the link in the old email is stale".
	if user.Password != row.PasswordHashAtIssue {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidToken, "Reset token is no longer valid")
		return
	}

	newHash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.HashError, "Failed to hash password")
		return
	}

	// Race guard: ConsumePasswordResetToken returns 0 if a parallel
	// request already consumed the row, so the password update is
	// gated on this UPDATE first.
	rows, err := h.passwordResets.ConsumePasswordResetToken(r.Context(), sqlc.ConsumePasswordResetTokenParams{
		TokenHash: tokenHash,
		UsedAt:    pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	if err != nil || rows == 0 {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidToken, "Reset token has already been used")
		return
	}
	if err := h.passwordResets.UpdateUserPassword(r.Context(), sqlc.UpdateUserPasswordParams{
		ID:       user.ID,
		Password: newHash,
	}); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.UpdateError, "Failed to update password")
		return
	}
	// Wipe every other outstanding reset token for the user so an
	// attacker who somehow has two emails can't replay the second
	// after the first lands.
	_ = h.passwordResets.DeletePasswordResetTokensForUser(r.Context(), user.ID)
	// Invalidate all existing sessions: anyone holding a JWT issued
	// before now should be forced to re-auth with the new password.
	if h.revocation != nil {
		_ = h.revocation.InvalidateAllTokens(r.Context(), sqlc.InvalidateAllTokensParams{
			ID:                  user.ID,
			TokensInvalidatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
		})
	}
	// Flush the JWT validation cache so the freshly-bumped cutoff takes
	// effect on the very next request. checkRevocations short-circuits on
	// its positive-result cache for up to JWTValidationCacheTTL (30s), so
	// without this an attacker session validated within the last window
	// keeps passing for up to 30s after the reset. Mirrors the force-logout
	// and SCIM revocation paths.
	if h.jwt != nil {
		h.jwt.InvalidateCache()
	}

	recordAuditAs(r, h.audit, pgtype.UUID{Bytes: user.ID, Valid: true},
		"auth.password_reset_complete", "user", user.ID.String(), user.Username, nil)
	RespondJSONUnwrapped(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "Password updated",
	})
}

// randomToken returns 32 cryptographically-random bytes hex-encoded.
// Roughly 256 bits of entropy; well above the 128-bit threshold for
// "indistinguishable from random" guesses.
func randomToken() (string, error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}

// buildResetURL composes the dashboard URL the user clicks from a
// trusted base + the reset token.
//
// The base MUST come from the operator-configured
// platform_configuration.server_url (see platformServerURL), never from
// the request's Host / X-Forwarded-Host headers: those are
// attacker-controllable, so deriving the link host from them lets a
// crafted request mint a valid reset token inside a link pointing at an
// attacker-owned domain — a victim who clicks it leaks the token and
// the attacker takes over the account. When the base is empty we emit a
// host-relative link rather than fall back to the untrusted request
// host.
func buildResetURL(base, token string) string {
	return base + "/reset-password?token=" + token
}

// platformServerURL returns the trusted base URL for emailed links,
// taken from platform_configuration.server_url (trailing slash trimmed),
// or "" when it is unavailable/unset.
//
// AuthHandler's querier interfaces do not expose GetPlatformConfig, but
// the concrete *sqlc.Queries that backs them in production does, so we
// resolve it via a capability type-assertion (mirroring the anonymous
// GetPlatformConfig interface agentServerURLFor relies on). Narrow test
// fakes that lack the method fall through to the empty (host-relative)
// base. We deliberately do NOT fall back to request headers here.
func (h *AuthHandler) platformServerURL(ctx context.Context) string {
	type platformConfigGetter interface {
		GetPlatformConfig(ctx context.Context) (sqlc.PlatformConfiguration, error)
	}
	var g platformConfigGetter
	if pc, ok := h.passwordResets.(platformConfigGetter); ok {
		g = pc
	} else if pc, ok := h.queries.(platformConfigGetter); ok {
		g = pc
	}
	if g == nil {
		return ""
	}
	cfg, err := g.GetPlatformConfig(ctx)
	if err != nil {
		return ""
	}
	return strings.TrimRight(strings.TrimSpace(cfg.ServerUrl), "/")
}
