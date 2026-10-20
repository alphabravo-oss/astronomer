package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/pkg/safeconv"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/api/apiutil"
	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest
	if err := decodeV1JSONBody(r, &req); err != nil {
		apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.InvalidBody, "Invalid JSON body")
		return
	}

	email, emailErr := normalizeLoginEmail(req.Email)
	if req.Email == "" {
		apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.ValidationError, "Email is required")
		return
	}
	if emailErr != nil {
		apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.InvalidEmail, "Enter a valid email address")
		return
	}

	if req.Password == "" {
		apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.AuthenticationRequired, "Password is required")
		return
	}

	ctx := r.Context()
	var user sqlc.User
	var err error

	user, err = h.queries.GetUserByEmail(ctx, email)
	if err != nil {
		// User-not-found is recorded under the attempted identifier so a
		// brute-force scan for valid accounts is visible in the audit
		// stream. user_id stays NULL because there's no row to attribute.
		if auditErr := h.recordCredentialAuditAs(r, pgtype.UUID{}, "auth.login_failed", "user", "", email, map[string]any{"reason": "user_not_found"}); auditErr != nil {
			apiutil.WriteError(w, r, http.StatusServiceUnavailable, apiutil.AuditUnavailable,
				"Mandatory audit storage is unavailable; authentication is temporarily unavailable")
			return
		}
		apiutil.WriteError(w, r, http.StatusUnauthorized, apiutil.AuthenticationRequired, "Invalid credentials")
		return
	}

	// Account-lockout gate. Sits BEFORE bcrypt so a locked account
	// can't be probed for password validity (which would also chew
	// CPU). An expired lock falls through naturally because the
	// timestamp comparison returns false. NIST 800-53 AC-7.
	if user.LockedUntil.Valid && user.LockedUntil.Time.After(time.Now()) {
		if auditErr := h.recordCredentialAuditAs(r, pgtype.UUID{Bytes: user.ID, Valid: true},
			"auth.login_locked", "user", user.ID.String(), user.Username, map[string]any{
				"reason":        "account_locked",
				"locked_until":  user.LockedUntil.Time.UTC().Format(time.RFC3339),
				"locked_reason": user.LockedReason,
			}); auditErr != nil {
			apiutil.WriteError(w, r, http.StatusServiceUnavailable, apiutil.AuditUnavailable,
				"Mandatory audit storage is unavailable; authentication is temporarily unavailable")
			return
		}
		// 423 Locked is the RFC 4918 status that fits best; we keep
		// the JSON error envelope shape unchanged so the frontend
		// can surface "account_locked" without parsing the status.
		apiutil.WriteError(w, r, http.StatusLocked, apiutil.AccountLocked, "Account is temporarily locked. Try again later or contact an administrator.")
		return
	}

	ok, needsRehash, verifyErr := auth.VerifyPassword(user.Password, req.Password)
	if verifyErr != nil {
		// A malformed stored hash is treated as a credential failure to avoid
		// leaking schema details. The error is logged for operators.
		if h.log != nil {
			h.log.Warn("password verification error", "user_id", user.ID.String(), "error", verifyErr)
		}
		if attemptErr := h.handleFailedAttempt(ctx, r, user, "verify_error"); attemptErr != nil {
			respondTransactionalMutationError(w, r, attemptErr, http.StatusServiceUnavailable, apiutil.StatusError, "Authentication safeguards are temporarily unavailable")
			return
		}
		apiutil.WriteError(w, r, http.StatusUnauthorized, apiutil.AuthenticationRequired, "Invalid credentials")
		return
	}
	if !ok {
		if attemptErr := h.handleFailedAttempt(ctx, r, user, "bad_password"); attemptErr != nil {
			respondTransactionalMutationError(w, r, attemptErr, http.StatusServiceUnavailable, apiutil.StatusError, "Authentication safeguards are temporarily unavailable")
			return
		}
		apiutil.WriteError(w, r, http.StatusUnauthorized, apiutil.AuthenticationRequired, "Invalid credentials")
		return
	}

	if !user.IsActive {
		if auditErr := h.recordCredentialAuditAs(r, pgtype.UUID{Bytes: user.ID, Valid: true}, "auth.login_failed", "user", user.ID.String(), user.Username, map[string]any{"reason": "account_disabled"}); auditErr != nil {
			apiutil.WriteError(w, r, http.StatusServiceUnavailable, apiutil.AuditUnavailable,
				"Mandatory audit storage is unavailable; authentication is temporarily unavailable")
			return
		}
		apiutil.WriteError(w, r, http.StatusForbidden, apiutil.AccountDisabled, "Account is disabled")
		return
	}

	// Opportunistically upgrade legacy Django PBKDF2/argon2 hashes to bcrypt.
	// Failure here is non-fatal — we still log the user in.
	if needsRehash && h.rehasher != nil {
		if newHash, hashErr := auth.HashPassword(req.Password); hashErr == nil {
			if err := h.rehasher.UpdateUserPasswordHash(ctx, sqlc.UpdateUserPasswordHashParams{
				ID:       user.ID,
				Password: newHash,
			}); err != nil && h.log != nil {
				h.log.Warn("failed to rehash password", "user_id", user.ID.String(), "error", err)
			}
		} else if h.log != nil {
			h.log.Warn("failed to compute bcrypt hash for rehash", "user_id", user.ID.String(), "error", hashErr)
		}
	}

	if h.handleLoginTOTP(w, r, user) {
		return
	}

	// Password-only authentication is complete here. MFA accounts returned a
	// challenge above and reset their shared failure counter only after the
	// second factor succeeds in TOTPHandler.Verify.
	if h.lockout != nil {
		if err := h.lockout.ResetFailedLoginCount(ctx, user.ID); err != nil && h.log != nil {
			h.log.Warn("failed to reset login failure counter", "user_id", user.ID.String(), "error", err)
		}
	}

	mintCtx := h.applySessionTimeoutFromSettings(r.Context())
	pair, err := h.jwt.PrepareTokenPairContext(mintCtx)
	if err != nil {
		apiutil.WriteError(w, r, http.StatusInternalServerError, apiutil.TokenError, "Failed to prepare session")
		return
	}
	_, err = executeMutation(r, h.runTx,
		func(q AuthMutationTx) (sqlc.User, error) {
			if err := createRefreshSession(r.Context(), q, user.ID, pair); err != nil {
				return sqlc.User{}, err
			}
			return user, nil
		},
		func(committed sqlc.User) mutationAuditEvent {
			return mutationAuditEvent{action: "auth.login", resourceType: "user", resourceID: committed.ID.String(),
				resourceName: committed.Username, status: http.StatusOK, detail: map[string]any{"identifier_type": "email"}}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusServiceUnavailable, apiutil.AuditUnavailable,
			"The browser session could not be persisted")
		return
	}
	accessToken, refreshToken, err := h.jwt.SignPreparedTokenPair(user.ID, pair)
	if err != nil {
		apiutil.WriteError(w, r, http.StatusServiceUnavailable, apiutil.TokenError, "Failed to generate session")
		return
	}

	// Update last_login after session commit; identity history does not weaken
	// the durable session/audit transaction if this best-effort write fails.
	_ = h.queries.UpdateUserLastLogin(ctx, user.ID)

	setBrowserSessionCookies(w, r, accessToken, refreshToken)
	RespondJSON(w, http.StatusOK, LoginResponse{User: userToResponse(user)})
}

