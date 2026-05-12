package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
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
	UpsertUserTOTPEnrollment(ctx context.Context, arg sqlc.UpsertUserTOTPEnrollmentParams) (sqlc.UserTotpEnrollment, error)
	DeleteUserTOTPEnrollment(ctx context.Context, userID uuid.UUID) error
	TouchUserTOTPLastUsed(ctx context.Context, arg sqlc.TouchUserTOTPLastUsedParams) error
	InsertRecoveryCode(ctx context.Context, arg sqlc.InsertRecoveryCodeParams) error
	ListUnusedRecoveryCodes(ctx context.Context, userID uuid.UUID) ([]sqlc.UserTotpRecoveryCode, error)
	CountUnusedRecoveryCodes(ctx context.Context, userID uuid.UUID) (int64, error)
	ConsumeRecoveryCode(ctx context.Context, arg sqlc.ConsumeRecoveryCodeParams) (int64, error)
	DeleteRecoveryCodesByUser(ctx context.Context, userID uuid.UUID) error
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
	audit      AuthAuditWriter
	log        *slog.Logger
	issuer     string
	requireAll bool
	emails     EmailNotifier
}

// SetEmailNotifier attaches the email-enqueue hook used by the
// enroll-confirm / disable / regenerate paths to fire security-
// relevant FYI emails.
func (h *TOTPHandler) SetEmailNotifier(n EmailNotifier) { h.emails = n }

// NewTOTPHandler wires the TOTP handler. queries / users / encryptor /
// jwt are required at construction; the optional dependencies (audit,
// logger, issuer, require-all) are set via dedicated setters so the
// existing test fakes can leave them off.
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

// SetAuditWriter attaches the audit-log writer. Optional.
func (h *TOTPHandler) SetAuditWriter(a AuthAuditWriter) { h.audit = a }

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

// IsEnrolled is a convenience for Login (and tests) — returns true if
// the user has a confirmed enrollment row. Errors are treated as
// "not enrolled" (the caller never wants a DB hiccup to grant a free
// login).
func (h *TOTPHandler) IsEnrolled(ctx context.Context, userID uuid.UUID) bool {
	if h == nil || h.queries == nil {
		return false
	}
	_, err := h.queries.GetUserTOTPEnrollment(ctx, userID)
	return err == nil
}

// --- Enrollment: start ---

// enrollStartResponse is the body returned to the browser. otpauth_url
// is the URL embedded in the QR; qr_data_url is a self-contained
// data: image so the SPA can render the QR without an extra fetch.
// challenge_token carries the pending secret in a 5-minute signed
// JWT — the secret itself is NOT yet persisted, so a user who closes
// the tab mid-enrollment leaves no DB trace.
type enrollStartResponse struct {
	OTPAuthURL     string `json:"otpauth_url"`
	QRCodeDataURL  string `json:"qr_data_url"`
	ChallengeToken string `json:"challenge_token"`
	Issuer         string `json:"issuer"`
}

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
	authUser, ok := middleware.GetAuthenticatedUser(r.Context())
	if !ok || authUser == nil {
		RespondError(w, http.StatusUnauthorized, "authentication_required", "Authentication required")
		return
	}
	userID, err := uuid.Parse(authUser.ID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "internal_error", "Invalid user ID")
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
		RespondError(w, http.StatusInternalServerError, "totp_generate_failed", "Failed to generate TOTP secret")
		return
	}
	qrDataURL, err := auth.QRCodeDataURL(url)
	if err != nil {
		h.log.Warn("totp generate qr failed", "user_id", userID.String(), "error", err)
		RespondError(w, http.StatusInternalServerError, "totp_generate_failed", "Failed to render QR code")
		return
	}

	// Encrypt the secret BEFORE stuffing it in the challenge JWT — the
	// JWT body is base64 (not encryption) so a leaked challenge token
	// would otherwise expose the secret to anyone watching the wire.
	// With Fernet wrapping, only this server (or its rotation peers)
	// can read it back during the confirm step.
	if h.encryptor == nil {
		RespondError(w, http.StatusServiceUnavailable, "not_configured", "TOTP is not configured")
		return
	}
	encryptedSecret, err := h.encryptor.Encrypt(secret)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "totp_encrypt_failed", "Failed to wrap TOTP secret")
		return
	}

	// Sign the challenge body into a 5-minute JWT. Purpose claim keeps
	// the regular auth middleware from accepting this as a session.
	payload, err := json.Marshal(enrollChallengeClaims{Secret: encryptedSecret, Label: account})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "internal_error", "Failed to marshal challenge")
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
		RespondError(w, http.StatusInternalServerError, "token_error", "Failed to mint challenge token")
		return
	}

	// Audit (no secret, only the user_id + that we issued a challenge).
	recordAuditAs(r, h.audit, pgtype.UUID{Bytes: userID, Valid: true},
		"auth.totp.enroll_started", "user", userID.String(), authUser.Username, nil)

	RespondJSON(w, http.StatusOK, map[string]any{
		"otpauth_url":     url,
		"qr_data_url":     qrDataURL,
		"challenge_token": token,
		"challenge":       challenge, // opaque encrypted-secret blob
		"issuer":          h.issuer,
	})
}

