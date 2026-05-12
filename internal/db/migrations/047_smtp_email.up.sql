-- SMTP email delivery + email message audit log (migration 047).
--
-- Rancher uses SMTP for password-reset, lockout, and alert-fired emails;
-- astronomer-go has no email path today. This migration adds:
--
--   1. smtp_settings — singleton (id = 00000000-0000-0000-0000-000000000001)
--      operator-managed row holding the SMTP host / port / auth / TLS knobs
--      and the FROM identity. The SMTP password is Fernet-encrypted under
--      auth.Encryptor (same key set + rotation procedure as every other
--      encrypted column).
--
--   2. email_messages — every email the platform tries to send writes a
--      row here BEFORE the SMTP delivery attempt, so:
--        - operators see "what did we try to send?" in the admin view
--        - the email:dispatch worker can retry transient failures up to
--          3 times without losing context
--        - a daily retention task (email:cleanup_old) purges rows older
--          than 90 days so the table doesn't grow unbounded
--
-- Both tables are NOT-NULL-DEFAULT only on ALTER COLUMN paths (T30 lint);
-- this is a fresh CREATE TABLE so the constraint just sets the starting
-- value for any operator-inserted row.

CREATE TABLE smtp_settings (
    -- Singleton row — the application code always reads/writes the same
    -- id. UUID is overkill but matches every other settings table in
    -- the schema (consistency over byte-savings).
    id                  UUID PRIMARY KEY,
    enabled             BOOLEAN      NOT NULL DEFAULT false,
    host                VARCHAR(255) NOT NULL DEFAULT '',
    port                INTEGER      NOT NULL DEFAULT 587,
    username            VARCHAR(255) NOT NULL DEFAULT '',
    -- Fernet-encrypted; uses the existing auth.Encryptor key set so a
    -- DB-only leak doesn't trivially yield a working SMTP relay. The
    -- plaintext is decrypted ONLY inside Sender.Send and never logged.
    password_encrypted  TEXT         NOT NULL DEFAULT '',
    from_address        VARCHAR(255) NOT NULL DEFAULT '',
    from_name           VARCHAR(255) NOT NULL DEFAULT 'Astronomer',
    -- "plain" | "login" | "cram-md5" | "none"
    auth_mechanism      VARCHAR(32)  NOT NULL DEFAULT 'plain',
    -- "starttls" | "tls" | "none"
    encryption          VARCHAR(32)  NOT NULL DEFAULT 'starttls',
    require_tls         BOOLEAN      NOT NULL DEFAULT true,
    timeout_seconds     INTEGER      NOT NULL DEFAULT 30,
    created_at          TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE TABLE email_messages (
    id           UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    to_address   VARCHAR(255) NOT NULL,
    cc_address   TEXT         NOT NULL DEFAULT '',
    -- RFC 5322 §2.1.1 caps subject lines at 998 chars; the handler
    -- validates ASCII-safety as well so we don't try to emit raw
    -- non-ASCII bytes through net/smtp.
    subject      VARCHAR(998) NOT NULL,
    -- Template identifier for the rendered body, e.g. "password_reset",
    -- "account_locked". The admin view groups by this column.
    template     VARCHAR(64)  NOT NULL,
    body_text    TEXT         NOT NULL DEFAULT '',
    body_html    TEXT         NOT NULL DEFAULT '',
    -- Optional link back to the user the email is about. SET NULL on
    -- delete so audit history survives a user deletion.
    user_id      UUID         REFERENCES users(id) ON DELETE SET NULL,
    -- "queued"   — awaiting the next dispatcher tick
    -- "sent"     — SMTP accepted the message
    -- "failed"   — final failure after attempts >= 3
    -- "skipped"  — SMTP is disabled OR the row aged out while queued
    status       VARCHAR(16)  NOT NULL DEFAULT 'queued',
    attempts     INTEGER      NOT NULL DEFAULT 0,
    last_error   TEXT         NOT NULL DEFAULT '',
    sent_at      TIMESTAMPTZ,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ  NOT NULL DEFAULT now()
);

-- Hot path for the dispatcher: every 30s SELECT FROM email_messages
-- WHERE status IN ('queued','failed'). Partial index keeps the working
-- set tiny on a table whose 'sent' rows dominate steady state.
CREATE INDEX idx_email_messages_status
    ON email_messages (status)
    WHERE status IN ('queued', 'failed');

-- Per-user lookup for the future "your recent emails" account page.
CREATE INDEX idx_email_messages_user
    ON email_messages (user_id);

-- Password reset tokens (migration 047). Backs the
-- /auth/password-reset/request|complete endpoints. A request enqueues
-- the email AND inserts a token row whose hash is tied to the user's
-- current password hash — changing the password invalidates every
-- outstanding reset link without an explicit revoke step.
CREATE TABLE password_reset_tokens (
    id              UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- hex(sha256(token)). The plaintext is in the emailed link only.
    token_hash      VARCHAR(64)  NOT NULL,
    -- Snapshot of users.password at issuance. The verify step compares
    -- against the user's CURRENT password hash and rejects when they
    -- differ — a successful password change in the interim invalidates
    -- this token.
    password_hash_at_issue VARCHAR(128) NOT NULL,
    expires_at      TIMESTAMPTZ  NOT NULL,
    used_at         TIMESTAMPTZ,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX uidx_password_reset_token_hash
    ON password_reset_tokens (token_hash);

CREATE INDEX idx_password_reset_user
    ON password_reset_tokens (user_id)
    WHERE used_at IS NULL;