func normalizeLoginEmail(value string) (string, error) {
	email := strings.TrimSpace(value)
	parsed, err := mail.ParseAddress(email)
	if err != nil {
		return "", err
	}
	if parsed.Address != email {
		return "", fmt.Errorf("display-name email format is not accepted")
	}
	return strings.ToLower(email), nil
}

// handleFailedAttempt is the shared post-bcrypt-miss branch: increment
// the per-user failure counter, lock the account when the threshold is
// reached, and commit the audit evidence in the same transaction. Audit or
// transaction failure prevents a successful authentication response.
//
// `reason` distinguishes between "bcrypt mismatch" (bad_password) and
// "stored hash unparseable" (verify_error); both count toward the
// threshold because either way the caller didn't prove possession of
// the credential.
func (h *AuthHandler) handleFailedAttempt(ctx context.Context, r *http.Request, user sqlc.User, reason string) error {
	threshold, lockDur := h.effectiveLockoutPolicy()
	now := time.Now().UTC()
	lockedUntil := now.Add(lockDur)

	if h.runTx == nil {
		return audit.ErrOutboxUnavailable
	}

	var updated sqlc.User
	err := h.runTx(ctx, func(q AuthMutationTx) error {
		var mutationErr error
		updated, mutationErr = q.RecordFailedLoginAttempt(ctx, sqlc.RecordFailedLoginAttemptParams{
			ID: user.ID, FailedLoginAt: pgtype.Timestamptz{Time: now, Valid: true},
			LockoutThreshold: safeconv.Int32(threshold), LockedUntil: pgtype.Timestamptz{Time: lockedUntil, Valid: true},
			LockedReason: auth.LockoutReasonTooManyFailedAttempts,
		})
		if mutationErr != nil {
			return mutationErr
		}
		locked := updated.LockedUntil.Valid && !updated.LockedUntil.Time.Before(now)
		action := "auth.login_failed"
		if locked {
			action = "auth.login_locked"
		}
		detail := map[string]any{
			"reason": reason, "failed_login_count": updated.FailedLoginCount,
			"lockout_threshold": threshold, "locked": locked,
		}
		if locked {
			detail["locked_until"] = updated.LockedUntil.Time.UTC().Format(time.RFC3339)
		}
		return recordAuditOutboxAs(r, q, pgtype.UUID{Bytes: user.ID, Valid: true},
			action, "user", user.ID.String(), user.Username, http.StatusUnauthorized, detail)
	})
	if err != nil {
		return err
	}
	if updated.LockedUntil.Valid && !updated.LockedUntil.Time.Before(now) {
		auth.AccountLockoutsTotal.WithLabelValues(observability.MetricValues(auth.LockoutReasonTooManyFailedAttempts)...).Inc()
		if h.emails != nil && user.Email != "" {
			h.emails.EnqueueAndLog(ctx, EmailNotifierRequest{
				To: user.Email, Template: "account_locked", UserID: user.ID,
				Data: map[string]any{"Username": user.Username, "UnlockAt": updated.LockedUntil.Time.UTC().Format(time.RFC3339)},
			})
		}
	}
	return nil
}

// Refresh handles POST /api/v1/auth/refresh/.
