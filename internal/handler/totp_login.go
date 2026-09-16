package handler

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
)

// handleLoginTOTP reads enrollment once after password authentication. An
// unavailable enrollment store must never turn MFA into password-only login.
// It returns true when it has handled the response (challenge or failure).
func (h *AuthHandler) handleLoginTOTP(w http.ResponseWriter, r *http.Request, user sqlc.User) bool {
	if h.totpGate == nil {
		return false
	}
	enrolled, err := h.totpGate.IsEnrolled(r.Context(), user.ID)
	if err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.StatusError, "MFA enrollment status is temporarily unavailable")
		return true
	}
	if enrolled {
		h.issueTOTPLoginChallenge(w, r, user, auth.PurposeTOTPChallenge, "auth.login_totp_required", "totp_required")
		return true
	}
	if h.totpEnforced(r.Context()) {
		h.issueTOTPLoginChallenge(w, r, user, auth.PurposeTOTPEnrollOnly, "auth.login_totp_enroll_required", "totp_enrollment_required")
		return true
	}
	return false
}

// A refresh token cannot bypass enrollment after administrators require MFA.
func (h *AuthHandler) handleRefreshTOTP(w http.ResponseWriter, r *http.Request, user sqlc.User) bool {
	if !h.totpEnforced(r.Context()) || h.totpGate == nil {
		return false
	}
	enrolled, err := h.totpGate.IsEnrolled(r.Context(), user.ID)
	if err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.StatusError, "MFA enrollment status is temporarily unavailable")
		return true
	}
	if !enrolled {
		h.issueTOTPLoginChallenge(w, r, user, auth.PurposeTOTPEnrollOnly, "auth.refresh_totp_enroll_required", "totp_enrollment_required")
		return true
	}
	return false
}

func (h *AuthHandler) issueTOTPLoginChallenge(w http.ResponseWriter, r *http.Request, user sqlc.User, purpose, action, code string) {
	challenge, err := h.jwt.GeneratePurposeToken(user.ID, purpose, auth.TOTPChallengeTTL)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.TokenError, "Failed to mint TOTP challenge")
		return
	}
	detail := map[string]any(nil)
	if purpose == auth.PurposeTOTPChallenge {
		detail = map[string]any{"identifier_type": "email"}
	}
	if err := h.recordCredentialAuditAs(r, pgtype.UUID{Bytes: user.ID, Valid: true},
		action, "user", user.ID.String(), user.Username, detail); err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.AuditUnavailable,
			"Mandatory audit storage is unavailable; the authentication challenge was not issued")
		return
	}
	RespondJSONUnwrapped(w, http.StatusLocked, map[string]any{"error": code, "challenge_token": challenge})
}
