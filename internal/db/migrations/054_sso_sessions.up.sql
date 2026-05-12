-- Single sign-out (SLO) support — NIST 800-53 AC-12 / SOC 2 CC6.6.
--
-- The Astronomer JWT layer can be revoked locally (migration 039), but
-- that ALONE doesn't terminate the user's session at Dex / the upstream
-- IdP. A refresh of /api/v1/auth/login auto-relogs them because the
-- upstream cookie is still valid. To close the compliance gap we need
-- RP-initiated logout: when the user logs out of Astronomer, we direct
-- them through Dex's `end_session_endpoint` so Dex tears down its
-- session (and, for connectors that support back-channel SLO, the
-- upstream IdP too).
--
-- For RP-initiated logout we need the `id_token_hint` parameter — i.e.
-- the upstream id_token that Dex originally minted. This is a one-time
-- artifact of the OIDC callback; today we discard it after extracting
-- claims. The sso_sessions table persists it (Fernet-encrypted at rest)
-- alongside the Astronomer JWT's JTI so Logout can correlate the two
-- without an extra session cookie.
--
-- A single user can have several rows here — one per browser/device
-- session. The cleanup task GCs expires_at < now() alongside the
-- jwt_revocations purge.

CREATE TABLE sso_sessions (
    -- Same JTI as the Astronomer JWT, so we can join the two on logout
    -- without an extra cookie or query parameter. PRIMARY KEY because
    -- one Astronomer JWT == one upstream session.
    jti                         VARCHAR(64) PRIMARY KEY,
    user_id                     UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- Provider name as recognised by the SSOManager (e.g. "dex",
    -- "okta-oidc", "keycloak"). Used as the audit/metric label.
    provider_name               VARCHAR(64) NOT NULL,
    -- Fernet-encrypted upstream id_token. Used as id_token_hint in the
    -- end_session redirect. Encrypted because it's a bearer-equivalent
    -- secret while it's valid — a DB leak that exposed plaintext
    -- id_tokens would let an attacker forge end-session calls against
    -- the IdP for those users.
    upstream_id_token_encrypted TEXT        NOT NULL,
    -- Cached upstream `end_session_endpoint` from the OIDC discovery
    -- document at issuance time. Stored on the row so the Logout
    -- hot path doesn't have to re-fetch /.well-known/openid-configuration
    -- (which would add ~100ms and could fail), and so a discovery
    -- change AFTER issuance doesn't break the in-flight session's
    -- logout. Empty string when the IdP doesn't advertise an
    -- end_session_endpoint at all — in that case Logout degrades to
    -- "local JWT revoked, no upstream redirect".
    end_session_endpoint        TEXT        NOT NULL DEFAULT '',
    expires_at                  TIMESTAMPTZ NOT NULL,
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- "List a user's active sessions" — used by admin force-logout to fire
-- back-channel end-session calls against every device the user is
-- signed in from.
CREATE INDEX idx_sso_sessions_user ON sso_sessions (user_id);

-- "Purge expired rows" — used by the nightly retention task that
-- already GCs jwt_revocations. Partial range scan over the expires_at
-- column, so a btree index is the right fit.
CREATE INDEX idx_sso_sessions_expires ON sso_sessions (expires_at);