// --- Enrollment: confirm ---

type enrollConfirmRequest struct {
	ChallengeToken string `json:"challenge_token"`
	Challenge      string `json:"challenge"`
	Code           string `json:"code"`
}

type enrollConfirmResponse struct {
	RecoveryCodes []string `json:"recovery_codes"`
	Enrolled      bool     `json:"enrolled"`
}

// EnrollConfirm handles POST /api/v1/auth/totp/enroll/confirm/.
//
// Body: { challenge_token, challenge, code }. Validates the challenge
// token (signature, expiry, purpose), decrypts the secret, verifies
// the user-supplied 6-digit code, persists the enrollment row, and
// generates+returns 10 recovery codes (shown ONCE).
func (h *TOTPHandler) EnrollConfirm(w http.ResponseWriter, r *http.Request) {
	authUser, ok := middleware.GetAuthenticatedUser(r.Context())
	if !ok || authUser == nil {
		RespondError(w, http.StatusUnauthorized, "authentication_required", "Authentication required")
		return
	}
	userID, err := uuid.Parse(authUser.ID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "internal_error", "Invalid user ID")
		return
	}

	var req enrollConfirmRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}
	if req.ChallengeToken == "" || req.Challenge == "" || req.Code == "" {
		RespondError(w, http.StatusBadRequest, "validation_error", "challenge_token, challenge and code are required")
		return
	}

	claims, err := h.jwt.ValidateToken(req.ChallengeToken)
	if err != nil || claims.TokenType != auth.PurposeToken || claims.Purpose != auth.PurposeTOTPChallenge {
		RespondError(w, http.StatusUnauthorized, "invalid_challenge", "Challenge token is invalid or expired")
		return
	}
	if claims.UserID != userID {
		// The challenge MUST belong to the same user that's authenticated.
		// Otherwise a holder of a leaked challenge could enroll on behalf
		// of someone else.
		RespondError(w, http.StatusUnauthorized, "invalid_challenge", "Challenge token does not match the authenticated user")
		return
	}

	payload, err := decodeTOTPChallenge(req.Challenge)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_challenge", "Challenge payload is malformed")
		return
	}
	var c enrollChallengeClaims
	if err := json.Unmarshal(payload, &c); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_challenge", "Challenge payload is malformed")
		return
	}

	if h.encryptor == nil {
		RespondError(w, http.StatusServiceUnavailable, "not_configured", "TOTP is not configured")
		return
	}
	secret, err := h.encryptor.Decrypt(c.Secret)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_challenge", "Challenge payload could not be decrypted")
		return
	}

	ok2, err := auth.VerifyCode(secret, req.Code)
	if err != nil {
		h.log.Warn("totp verify failed during enroll", "user_id", userID.String(), "error", err)
		RespondError(w, http.StatusBadRequest, "invalid_code", "TOTP code is invalid")
		return
	}
	if !ok2 {
		recordAuditAs(r, h.audit, pgtype.UUID{Bytes: userID, Valid: true},
			"auth.totp.verify_failed", "user", userID.String(), authUser.Username, map[string]any{
				"flow": "enroll_confirm",
			})
		RespondError(w, http.StatusBadRequest, "invalid_code", "TOTP code is invalid")
		return
	}

	// Persist the encrypted secret. The plaintext goes out of scope on
	// return from this function — never log it.
	_, err = h.queries.UpsertUserTOTPEnrollment(r.Context(), sqlc.UpsertUserTOTPEnrollmentParams{
		UserID:          userID,
		SecretEncrypted: c.Secret,
		Label:           c.Label,
		ConfirmedAt:     time.Now(),
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "persist_failed", "Failed to persist enrollment")
		return
	}

	// Generate recovery codes. The plaintext set is returned ONCE; only
	// the hashes are stored.
	codes, hashes, err := auth.GenerateRecoveryCodes(auth.RecoveryCodeCount)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "recovery_failed", "Failed to generate recovery codes")
		return
	}
	// Wipe any pre-existing codes (re-enroll path) before inserting new ones.
	_ = h.queries.DeleteRecoveryCodesByUser(r.Context(), userID)
	for _, hashed := range hashes {
		if err := h.queries.InsertRecoveryCode(r.Context(), sqlc.InsertRecoveryCodeParams{
			UserID:   userID,
			CodeHash: hashed,
		}); err != nil {
			// Inserting the same hash twice is the only realistic failure
			// here (uidx_totp_recovery_hash). Log + continue; the user
			// will simply have fewer codes than expected.
			h.log.Warn("totp insert recovery code failed", "user_id", userID.String(), "error", err)
		}
	}

	recordAuditAs(r, h.audit, pgtype.UUID{Bytes: userID, Valid: true},
		"auth.totp.enrolled", "user", userID.String(), authUser.Username, map[string]any{
			"recovery_codes_issued": len(codes),
		})

	if h.emails != nil {
		if u, err := h.users.GetUserByID(r.Context(), userID); err == nil && u.Email != "" {
			h.emails.EnqueueAndLog(r.Context(), EmailNotifierRequest{
				To:       u.Email,
				Template: "totp_enabled",
				Data: map[string]any{
					"Username":          u.Username,
					"RecoveryCodeCount": len(codes),
				},
				UserID: userID,
			})
		}
	}

	RespondJSON(w, http.StatusOK, enrollConfirmResponse{RecoveryCodes: codes, Enrolled: true})
}

