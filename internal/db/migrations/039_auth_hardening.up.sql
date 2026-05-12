-- Enterprise auth hardening: account lockout + JWT session revocation.
--
-- NIST 800-53 AC-7 (unsuccessful login attempts), ISO 27001 A.9.4.2,
-- SOC 2 CC6.1. The platform already rate-limits login attempts per source
-- IP; this layer adds the per-account lockout so a credential-stuffing
-- run that rotates source IPs still trips the safety net.
--
-- All ALTER COLUMN additions are NOT NULL DEFAULT to keep the migration
-- non-blocking on a populated table (T30 lint).

ALTER TABLE users
    ADD COLUMN failed_login_count   INTEGER     NOT NULL DEFAULT 0,
    ADD COLUMN failed_login_at      TIMESTAMPTZ,
    ADD COLUMN locked_until         TIMESTAMPTZ,
    ADD COLUMN locked_reason        TEXT        NOT NULL DEFAULT '',
    ADD COLUMN tokens_invalidated_at TIMESTAMPTZ;

-- Partial index — we only ever scan for currently-locked rows, and the
-- vast majority of accounts will have NULL here. Index is tiny in steady
-- state.
CREATE INDEX idx_users_locked_until ON users (locked_until)
    WHERE locked_until IS NOT NULL;

-- JWT revocation list (logout / forced revocation by JTI).
--
-- The row lifetime is bounded by the token's own expiry: a revoked JTI
-- only needs to remain on the deny-list until the underlying JWT would
-- have naturally expired, after which any signature check fails anyway.
-- The retention worker GCs rows where expires_at < now() to keep this
-- table bounded.
CREATE TABLE jwt_revocations (
    jti         VARCHAR(64) PRIMARY KEY,
    user_id     UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at  TIMESTAMPTZ NOT NULL,
    revoked_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    reason      TEXT        NOT NULL DEFAULT 'user_logout'
);
CREATE INDEX idx_jwt_revocations_user ON jwt_revocations (user_id);
CREATE INDEX idx_jwt_revocations_expires ON jwt_revocations (expires_at);
