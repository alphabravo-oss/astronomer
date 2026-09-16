package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/reqctx"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
)

// chiURLParam is a tiny indirection so the rest of the file reads as
// `chiURLParam(r, "id")` — matching the existing convention used by
// other handlers in this package.
func chiURLParam(r *http.Request, key string) string {
	return chi.URLParam(r, key)
}

// base64URLEncoding is the alphabet shared by encodeTOTPChallenge /
// decodeTOTPChallenge. URL-safe so the value survives a query-string
// fallback without re-encoding.
var base64URLEncoding = base64.RawURLEncoding

// TOTPQuerier is the database surface the TOTP handler + the Login
// enrollment-gate need. Production wires *sqlc.Queries here.
//
// Kept narrow so tests can hand a tiny in-memory fake to the same
// constructors without dragging the rest of the schema along.
type TOTPQuerier interface {
	GetUserTOTPEnrollment(ctx context.Context, userID uuid.UUID) (sqlc.UserTotpEnrollment, error)
	CountUnusedRecoveryCodes(ctx context.Context, userID uuid.UUID) (int64, error)
}

// TOTPHandler owns the /auth/totp/* endpoints. It's split out from
// AuthHandler so the latter can stay focused on the password/JWT flow;
// AuthHandler holds a reference to the TOTP enrollment lookup for the
// Login gate (see AuthHandler.totp below).
type TOTPHandler struct {
	queries    TOTPQuerier
	users      UserQuerier
	rehasher   PasswordRehasher // for password verify on disable
	encryptor  *auth.Encryptor
	jwt        *auth.JWTManager
	runTx      totpRunTxFunc
	log        *slog.Logger
	issuer     string
	requireAll bool
	emails     EmailNotifier
	failThresh int
	lockoutDur time.Duration
}

// SetEmailNotifier attaches the email-enqueue hook used by the
// enroll-confirm / disable / regenerate paths to fire security-
// relevant FYI emails.
func (h *TOTPHandler) SetEmailNotifier(n EmailNotifier) { h.emails = n }

// NewTOTPHandler wires the TOTP handler. queries / users / encryptor /
// jwt are required at construction. Mutations also require SetRunTx.
func NewTOTPHandler(queries TOTPQuerier, users UserQuerier, encryptor *auth.Encryptor, jwt *auth.JWTManager) *TOTPHandler {
	return &TOTPHandler{
		queries:   queries,
		users:     users,
		encryptor: encryptor,
		jwt:       jwt,
		log:       slog.Default(),
		issuer:    "Astronomer",
	}
}

// SetLogger overrides the default logger.
func (h *TOTPHandler) SetLogger(l *slog.Logger) {
	if l != nil {
		h.log = l
	}
}

// SetIssuer overrides the issuer string shown in the authenticator
// app's account row. Comes from auth.totp.issuer in the chart.
func (h *TOTPHandler) SetIssuer(issuer string) {
	if issuer != "" {
		h.issuer = issuer
	}
}

// SetPasswordRehasher wires the user-row lookup used by Disable to
// require the current password (the rehasher interface coincidentally
// exposes the ClearMustChangePassword shape we don't need; we only
// reach for UpdateUserPasswordHash isn't even called here — so this is
// actually only for type symmetry. Disable verifies via the password
// stored on the user row pulled from `users`.)
func (h *TOTPHandler) SetPasswordRehasher(p PasswordRehasher) { h.rehasher = p }

// SetRequireAll switches the handler into the "every local-password
// user must enroll" mode. Login gate consults this via its own copy of
// the flag (see AuthHandler.SetTOTPRequireAll).
func (h *TOTPHandler) SetRequireAll(require bool) { h.requireAll = require }

// SetLockoutPolicy keeps the second-factor failure budget identical to the
// password failure budget. The database update is atomic across replicas.
func (h *TOTPHandler) SetLockoutPolicy(threshold int, duration time.Duration) {
	h.failThresh = threshold
	h.lockoutDur = duration
}

func (h *TOTPHandler) effectiveLockoutPolicy() (int, time.Duration) {
	threshold := h.failThresh
	if threshold <= 0 {
		threshold = auth.LoginFailureThreshold
	}
	duration := h.lockoutDur
	if duration <= 0 {
		duration = auth.LockoutDuration
	}
	return threshold, duration
}