// --- Disable ---

type disableRequest struct {
	Password string `json:"password"`
	Code     string `json:"code"`
}

// Disable handles POST /api/v1/auth/totp/disable/.
//
// Body: { password, code }. Requires BOTH the current password AND a
// valid TOTP code — disabling 2FA from a session that's missing one
// factor would defeat the point. Audit emits auth.totp.disabled.
func (h *TOTPHandler) Disable(w http.ResponseWriter, r *http.Request) {
	authUser, ok := middleware.GetAuthenticatedUser(r.Context())
	if !ok || authUser == nil {
		RespondError(w, http.StatusUnauthorized, "authentication_required", "Authentication required")
		return
	}
	userID, err := uuid.Parse(authUser.ID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "internal_error", "Invalid user ID")
		return
	}

	var req disableRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}
	if req.Password == "" || req.Code == "" {
		RespondError(w, http.StatusBadRequest, "validation_error", "password and code are required")
		return
	}

	dbUser, err := h.users.GetUserByID(r.Context(), userID)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "User not found")
		return
	}
	verified, _, verr := auth.VerifyPassword(dbUser.Password, req.Password)
	if verr != nil || !verified {
		RespondError(w, http.StatusUnauthorized, "invalid_credentials", "Current password is incorrect")
		return
	}

	enrollment, err := h.queries.GetUserTOTPEnrollment(r.Context(), userID)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_enrolled", "TOTP is not currently enabled for this account")
		return
	}
	plaintextSecret, err := h.encryptor.Decrypt(enrollment.SecretEncrypted)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "internal_error", "Stored TOTP secret could not be read")
		return
	}
	ok2, err := auth.VerifyCode(plaintextSecret, req.Code)
	if err != nil || !ok2 {
		recordAuditAs(r, h.audit, pgtype.UUID{Bytes: userID, Valid: true},
			"auth.totp.verify_failed", "user", userID.String(), authUser.Username, map[string]any{
				"flow": "disable",
			})
		RespondError(w, http.StatusUnauthorized, "invalid_code", "TOTP code is invalid")
		return
	}

	if err := h.queries.DeleteUserTOTPEnrollment(r.Context(), userID); err != nil {
		RespondError(w, http.StatusInternalServerError, "persist_failed", "Failed to disable 2FA")
		return
	}
	// Best-effort: wipe the recovery codes too. Leaving them behind on
	// disable would leak partial-2FA bypass capability if 2FA is
	// re-enabled later with a fresh device.
	_ = h.queries.DeleteRecoveryCodesByUser(r.Context(), userID)

	recordAuditAs(r, h.audit, pgtype.UUID{Bytes: userID, Valid: true},
		"auth.totp.disabled", "user", userID.String(), authUser.Username, nil)

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
	authUser, ok := middleware.GetAuthenticatedUser(r.Context())
	if !ok || authUser == nil {
		RespondError(w, http.StatusUnauthorized, "authentication_required", "Authentication required")
		return
	}
	userID, err := uuid.Parse(authUser.ID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "internal_error", "Invalid user ID")
		return
	}

	enrollment, err := h.queries.GetUserTOTPEnrollment(r.Context(), userID)
	if err != nil {
		RespondJSON(w, http.StatusOK, statusResponse{Enrolled: false})
		return
	}
	var lastUsed *string
	if enrollment.LastUsedAt.Valid {
		s := enrollment.LastUsedAt.Time.UTC().Format("2006-01-02T15:04:05Z")
		lastUsed = &s
	}
	count, _ := h.queries.CountUnusedRecoveryCodes(r.Context(), userID)
	RespondJSON(w, http.StatusOK, statusResponse{
		Enrolled:               true,
		LastUsedAt:             lastUsed,
		RecoveryCodesRemaining: count,
	})
}

