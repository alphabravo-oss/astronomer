package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

// --- Verify (challenge -> session JWT) ---

// openapi:request-operation postAuthTotpVerify
type verifyRequest struct {
	ChallengeToken string `json:"challenge_token" validate:"required"`
	Code           string `json:"code" validate:"required"`
	// Optional explicit hint so the client can choose to send a
	// recovery code (e.g. user's phone is dead). When empty, we try
	// TOTP first then fall back to recovery; both paths emit distinct
	// audit actions.
	UseRecovery bool `json:"use_recovery"`
}

// Verify handles POST /api/v1/auth/totp/verify/.
//
// Body: { challenge_token, code, use_recovery? }. On success, mints
// the real session JWT pair and returns it in the same shape the
// regular Login endpoint does. On failure, increments the user's
// failed-login counter (same lockout policy as bcrypt) and returns
// 401.
//
// The handler is mounted PUBLIC (no auth middleware) — the
// challenge_token is the user's proof of identity at this stage.
func (h *TOTPHandler) Verify(w http.ResponseWriter, r *http.Request) {
	if !h.requireRunner(w, r) {
		return
	}
	var req verifyRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}
	claims, err := h.jwt.ValidateToken(req.ChallengeToken)
	if err != nil {
		if !h.auditEvent(w, r, pgtype.UUID{}, "auth.totp.verify_failed", "", "", http.StatusUnauthorized, map[string]any{
			"reason": "invalid_challenge",
		}) {
			return
		}
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.InvalidChallenge, "Challenge token is invalid or expired")
		return
	}
	if claims.TokenType != auth.PurposeToken || claims.Purpose != auth.PurposeTOTPChallenge {
		if !h.auditEvent(w, r, pgtype.UUID{Bytes: claims.UserID, Valid: true}, "auth.totp.verify_failed", claims.UserID.String(), "", http.StatusUnauthorized, map[string]any{
			"reason": "wrong_purpose",
		}) {
			return
		}
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.InvalidChallenge, "Challenge token is not a TOTP challenge")
		return
	}
	userID := claims.UserID

	user, err := h.users.GetUserByID(r.Context(), userID)
	if err != nil || !user.IsActive {
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.AuthenticationRequired, "Invalid credentials")
		return
	}
	if user.LockedUntil.Valid && user.LockedUntil.Time.After(time.Now()) {
		RespondRequestError(w, r, http.StatusLocked, apierror.AccountLocked, "Account is temporarily locked. Try again later or contact an administrator.")
		return
	}

	enrollment, err := h.queries.GetUserTOTPEnrollment(r.Context(), userID)
	if err != nil {
		// Race: user disabled TOTP after the challenge was issued.
		// Reject — the client should restart the login.
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.NotEnrolled, "TOTP is not enabled for this account")
		return
	}
	secret, err := h.encryptor.Decrypt(enrollment.SecretEncrypted)
	if err != nil {
		h.log.Warn("totp decrypt failed", "user_id", userID.String(), "error", err)
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Stored secret could not be read")
		return
	}

	// Try TOTP first unless the client explicitly opted into recovery
	// (the "lost phone" path).
	verified := false
	usedRecovery := false
	if !req.UseRecovery {
		ok2, vErr := auth.VerifyCode(secret, req.Code)
		if vErr == nil && ok2 {
			verified = true
		}
	}
	// Runtime TTL resolution can read through the pool, so prepare before
	// acquiring transaction locks. Signing and publication follow commit.
	session, err := h.jwt.PrepareTokenPairContext(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.TokenError, "Failed to prepare session")
		return
	}
	var locked, inactive, newlyLocked bool
	if !h.mutate(w, r, func(q TOTPMutationTx) error {
		current, err := lockTOTPEnrollment(r.Context(), q, userID, enrollment)
		if err != nil {
			return err
		}
		user = current
		inactive = !user.IsActive
		locked = user.LockedUntil.Valid && user.LockedUntil.Time.After(time.Now())
		if inactive || locked {
			status := http.StatusUnauthorized
			if locked {
				status = http.StatusLocked
			}
			return recordAuditOutboxAs(r, q, pgtype.UUID{Bytes: userID, Valid: true},
				"auth.totp.verify_failed", "user", userID.String(), user.Username, status,
				map[string]any{"reason": "account_unavailable"})
		}
		if !verified {
			rows, err := q.ConsumeRecoveryCode(r.Context(), sqlc.ConsumeRecoveryCodeParams{
				UserID: userID, CodeHash: auth.HashRecoveryCode(req.Code),
				UsedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
			})
			if err != nil {
				return err
			}
			verified, usedRecovery = rows == 1, rows == 1
		}
		if !verified {
			newlyLocked, err = h.recordFailedTOTPAttempt(r.Context(), q, user)
			if err != nil {
				return err
			}
			return recordAuditOutboxAs(r, q, pgtype.UUID{Bytes: userID, Valid: true},
				"auth.totp.verify_failed", "user", userID.String(), user.Username, http.StatusUnauthorized,
				map[string]any{"reason": "bad_code"})
		}
		if err := consumeTOTPChallenge(r.Context(), q, claims); err != nil {
			return err
		}
		if err := q.ResetFailedLoginCount(r.Context(), userID); err != nil {
			return err
		}
		if err := q.TouchUserTOTPLastUsed(r.Context(), sqlc.TouchUserTOTPLastUsedParams{
			UserID: userID, LastUsedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
		}); err != nil {
			return err
		}
		if err := q.UpdateUserLastLogin(r.Context(), userID); err != nil {
			return err
		}
		action := "auth.totp.verified"
		if usedRecovery {
			action = "auth.totp.recovery_code_consumed"
		}
		return recordAuditOutboxAs(r, q, pgtype.UUID{Bytes: userID, Valid: true},
			action, "user", userID.String(), user.Username, http.StatusOK, nil)
	}) {
		return
	}
	if locked {
		RespondRequestError(w, r, http.StatusLocked, apierror.AccountLocked, "Account is temporarily locked. Try again later or contact an administrator.")
		return
	}
	if inactive {
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.AuthenticationRequired, "Invalid credentials")
		return
	}
	if !verified {
		if newlyLocked {
			auth.AccountLockoutsTotal.WithLabelValues(observability.MetricValues(auth.LockoutReasonTooManyFailedAttempts)...).Inc()
		}
		auth.TOTPVerifiesTotal.WithLabelValues(observability.MetricValues("failed")...).Inc()
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.AuthenticationRequired, "Invalid TOTP or recovery code")
		return
	}
	h.jwt.InvalidateJTI(r.Context(), claims.ID)
	accessToken, refreshToken, err := h.jwt.SignPreparedTokenPair(userID, session)
	if err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.TokenError, "Failed to generate session")
		return
	}
	outcome := "success"
	if usedRecovery {
		outcome = "recovery"
	}
	auth.TOTPVerifiesTotal.WithLabelValues(observability.MetricValues(outcome)...).Inc()

	setBrowserSessionCookies(w, r, accessToken, refreshToken)
	RespondJSON(w, http.StatusOK, LoginResponse{
		Token:   accessToken,
		Refresh: refreshToken,
		User:    userToResponse(user),
	})
}

