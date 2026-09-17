package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
)

// TOTPMutationTx binds second-factor state, account safeguards, and mandatory
// audit evidence to the same database transaction. Locking the user serializes
// enrollment, verification, and recovery-code replacement for that account.
type TOTPMutationTx interface {
	TOTPQuerier
	audit.OutboxQuerier
	GetUserByIDForUpdate(context.Context, uuid.UUID) (sqlc.User, error)
	UpdateUserLastLogin(context.Context, uuid.UUID) error
	UpsertUserTOTPEnrollment(ctx context.Context, arg sqlc.UpsertUserTOTPEnrollmentParams) (sqlc.UserTotpEnrollment, error)
	DeleteUserTOTPEnrollment(ctx context.Context, userID uuid.UUID) error
	TouchUserTOTPLastUsed(ctx context.Context, arg sqlc.TouchUserTOTPLastUsedParams) error
	InsertRecoveryCode(ctx context.Context, arg sqlc.InsertRecoveryCodeParams) error
	ConsumeRecoveryCode(ctx context.Context, arg sqlc.ConsumeRecoveryCodeParams) (int64, error)
	DeleteRecoveryCodesByUser(ctx context.Context, userID uuid.UUID) error
	RecordFailedLoginAttempt(ctx context.Context, arg sqlc.RecordFailedLoginAttemptParams) (sqlc.User, error)
	ResetFailedLoginCount(ctx context.Context, id uuid.UUID) error
	ConsumeJWTChallenge(ctx context.Context, arg sqlc.ConsumeJWTChallengeParams) (int64, error)
	CreateRefreshSession(context.Context, sqlc.CreateRefreshSessionParams) error
}

type totpRunTxFunc func(context.Context, func(TOTPMutationTx) error) error

var _ TOTPMutationTx = (*sqlc.Queries)(nil)

var errTOTPChallengeUsed = errors.New("TOTP challenge has already been used")
var errTOTPEnrollmentChanged = errors.New("TOTP enrollment changed during verification")
var errTOTPAccountDisabled = errors.New("TOTP account is disabled")
var errTOTPAccountLocked = errors.New("TOTP account is locked")

// SetRunTx supplies the mandatory transaction runner for TOTP mutations.
func (h *TOTPHandler) SetRunTx(fn func(context.Context, func(TOTPMutationTx) error) error) {
	h.runTx = fn
}

func (h *TOTPHandler) TransactionalAuditWired() bool { return h != nil && h.runTx != nil }

func (h *TOTPHandler) requireRunner(w http.ResponseWriter, r *http.Request) bool {
	if h.runTx == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.RunnerUnwired, "TOTP transaction runner is not configured")
		return false
	}
	return true
}

func (h *TOTPHandler) mutate(w http.ResponseWriter, r *http.Request, fn func(TOTPMutationTx) error) bool {
	if !h.requireRunner(w, r) {
		return false
	}
	if err := h.runTx(r.Context(), fn); err != nil {
		if errors.Is(err, errTOTPChallengeUsed) {
			RespondRequestError(w, r, http.StatusUnauthorized, apierror.InvalidChallenge, "Challenge token has already been used")
		} else if errors.Is(err, errTOTPEnrollmentChanged) {
			RespondRequestError(w, r, http.StatusUnauthorized, apierror.InvalidChallenge, "TOTP enrollment changed; restart authentication")
		} else if errors.Is(err, errTOTPAccountDisabled) {
			RespondRequestError(w, r, http.StatusForbidden, apierror.AccountDisabled, "Account is disabled")
		} else if errors.Is(err, errTOTPAccountLocked) {
			RespondRequestError(w, r, http.StatusLocked, apierror.AccountLocked, "Account is temporarily locked")
		} else {
			h.log.Error("TOTP mutation failed", "error", err)
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.StatusError, "Authentication safeguards are temporarily unavailable")
		}
		return false
	}
	return true
}

func (h *TOTPHandler) auditEvent(w http.ResponseWriter, r *http.Request, actor pgtype.UUID, action, resourceID, name string, status int, detail map[string]any) bool {
	return h.mutate(w, r, func(q TOTPMutationTx) error {
		return recordAuditOutboxAs(r, q, actor, action, "user", resourceID, name, status, detail)
	})
}

func replaceTOTPRecoveryCodes(ctx context.Context, q TOTPMutationTx, userID uuid.UUID, hashes []string) error {
	if err := q.DeleteRecoveryCodesByUser(ctx, userID); err != nil {
		return err
	}
	for _, hash := range hashes {
		if err := q.InsertRecoveryCode(ctx, sqlc.InsertRecoveryCodeParams{UserID: userID, CodeHash: hash}); err != nil {
			return err
		}
	}
	return nil
}

func lockTOTPEnrollment(ctx context.Context, q TOTPMutationTx, userID uuid.UUID, expected sqlc.UserTotpEnrollment) (sqlc.User, error) {
	user, err := q.GetUserByIDForUpdate(ctx, userID)
	if err != nil {
		return sqlc.User{}, err
	}
	current, err := q.GetUserTOTPEnrollment(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.User{}, errTOTPEnrollmentChanged
	}
	if err != nil {
		return sqlc.User{}, err
	}
	if current.SecretEncrypted != expected.SecretEncrypted {
		return sqlc.User{}, errTOTPEnrollmentChanged
	}
	return user, nil
}