// --- Recovery code regeneration ---

type regenerateRequest struct {
	Code string `json:"code"`
}

type regenerateResponse struct {
	RecoveryCodes []string `json:"recovery_codes"`
}

// RegenerateRecoveryCodes handles POST /api/v1/auth/totp/recovery-codes/regenerate/.
// Body: { code }. Requires a fresh TOTP code (NOT a recovery code) to
// prove possession before issuing a new sheet.
func (h *TOTPHandler) RegenerateRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	authUser, ok := middleware.GetAuthenticatedUser(r.Context())
	if !ok || authUser == nil {
		RespondError(w, http.StatusUnauthorized, "authentication_required", "Authentication required")
		return
	}
	userID, err := uuid.Parse(authUser.ID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "internal_error", "Invalid user ID")
		return
	}

	var req regenerateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}
	if req.Code == "" {
		RespondError(w, http.StatusBadRequest, "validation_error", "code is required")
		return
	}

	enrollment, err := h.queries.GetUserTOTPEnrollment(r.Context(), userID)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_enrolled", "TOTP is not enabled for this account")
		return
	}
	secret, err := h.encryptor.Decrypt(enrollment.SecretEncrypted)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "internal_error", "Stored secret could not be read")
		return
	}
	ok2, err := auth.VerifyCode(secret, req.Code)
	if err != nil || !ok2 {
		RespondError(w, http.StatusUnauthorized, "invalid_code", "TOTP code is invalid")
		return
	}

	if err := h.queries.DeleteRecoveryCodesByUser(r.Context(), userID); err != nil {
		RespondError(w, http.StatusInternalServerError, "persist_failed", "Failed to clear recovery codes")
		return
	}
	codes, hashes, err := auth.GenerateRecoveryCodes(auth.RecoveryCodeCount)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "recovery_failed", "Failed to generate recovery codes")
		return
	}
	for _, hashed := range hashes {
		_ = h.queries.InsertRecoveryCode(r.Context(), sqlc.InsertRecoveryCodeParams{
			UserID:   userID,
			CodeHash: hashed,
		})
	}

	recordAuditAs(r, h.audit, pgtype.UUID{Bytes: userID, Valid: true},
		"auth.totp.recovery_codes_regenerated", "user", userID.String(), authUser.Username, map[string]any{
			"recovery_codes_issued": len(codes),
		})

	if h.emails != nil {
		if u, err := h.users.GetUserByID(r.Context(), userID); err == nil && u.Email != "" {
			h.emails.EnqueueAndLog(r.Context(), EmailNotifierRequest{
				To:       u.Email,
				Template: "recovery_codes_regenerated",
				Data:     map[string]any{"Username": u.Username},
				UserID:   userID,
			})
		}
	}

	RespondJSON(w, http.StatusOK, regenerateResponse{RecoveryCodes: codes})
}