func (h *TOTPHandler) recordFailedTOTPAttempt(ctx context.Context, q TOTPMutationTx, user sqlc.User) (bool, error) {
	threshold, duration := h.effectiveLockoutPolicy()
	now := time.Now().UTC()
	updated, err := q.RecordFailedLoginAttempt(ctx, sqlc.RecordFailedLoginAttemptParams{
		ID: user.ID, FailedLoginAt: pgtype.Timestamptz{Time: now, Valid: true},
		LockoutThreshold: int32(threshold), LockedUntil: pgtype.Timestamptz{Time: now.Add(duration), Valid: true},
		LockedReason: auth.LockoutReasonTooManyFailedAttempts,
	})
	return err == nil && updated.LockedUntil.Valid && updated.LockedUntil.Time.After(now), err
}

func consumeTOTPChallenge(ctx context.Context, q TOTPMutationTx, claims *auth.Claims) error {
	if claims == nil || claims.ID == "" || claims.ExpiresAt == nil {
		return errTOTPChallengeUsed
	}
	rows, err := q.ConsumeJWTChallenge(ctx, sqlc.ConsumeJWTChallengeParams{
		Jti: claims.ID, UserID: claims.UserID, ExpiresAt: claims.ExpiresAt.Time, Reason: "totp_challenge_consumed",
	})
	if err != nil {
		return err
	}
	if rows != 1 {
		return errTOTPChallengeUsed
	}
	return nil
}