// IsEnrolled distinguishes missing enrollment from unavailable storage so
// authentication cannot bypass MFA during an enrollment-read outage.
func (h *TOTPHandler) IsEnrolled(ctx context.Context, userID uuid.UUID) (bool, error) {
	if h == nil || h.queries == nil {
		return false, errors.New("TOTP enrollment store is unavailable")
	}
	_, err := h.queries.GetUserTOTPEnrollment(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// --- Enrollment: start ---

// enrollChallengeClaims is the encryptable payload tied into the
// enrollment challenge JWT. The plaintext secret lives in the JWT
// (signed under the platform JWT key) so a stolen-mid-flow attacker
// can't substitute their own secret; we never persist it until the
// confirm step.
type enrollChallengeClaims struct {
	Secret string `json:"s"`
	Label  string `json:"l"`
}

// EnrollStart handles POST /api/v1/auth/totp/enroll/start/.
//
// Generates a fresh secret + otpauth URL + QR PNG, parks the secret
// in a short-lived signed challenge JWT, and returns the package to
// the browser. Nothing lands in the DB at this stage — the user is
// free to abandon the flow.
func (h *TOTPHandler) EnrollStart(w http.ResponseWriter, r *http.Request) {
	if !h.requireRunner(w, r) {
		return
	}
	authUser, ok := reqctx.AuthenticatedUser(r.Context())
	if !ok || authUser == nil {
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.AuthenticationRequired, "Authentication required")
		return
	}
	userID, err := uuid.Parse(authUser.ID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Invalid user ID")
		return
	}

	// Account label = "<issuer>:<username|email>" — what the user sees
	// in their authenticator. Falls back to userID if neither is set.
	account := authUser.Username
	if account == "" {
		account = authUser.Email
	}
	if account == "" {
		account = userID.String()
	}

	secret, url, err := auth.GenerateSecret(account, h.issuer)
	if err != nil {
		h.log.Warn("totp generate secret failed", "user_id", userID.String(), "error", err)
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.TOTPGenerateFailed, "Failed to generate TOTP secret")
		return
	}
	qrDataURL, err := auth.QRCodeDataURL(url)
	if err != nil {
		h.log.Warn("totp generate qr failed", "user_id", userID.String(), "error", err)
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.TOTPGenerateFailed, "Failed to render QR code")
		return
	}

	// Encrypt the secret BEFORE stuffing it in the challenge JWT — the
	// JWT body is base64 (not encryption) so a leaked challenge token
	// would otherwise expose the secret to anyone watching the wire.
	// With Fernet wrapping, only this server (or its rotation peers)
	// can read it back during the confirm step.
	if h.encryptor == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.NotConfigured, "TOTP is not configured")
		return
	}
	encryptedSecret, err := h.encryptor.Encrypt(secret)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.TOTPEncryptFailed, "Failed to wrap TOTP secret")
		return
	}

	// Sign the challenge body into a 5-minute JWT. Purpose claim keeps
	// the regular auth middleware from accepting this as a session.
	payload, err := json.Marshal(enrollChallengeClaims{Secret: encryptedSecret, Label: account})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Failed to marshal challenge")
		return
	}
	// Wrap the JSON payload as a base64-url string and stuff it into a
	// purpose-bound JWT under a custom claim. We can't reuse the
	// Claims struct directly because Purpose is a string; instead we
	// stash the encrypted secret in the JWT's audience claim, which is
	// already a string slice and already part of RegisteredClaims.
	//
	// Trick: GeneratePurposeToken doesn't accept extra claims, so we
	// encode the payload as a single base64 string and pass it back
	// alongside the JWT. The client is opaque-treats both — it only
	// needs to echo them back on confirm.
	challenge := encodeTOTPChallenge(payload)
	token, err := h.jwt.GeneratePurposeToken(userID, auth.PurposeTOTPChallenge, auth.TOTPChallengeTTL)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.TokenError, "Failed to mint challenge token")
		return
	}

	// Audit (no secret, only the user_id + that we issued a challenge).
	if !h.auditEvent(w, r, pgtype.UUID{Bytes: userID, Valid: true},
		"auth.totp.enroll_started", userID.String(), authUser.Username, http.StatusOK, nil) {
		return
	}

	RespondJSON(w, http.StatusOK, map[string]any{
		"otpauth_url":     url,
		"qr_data_url":     qrDataURL,
		"challenge_token": token,
		"challenge":       challenge, // opaque encrypted-secret blob
		"issuer":          h.issuer,
	})
}

