-- Two-factor authentication (TOTP / RFC 6238) for local-password users.
-- SSO users are out of scope — their IdP enforces MFA.
--
-- This is a SOC 2 Type II + ISO 27001 hard-block for enterprise sales.
-- We persist:
--
--   1. user_totp_enrollments — one row per user that completed enrollment.
--      Absence of a row means the user has not enrolled. The shared secret
--      is stored Fernet-encrypted (auth.Encryptor) so a DB-only leak does
--      NOT trivially yield "infinite working TOTP codes"; rotation works
--      identically to every other encrypted column in the schema.
--
--   2. user_totp_recovery_codes — one-time-use recovery codes (10 per
--      enrollment by default). We store ONLY a sha256 hash of each code,
--      not the literal — so an attacker with DB read CANNOT bypass 2FA
--      with the stored data. The codes themselves are shown to the user
--      exactly once at enrollment / regeneration time.
--
-- Login flow change (in code, not schema): when a user has a row in
-- user_totp_enrollments, the bcrypt-success path no longer hands out a
-- session JWT directly — it returns a short-lived "challenge" token and
-- forces the client to POST /totp/verify/ with a 6-digit code (or a
-- recovery code). Per-user failure counts continue to reuse the existing
-- account-lockout columns from migration 039.

CREATE TABLE user_totp_enrollments (
    user_id           UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    -- Fernet-encrypted shared secret. Decrypted on every verify; multiple
    -- keys supported via auth.Encryptor for online rotation. The plaintext
    -- secret never leaves the verify code path and is never logged.
    secret_encrypted  TEXT        NOT NULL,
    -- Human-readable account label shown in the authenticator app, e.g.
    -- "astronomer.example.com:alice". Stored so we can render the QR with
    -- the same label the user already paired with after a future
    -- regenerate-codes-without-rescan flow.
    label             VARCHAR(255) NOT NULL DEFAULT '',
    -- confirmed_at is NOT NULL because the row is only created post-
    -- confirm. The pending "scanned QR but didn't enter code yet" state
    -- lives in a signed session, NOT in the DB — we don't want a stale
    -- half-enrollment to interfere with the user's next attempt.
    confirmed_at      TIMESTAMPTZ NOT NULL,
    last_used_at      TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One-time-use recovery codes. Stored as hex(sha256(code)); the literal
-- code is returned to the user once and never re-derivable from the DB.
-- 10 codes per enrollment by default; regenerate-all invalidates every
-- prior code (used or not) and writes a fresh 10.
CREATE TABLE user_totp_recovery_codes (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_hash   VARCHAR(64) NOT NULL,            -- hex(sha256(code))
    used_at     TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Hot path: "how many unused codes does this user have left?" + the
-- consume-on-login lookup. Partial index so the typical (most codes
-- unused) scan stays cheap.
CREATE INDEX idx_totp_recovery_user
    ON user_totp_recovery_codes (user_id)
    WHERE used_at IS NULL;

-- Defense-in-depth: even if two users happened to be issued the same
-- 10-char code (vanishingly unlikely at 50 bits of entropy but still),
-- the unique index makes a cross-user replay impossible.
CREATE UNIQUE INDEX uidx_totp_recovery_hash
    ON user_totp_recovery_codes (code_hash);