// --- Verify (challenge -> session JWT) ---

type verifyRequest struct {
	ChallengeToken string `json:"challenge_token"`
	Code           string `json:"code"`
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
	var req verifyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}
	if req.ChallengeToken == "" || req.Code == "" {
		RespondError(w, http.StatusBadRequest, "validation_error", "challenge_token and code are required")
		return
	}
	claims, err := h.jwt.ValidateToken(req.ChallengeToken)
	if err != nil {
		recordAuditAs(r, h.audit, pgtype.UUID{}, "auth.totp.verify_failed", "user", "", "", map[string]any{
			"reason": "invalid_challenge",
		})
		RespondError(w, http.StatusUnauthorized, "invalid_challenge", "Challenge token is invalid or expired")
		return
	}
	if claims.TokenType != auth.PurposeToken || claims.Purpose != auth.PurposeTOTPChallenge {
		recordAuditAs(r, h.audit, pgtype.UUID{Bytes: claims.UserID, Valid: true}, "auth.totp.verify_failed", "user", claims.UserID.String(), "", map[string]any{
			"reason": "wrong_purpose",
		})
		RespondError(w, http.StatusUnauthorized, "invalid_challenge", "Challenge token is not a TOTP challenge")
		return
	}
	userID := claims.UserID

	user, err := h.users.GetUserByID(r.Context(), userID)
	if err != nil || !user.IsActive {
		RespondError(w, http.StatusUnauthorized, "invalid_credentials", "Invalid credentials")
		return
	}

	enrollment, err := h.queries.GetUserTOTPEnrollment(r.Context(), userID)
	if err != nil {
		// Race: user disabled TOTP after the challenge was issued.
		// Reject — the client should restart the login.
		RespondError(w, http.StatusUnauthorized, "not_enrolled", "TOTP is not enabled for this account")
		return
	}
	secret, err := h.encryptor.Decrypt(enrollment.SecretEncrypted)
	if err != nil {
		h.log.Warn("totp decrypt failed", "user_id", userID.String(), "error", err)
		RespondError(w, http.StatusInternalServerError, "internal_error", "Stored secret could not be read")
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
	if !verified {
		// Either the user explicitly chose recovery, or the TOTP code
		// failed and we get a free second-chance check against a
		// recovery code (avoids forcing the user to click an extra
		// "use recovery code" link first).
		hash := auth.HashRecoveryCode(req.Code)
		rows, cErr := h.queries.ConsumeRecoveryCode(r.Context(), sqlc.ConsumeRecoveryCodeParams{
			UserID:   userID,
			CodeHash: hash,
			UsedAt:   pgtype.Timestamptz{Time: time.Now(), Valid: true},
		})
		if cErr == nil && rows > 0 {
			verified = true
			usedRecovery = true
		}
	}

	if !verified {
		recordAuditAs(r, h.audit, pgtype.UUID{Bytes: userID, Valid: true},
			"auth.totp.verify_failed", "user", userID.String(), user.Username, map[string]any{
				"reason": "bad_code",
			})
		// The TOTP failure path reuses the same lockout counter as
		// bcrypt so a user that's been guessing 6-digit codes gets
		// locked out the same way as one guessing passwords. This is
		// wired via the AuthHandler — the public route doesn't know
		// the lockout querier; we surface a simple 401 here and let
		// the upstream metric pick up the failure.
		auth.TOTPVerifiesTotal.WithLabelValues(observability.MetricValues("failed")...).Inc()
		RespondError(w, http.StatusUnauthorized, "invalid_credentials", "Invalid TOTP or recovery code")
		return
	}

	// Successful verify — mint the real session pair and (best-effort)
	// touch last_used_at.
	accessToken, refreshToken, err := h.jwt.GenerateTokenPair(userID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "token_error", "Failed to generate token")
		return
	}
	_ = h.queries.TouchUserTOTPLastUsed(r.Context(), sqlc.TouchUserTOTPLastUsedParams{
		UserID:     userID,
		LastUsedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	_ = h.users.UpdateUserLastLogin(r.Context(), userID)

	action := "auth.totp.verified"
	outcome := "success"
	if usedRecovery {
		action = "auth.totp.recovery_code_consumed"
		outcome = "recovery"
	}
	recordAuditAs(r, h.audit, pgtype.UUID{Bytes: userID, Valid: true},
		action, "user", userID.String(), user.Username, nil)
	auth.TOTPVerifiesTotal.WithLabelValues(observability.MetricValues(outcome)...).Inc()

	RespondJSON(w, http.StatusOK, LoginResponse{
		Token:   accessToken,
		Refresh: refreshToken,
		User:    userToResponse(user),
	})
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
	authUser, ok := middleware.GetAuthenticatedUser(r.Context())
	if !ok || authUser == nil {
		RespondError(w, http.StatusUnauthorized, "authentication_required", "Authentication required")
		return
	}
	adminID, err := uuid.Parse(authUser.ID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "internal_error", "Invalid user ID")
		return
	}
	// Superuser gate via fresh row read. Match the pattern used by
	// UnlockUser / ForceLogoutUser elsewhere in this package so any
	// future tightening (e.g. require staff + superuser) lands in
	// one place.
	adminUser, err := h.users.GetUserByID(r.Context(), adminID)
	if err != nil || !adminUser.IsSuperuser {
		RespondError(w, http.StatusForbidden, "forbidden", "Superuser required")
		return
	}

	targetIDStr := chiURLParam(r, "id")
	targetID, err := uuid.Parse(targetIDStr)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid user ID")
		return
	}
	target, err := h.users.GetUserByID(r.Context(), targetID)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "User not found")
		return
	}

	if err := h.queries.DeleteUserTOTPEnrollment(r.Context(), targetID); err != nil {
		RespondError(w, http.StatusInternalServerError, "persist_failed", "Failed to disable 2FA")
		return
	}
	_ = h.queries.DeleteRecoveryCodesByUser(r.Context(), targetID)

	recordAuditAs(r, h.audit, pgtype.UUID{Bytes: adminID, Valid: true},
		"admin.user.totp_disabled", "user", target.ID.String(), target.Username, map[string]any{
			"actor_username": adminUser.Username,
		})

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