// --- Enrollment: confirm ---

// openapi:request-operation postAuthTotpEnrollConfirm
type enrollConfirmRequest struct {
	ChallengeToken string `json:"challenge_token" validate:"required"`
	Challenge      string `json:"challenge" validate:"required"`
	Code           string `json:"code" validate:"required"`
}

type enrollConfirmResponse struct {
	RecoveryCodes []string `json:"recovery_codes"`
	Enrolled      bool     `json:"enrolled"`
	// Token/Refresh are populated ONLY when the caller reached confirm via the
	// forced-enrollment challenge (they had no session). Completing enrollment
	// logs them in, so we mint and return the real session pair here.
	Token   string `json:"token,omitempty"`
	Refresh string `json:"refresh,omitempty"`
}

// EnrollConfirm handles POST /api/v1/auth/totp/enroll/confirm/.
//
// Body: { challenge_token, challenge, code }. Validates the challenge
// token (signature, expiry, purpose), decrypts the secret, verifies
// the user-supplied 6-digit code, persists the enrollment row, and
// generates+returns 10 recovery codes (shown ONCE).
func (h *TOTPHandler) EnrollConfirm(w http.ResponseWriter, r *http.Request) {
	if !h.requireRunner(w, r) {
		return
	}
	authUser, ok := reqctx.AuthenticatedUser(r.Context())
	if !ok || authUser == nil {
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.AuthenticationRequired, "Authentication required")
		return
	}
	userID, err := uuid.Parse(authUser.ID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Invalid user ID")
		return
	}

	var req enrollConfirmRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}

	claims, err := h.jwt.ValidateToken(req.ChallengeToken)
	if err != nil || claims.TokenType != auth.PurposeToken || claims.Purpose != auth.PurposeTOTPChallenge {
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.InvalidChallenge, "Challenge token is invalid or expired")
		return
	}
	if claims.UserID != userID {
		// The challenge MUST belong to the same user that's authenticated.
		// Otherwise a holder of a leaked challenge could enroll on behalf
		// of someone else.
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.InvalidChallenge, "Challenge token does not match the authenticated user")
		return
	}

	payload, err := decodeTOTPChallenge(req.Challenge)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidChallenge, "Challenge payload is malformed")
		return
	}
	var c enrollChallengeClaims
	if err := json.Unmarshal(payload, &c); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidChallenge, "Challenge payload is malformed")
		return
	}

	if h.encryptor == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.NotConfigured, "TOTP is not configured")
		return
	}
	secret, err := h.encryptor.Decrypt(c.Secret)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidChallenge, "Challenge payload could not be decrypted")
		return
	}

	ok2, err := auth.VerifyCode(secret, req.Code)
	if err != nil {
		h.log.Warn("totp verify failed during enroll", "user_id", userID.String(), "error", err)
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidCode, "TOTP code is invalid")
		return
	}
	if !ok2 {
		if !h.auditEvent(w, r, pgtype.UUID{Bytes: userID, Valid: true},
			"auth.totp.verify_failed", userID.String(), authUser.Username, http.StatusBadRequest, map[string]any{
				"flow": "enroll_confirm",
			}) {
			return
		}
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidCode, "TOTP code is invalid")
		return
	}
	// Generate recovery codes. The plaintext set is returned ONCE; only
	// the hashes are stored.
	codes, hashes, err := auth.GenerateRecoveryCodes(auth.RecoveryCodeCount)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.RecoveryFailed, "Failed to generate recovery codes")
		return
	}
	resp := enrollConfirmResponse{RecoveryCodes: codes, Enrolled: true}
	enrollOnly := reqctx.IsTOTPEnrollOnly(r.Context())
	var session auth.PreparedTokenPair
	if enrollOnly {
		session, err = h.jwt.PrepareTokenPairContext(r.Context())
		if err != nil {
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.TokenError, "Failed to prepare session")
			return
		}
	}
	var enrolledUser sqlc.User
	if !h.mutate(w, r, func(q TOTPMutationTx) error {
		var err error
		enrolledUser, err = q.GetUserByIDForUpdate(r.Context(), userID)
		if err != nil {
			return err
		}
		if !enrolledUser.IsActive {
			return errTOTPAccountDisabled
		}
		if enrollOnly && enrolledUser.LockedUntil.Valid && enrolledUser.LockedUntil.Time.After(time.Now()) {
			return errTOTPAccountLocked
		}
		if err := consumeTOTPChallenge(r.Context(), q, claims); err != nil {
			return err
		}
		if err := q.ResetFailedLoginCount(r.Context(), userID); err != nil {
			return err
		}
		if _, err := q.UpsertUserTOTPEnrollment(r.Context(), sqlc.UpsertUserTOTPEnrollmentParams{
			UserID: userID, SecretEncrypted: c.Secret, Label: c.Label, ConfirmedAt: time.Now(),
		}); err != nil {
			return err
		}
		if err := replaceTOTPRecoveryCodes(r.Context(), q, userID, hashes); err != nil {
			return err
		}
		if enrollOnly {
			if err := q.UpdateUserLastLogin(r.Context(), userID); err != nil {
				return err
			}
		}
		return recordAuditOutboxAs(r, q, pgtype.UUID{Bytes: userID, Valid: true},
			"auth.totp.enrolled", "user", userID.String(), authUser.Username, http.StatusOK,
			map[string]any{"recovery_codes_issued": len(codes)})
	}) {
		return
	}
	h.jwt.InvalidateJTI(r.Context(), claims.ID)

	if h.emails != nil && enrolledUser.Email != "" {
		h.emails.EnqueueAndLog(r.Context(), EmailNotifierRequest{
			To: enrolledUser.Email, Template: "totp_enabled", UserID: userID,
			Data: map[string]any{"Username": enrolledUser.Username, "RecoveryCodeCount": len(codes)},
		})
	}

	// Forced-enrollment path: the caller authenticated with the enroll-only
	// challenge and has no session. Now that MFA is enrolled, mint the real
	// session pair and log them in — otherwise they'd complete enrollment and
	// still be stuck at a login wall.
	if enrollOnly {
		resp.Token, resp.Refresh, err = h.jwt.SignPreparedTokenPair(userID, session)
		if err != nil {
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.TokenError, "Failed to generate session")
			return
		}
		setBrowserSessionCookies(w, r, resp.Token, resp.Refresh)
	}
	RespondJSON(w, http.StatusOK, resp)
}

