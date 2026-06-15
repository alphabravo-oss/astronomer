-- SMTP + email-message queries (migration 047). Backs:
--
--   * the admin /api/v1/admin/smtp/* endpoints (Get/Upsert/Test) and
--     /api/v1/admin/emails/ audit view
--   * the email:dispatch worker that drains queued/failed rows into
--     real SMTP sends
--   * the email:cleanup_old retention task
--   * the password-reset request/complete flow (token table)
--
-- The smtp_settings row is a singleton — every read/write targets the
-- same well-known id. The application code (NOT this query layer) is
-- responsible for Fernet-encrypting/decrypting password_encrypted; we
-- store and return the ciphertext verbatim.

-- name: GetSMTPSettings :one
-- Returns the singleton settings row. Callers handle the no-rows case
-- by treating an absent row as "disabled with all defaults".
SELECT * FROM smtp_settings WHERE id = $1;

-- name: UpsertSMTPSettings :one
-- Singleton write. The handler always passes the same well-known id;
-- ON CONFLICT lets the first PUT create the row and every subsequent
-- one update it without a separate INSERT/UPDATE branch.
INSERT INTO smtp_settings (
    id, enabled, host, port, username, password_encrypted,
    from_address, from_name, auth_mechanism, encryption,
    require_tls, timeout_seconds
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
ON CONFLICT (id) DO UPDATE SET
    enabled             = EXCLUDED.enabled,
    host                = EXCLUDED.host,
    port                = EXCLUDED.port,
    username            = EXCLUDED.username,
    password_encrypted  = EXCLUDED.password_encrypted,
    from_address        = EXCLUDED.from_address,
    from_name           = EXCLUDED.from_name,
    auth_mechanism      = EXCLUDED.auth_mechanism,
    encryption          = EXCLUDED.encryption,
    require_tls         = EXCLUDED.require_tls,
    timeout_seconds     = EXCLUDED.timeout_seconds,
    updated_at          = now()
RETURNING *;

-- name: InsertEmailMessage :one
-- Persists a "we want to send this" record. The handler that wraps a
-- user-facing event (lockout, totp-enabled, alert-fired, ...) always
-- writes a row BEFORE the SMTP attempt — even when SMTP is disabled,
-- which writes the row with status='skipped' so operators can spot the
-- gap in the admin view.
INSERT INTO email_messages (
    to_address, cc_address, subject, template,
    body_text, body_html, user_id, status, last_error
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: ListQueuedEmails :many
-- Dispatcher worker batch read. Returns rows the worker should attempt
-- this tick: brand-new queued rows and previously-failed rows whose
-- attempt count is still under the retry budget. ORDER BY created_at
-- so the dispatch is FIFO even after failures bubble rows back to the
-- front of the queue.
SELECT * FROM email_messages
WHERE (status = 'queued')
   OR (status = 'failed' AND attempts < 3)
ORDER BY created_at ASC
LIMIT $1;

-- name: ListEmailMessages :many
-- Admin audit view. Paginated, newest-first. The handler redacts the
-- body_text/body_html before returning the rows so a sensitive reset
-- link or recovery-code email doesn't appear in the admin UI.
SELECT * FROM email_messages
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: CountEmailMessages :one
SELECT count(*) FROM email_messages;

-- name: MarkEmailSent :exec
-- Final-state UPDATE on a successful send. attempts is incremented in
-- the same UPDATE so the sent rows reflect the actual try count.
UPDATE email_messages
SET status   = 'sent',
    sent_at  = $2,
    attempts = attempts + 1,
    updated_at = now(),
    last_error = ''
WHERE id = $1;

-- name: MarkEmailFailed :exec
-- Records a delivery failure. attempts is the NEW count (caller computes
-- prev+1) so the dispatcher can decide whether to mark the row 'failed'
-- (still retryable) or escalate to a final state. last_error is kept
-- short by the caller; the column is TEXT so we don't truncate here.
UPDATE email_messages
SET status   = $2,
    attempts = $3,
    last_error = $4,
    updated_at = now()
WHERE id = $1;

-- name: MarkEmailSkipped :exec
-- Used by the dispatcher to age out queued rows that have been sitting
-- around for more than an hour with SMTP still disabled. Without this,
-- a disabled-SMTP deployment would accumulate queued rows forever.
UPDATE email_messages
SET status     = 'skipped',
    last_error = $2,
    updated_at = now()
WHERE id = $1;

-- name: DeleteEmailsOlderThan :execrows
-- Retention sweep, runs daily. Returns the row count so the task can
-- emit a "rows deleted" log line for the operator.
DELETE FROM email_messages WHERE created_at < $1;

-- ----- Password reset tokens -----

-- name: CreatePasswordResetToken :one
-- Issues a new reset token. The handler caller hashes the plaintext
-- token (returned to the user via email) before calling this; we never
-- see the plaintext. password_hash_at_issue snapshots the user's
-- current password hash so a successful password change invalidates
-- every outstanding link.
INSERT INTO password_reset_tokens (user_id, token_hash, password_hash_at_issue, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetPasswordResetTokenByHash :one
SELECT * FROM password_reset_tokens WHERE token_hash = $1;

-- name: ConsumePasswordResetToken :execrows
-- Atomically marks a reset token as used. Returns row-count so the
-- caller can distinguish "first use" (1) from "already used" (0)
-- without a separate SELECT. The handler also verifies expires_at +
-- password-hash match BEFORE this call — the predicate here is just
-- the race guard.
UPDATE password_reset_tokens
SET used_at = $2
WHERE token_hash = $1 AND used_at IS NULL;

-- name: DeletePasswordResetTokensForUser :exec
-- Wipes every outstanding reset token for a user. Called after a
-- successful reset so the consumed token's siblings can't be replayed,
-- and on user delete (CASCADE handles that one but this exists for
-- explicit "force expire all links" flows).
DELETE FROM password_reset_tokens WHERE user_id = $1;

-- name: DeleteExpiredPasswordResetTokens :execrows
-- Daily retention sweep companion to email retention. Returns the
-- row count for the log line.
DELETE FROM password_reset_tokens WHERE expires_at < $1;
