-- SCIM 2.0 provisioning bearer tokens (P1 item 11 — "scim").
--
-- SCIM clients (Okta, Azure AD, OneLogin, ...) authenticate to
-- /scim/v2/* with a static bearer token. We store only the SHA-256 hash
-- (hex) of the token — same hash-only contract as cluster_agent_tokens
-- (migration 094) and the registration token (migration 102) — so a DB
-- compromise never yields a usable credential. The plaintext is shown
-- exactly once at creation time.
--
-- Minimal slice: a single named token row. Multiple rows are allowed so
-- an operator can rotate (create new, delete old). No per-token scoping
-- yet — possession of any non-revoked token grants the full SCIM
-- surface, which is the standard SCIM contract (the IdP is the trusted
-- provisioning peer).
CREATE TABLE scim_tokens (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name         VARCHAR(128) NOT NULL,
    token_hash   VARCHAR(128) NOT NULL UNIQUE,
    prefix       VARCHAR(16) NOT NULL DEFAULT '',
    last_used_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_scim_tokens_hash ON scim_tokens (token_hash);