// --- Disable ---

// openapi:request-operation postAuthTotpDisable
type disableRequest struct {
	Password string `json:"password" validate:"required"`
	Code     string `json:"code" validate:"required"`
}

// Disable handles POST /api/v1/auth/totp/disable/.
//
// Body: { password, code }. Requires BOTH the current password AND a
// valid TOTP code — disabling 2FA from a session that's missing one
// factor would defeat the point. Audit emits auth.totp.disabled.
func (h *TOTPHandler) Disable(w http.ResponseWriter, r *http.Request) {
	if !h.requireRunner(w, r) {
		return
	}
	authUser, ok := reqctx.AuthenticatedUser(r.Context())
	if !ok || authUser == nil {
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.AuthenticationRequired, "Authentication required")
		return
	}
	userID, err := uuid.Parse(authUser.ID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Invalid user ID")
		return
	}

	var req disableRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}

	dbUser, err := h.users.GetUserByID(r.Context(), userID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "User not found")
		return
	}
	verified, _, verr := auth.VerifyPassword(dbUser.Password, req.Password)
	if verr != nil || !verified {
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.AuthenticationRequired, "Current password is incorrect")
		return
	}

	enrollment, err := h.queries.GetUserTOTPEnrollment(r.Context(), userID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotEnrolled, "TOTP is not currently enabled for this account")
		return
	}
	plaintextSecret, err := h.encryptor.Decrypt(enrollment.SecretEncrypted)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Stored TOTP secret could not be read")
		return
	}
	ok2, err := auth.VerifyCode(plaintextSecret, req.Code)
	if err != nil || !ok2 {
		if !h.auditEvent(w, r, pgtype.UUID{Bytes: userID, Valid: true},
			"auth.totp.verify_failed", userID.String(), authUser.Username, http.StatusUnauthorized, map[string]any{
				"flow": "disable",
			}) {
			return
		}
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.InvalidCode, "TOTP code is invalid")
		return
	}

	if !h.mutate(w, r, func(q TOTPMutationTx) error {
		current, err := lockTOTPEnrollment(r.Context(), q, userID, enrollment)
		if err != nil {
			return err
		}
		if current.Password != dbUser.Password {
			return errTOTPEnrollmentChanged
		}
		if err := q.DeleteUserTOTPEnrollment(r.Context(), userID); err != nil {
			return err
		}
		if err := q.DeleteRecoveryCodesByUser(r.Context(), userID); err != nil {
			return err
		}
		return recordAuditOutboxAs(r, q, pgtype.UUID{Bytes: userID, Valid: true},
			"auth.totp.disabled", "user", userID.String(), authUser.Username, http.StatusOK, nil)
	}) {
		return
	}

	if h.emails != nil && dbUser.Email != "" {
		h.emails.EnqueueAndLog(r.Context(), EmailNotifierRequest{
			To:       dbUser.Email,
			Template: "totp_disabled",
			Data:     map[string]any{"Username": dbUser.Username},
			UserID:   userID,
		})
	}

	RespondJSONUnwrapped(w, http.StatusOK, map[string]string{"detail": "TOTP disabled"})
}

