-- TOTP / 2FA queries (migration 043). Drives the per-user enrollment
-- table + recovery-code table behind /api/v1/auth/totp/*.
--
-- Secrets are stored encrypted (callers Encrypt before INSERT and
-- Decrypt after SELECT); recovery codes are stored as hex(sha256(code)).

-- name: GetUserTOTPEnrollment :one
-- Read the (encrypted) enrollment row for a user. Returns ErrNoRows
-- when the user has not enrolled.
SELECT * FROM user_totp_enrollments WHERE user_id = $1;

-- name: UpsertUserTOTPEnrollment :one
-- Persists a freshly-confirmed enrollment. Caller passes the Fernet-
-- encrypted secret in $2 — the plaintext must NEVER reach this query.
-- ON CONFLICT lets the user re-enroll (lost device, new authenticator)
-- without first calling DeleteUserTOTPEnrollment. confirmed_at is
-- replaced on the conflict path so the audit detail is accurate.
INSERT INTO user_totp_enrollments (user_id, secret_encrypted, label, confirmed_at)
VALUES ($1, $2, $3, $4)
ON CONFLICT (user_id) DO UPDATE SET
    secret_encrypted = EXCLUDED.secret_encrypted,
    label            = EXCLUDED.label,
    confirmed_at     = EXCLUDED.confirmed_at,
    last_used_at     = NULL,
    updated_at       = now()
RETURNING *;

-- name: DeleteUserTOTPEnrollment :exec
-- Disables 2FA. The handler that wraps this query ALSO deletes the
-- recovery codes so a lost-device admin force-disable doesn't leave
-- exploitable codes behind.
DELETE FROM user_totp_enrollments WHERE user_id = $1;

-- name: TouchUserTOTPLastUsed :exec
-- Best-effort timestamp update after a successful verify. Audit + the
-- /status endpoint surface this so users can spot "I never logged in
-- yesterday" anomalies.
UPDATE user_totp_enrollments
SET last_used_at = $2,
    updated_at   = now()
WHERE user_id = $1;

-- name: InsertRecoveryCode :exec
-- Stores ONE hashed recovery code. Called 10 times in a row at
-- enrollment / regeneration; we keep it as a single-row INSERT for
-- simplicity (10 round-trips on a flow the user only runs every few
-- months).
INSERT INTO user_totp_recovery_codes (user_id, code_hash)
VALUES ($1, $2);

-- name: ListUnusedRecoveryCodes :many
-- Drives the "N codes remaining" indicator on the account page. Used
-- by the audit summary too.
SELECT * FROM user_totp_recovery_codes
WHERE user_id = $1 AND used_at IS NULL
ORDER BY created_at ASC;

-- name: CountUnusedRecoveryCodes :one
-- Lightweight count for the /status endpoint — avoids hauling the
-- whole list back when we only need the integer.
SELECT count(*) FROM user_totp_recovery_codes
WHERE user_id = $1 AND used_at IS NULL;

-- name: ConsumeRecoveryCode :execrows
-- Atomically marks a recovery code as used. Returns the row-count so
-- the caller can distinguish "valid + just consumed" (1) from "invalid
-- or already used" (0) without a separate SELECT. Using execrows
-- keeps the verify path race-free: a concurrent login attempt that
-- sees used_at IS NULL will have its UPDATE no-op.
UPDATE user_totp_recovery_codes
SET used_at = $3
WHERE user_id = $1 AND code_hash = $2 AND used_at IS NULL;

-- name: DeleteRecoveryCodesByUser :exec
-- Called by both the disable-2FA path and the regenerate-codes path.
-- Wipes every code (used or not) so a regen invalidates the old sheet
-- entirely, not just the unused entries.
DELETE FROM user_totp_recovery_codes WHERE user_id = $1;