// --- Status ---

type statusResponse struct {
	Enrolled               bool    `json:"enrolled"`
	LastUsedAt             *string `json:"last_used_at"`
	RecoveryCodesRemaining int64   `json:"recovery_codes_remaining"`
}

// Status handles GET /api/v1/auth/totp/status/. Cheap; the SPA polls
// this on the account-security page to render the "you have 2FA on /
// off" toggle.
func (h *TOTPHandler) Status(w http.ResponseWriter, r *http.Request) {
	authUser, ok := reqctx.AuthenticatedUser(r.Context())
	if !ok || authUser == nil {
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.AuthenticationRequired, "Authentication required")
		return
	}
	userID, err := uuid.Parse(authUser.ID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Invalid user ID")
		return
	}

	enrollment, err := h.queries.GetUserTOTPEnrollment(r.Context(), userID)
	if errors.Is(err, pgx.ErrNoRows) {
		RespondJSON(w, http.StatusOK, statusResponse{Enrolled: false})
		return
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.StatusError, "TOTP status is temporarily unavailable")
		return
	}
	var lastUsed *string
	if enrollment.LastUsedAt.Valid {
		s := enrollment.LastUsedAt.Time.UTC().Format("2006-01-02T15:04:05Z")
		lastUsed = &s
	}
	count, err := h.queries.CountUnusedRecoveryCodes(r.Context(), userID)
	if err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.StatusError, "TOTP status is temporarily unavailable")
		return
	}
	RespondJSON(w, http.StatusOK, statusResponse{
		Enrolled:               true,
		LastUsedAt:             lastUsed,
		RecoveryCodesRemaining: count,
	})
}

// --- Recovery code regeneration ---

// openapi:request-operation postAuthTotpRecoveryCodesRegenerate
type regenerateRequest struct {
	Code string `json:"code" validate:"required"`
}

type regenerateResponse struct {
	RecoveryCodes []string `json:"recovery_codes"`
}

// RegenerateRecoveryCodes handles POST /api/v1/auth/totp/recovery-codes/regenerate/.
// Body: { code }. Requires a fresh TOTP code (NOT a recovery code) to
// prove possession before issuing a new sheet.
func (h *TOTPHandler) RegenerateRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	if !h.requireRunner(w, r) {
		return
	}
	authUser, ok := reqctx.AuthenticatedUser(r.Context())
	if !ok || authUser == nil {
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.AuthenticationRequired, "Authentication required")
		return
	}
	userID, err := uuid.Parse(authUser.ID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Invalid user ID")
		return
	}

	var req regenerateRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}

	enrollment, err := h.queries.GetUserTOTPEnrollment(r.Context(), userID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotEnrolled, "TOTP is not enabled for this account")
		return
	}
	secret, err := h.encryptor.Decrypt(enrollment.SecretEncrypted)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Stored secret could not be read")
		return
	}
	ok2, err := auth.VerifyCode(secret, req.Code)
	if err != nil || !ok2 {
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.InvalidCode, "TOTP code is invalid")
		return
	}

	codes, hashes, err := auth.GenerateRecoveryCodes(auth.RecoveryCodeCount)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.RecoveryFailed, "Failed to generate recovery codes")
		return
	}
	var enrolledUser sqlc.User
	if !h.mutate(w, r, func(q TOTPMutationTx) error {
		var err error
		enrolledUser, err = lockTOTPEnrollment(r.Context(), q, userID, enrollment)
		if err != nil {
			return err
		}
		if err := replaceTOTPRecoveryCodes(r.Context(), q, userID, hashes); err != nil {
			return err
		}
		return recordAuditOutboxAs(r, q, pgtype.UUID{Bytes: userID, Valid: true},
			"auth.totp.recovery_codes_regenerated", "user", userID.String(), authUser.Username,
			http.StatusOK, map[string]any{"recovery_codes_issued": len(codes)})
	}) {
		return
	}

	if h.emails != nil && enrolledUser.Email != "" {
		h.emails.EnqueueAndLog(r.Context(), EmailNotifierRequest{
			To: enrolledUser.Email, Template: "recovery_codes_regenerated", UserID: userID,
			Data: map[string]any{"Username": enrolledUser.Username},
		})
	}

	RespondJSON(w, http.StatusOK, regenerateResponse{RecoveryCodes: codes})
}

// --- Admin force-disable ---

// AdminForceDisable handles POST /api/v1/admin/users/{id}/disable-totp/.
// Superuser-gated inside the handler — for the lost-device case where
// the user can't satisfy Disable's "password + code" requirement.
//
// We read the target user ID from the URL and the actor's superuser
// flag from the request context (via the auth middleware) + a fresh
// DB lookup so the gate can't be spoofed by a stale claim.
func (h *TOTPHandler) AdminForceDisable(w http.ResponseWriter, r *http.Request) {
	if !h.requireRunner(w, r) {
		return
	}
	adminUser, ok := requireSuperuser(w, r, h.users, superuserGateConfig{
		InvalidUserMessage: "Invalid user ID",
		ForbiddenMessage:   "Superuser required",
	})
	if !ok {
		return
	}
	adminID := adminUser.ID

	targetIDStr := chiURLParam(r, "id")
	targetID, err := uuid.Parse(targetIDStr)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid user ID")
		return
	}
	target, err := h.users.GetUserByID(r.Context(), targetID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "User not found")
		return
	}

	if !h.mutate(w, r, func(q TOTPMutationTx) error {
		if _, err := q.GetUserByIDForUpdate(r.Context(), targetID); err != nil {
			return err
		}
		if err := q.DeleteUserTOTPEnrollment(r.Context(), targetID); err != nil {
			return err
		}
		if err := q.DeleteRecoveryCodesByUser(r.Context(), targetID); err != nil {
			return err
		}
		return recordAuditOutboxAs(r, q, pgtype.UUID{Bytes: adminID, Valid: true},
			"admin.user.totp_disabled", "user", target.ID.String(), target.Username, http.StatusOK,
			map[string]any{"actor_username": adminUser.Username})
	}) {
		return
	}

	RespondJSONUnwrapped(w, http.StatusOK, map[string]string{"detail": "TOTP disabled for user"})
}

// --- helpers ---

// encodeTOTPChallenge serialises the per-flow challenge body. We use a
// URL-safe base64 of the raw JSON; the secret inside is already
// encrypted (Fernet) so the encoding here is purely transport.
func encodeTOTPChallenge(payload []byte) string {
	return base64URLEncoding.EncodeToString(payload)
}
func decodeTOTPChallenge(s string) ([]byte, error) {
	return base64URLEncoding.DecodeString(s)
}
