-- Migration 045: collapse the two parallel auth-provider systems into one.
--
-- Background:
--   * Migration 001 created `sso_configurations` (3 hardcoded providers:
--     github / google / oidc). The server does the OAuth2 dance directly.
--   * Migration 023 added `dex_settings` + `dex_connectors`. The server
--     manages an EXTERNAL Dex deployment with 10 connector types and lets
--     Dex broker the messy IdPs.
--
-- Two source-of-truth tables for "who can sign in" is the longest-running
-- architectural debt in this stack. This migration moves the legacy
-- sso_configurations rows into dex_connectors so the card-grid wizard is
-- the only setting surface going forward.
--
-- Strategy:
--   1. For every ENABLED sso_configurations row, INSERT the equivalent
--      dex_connector. The client_secret_encrypted column is already
--      Fernet-encrypted with the same Encryptor used by dex_connectors.config
--      secret fields, so we move the ciphertext verbatim — no plaintext
--      ever passes through the migration.
--   2. Stamp `migrated_to_dex_at` on the legacy rows so a later cleanup
--      migration can drop them, and so the startup-time deprecation warning
--      can tell migrated rows from rows the operator added AFTER cutover.
--   3. Legacy /api/v1/auth/login/{provider}/ endpoints keep working for one
--      release — the SSOManager keeps loading from sso_configurations. The
--      cleanup is a separate migration once telemetry shows nobody is
--      hitting the legacy endpoints any more.
--
-- Re-runnability:
--   * INSERT ... ON CONFLICT (name) DO NOTHING — re-running is a no-op once
--     the rows have been migrated.
--   * `ADD COLUMN ... IF NOT EXISTS` (Postgres 9.6+ syntax) — the column
--     add survives partial application.
--   * The `UPDATE sso_configurations SET migrated_to_dex_at = now()` is
--     guarded so already-stamped rows aren't re-stamped (preserves the
--     timestamp of the actual migration).
--
-- T30 lint compliance: every ADD COLUMN is NULLable, so no rewrite scan.

-- Phase 1: copy every enabled legacy row into dex_connectors.
--
-- Mapping per provider:
--   * github → type='github', config = { clientID, clientSecret (ciphertext), orgs[] }
--   * google → type='google', config = { clientID, clientSecret (ciphertext), hostedDomains[] }
--   * oidc   → type='oidc',   config = { issuer, clientID, clientSecret (ciphertext), scopes[] }
--
-- The `allowed_organizations` / `allowed_domains` JSONB columns on sso_configurations
-- store JSON arrays of strings already, so they slot straight into the connector
-- config under `orgs` / `hostedDomains` respectively. For oidc rows we also pull
-- the issuer URL out of the existing config blob; pre-cutover OIDC providers
-- always had `issuer_url` populated (the SSOManager refuses to load otherwise).
--
-- We bias `enabled = is_enabled` to preserve the operator's intent — they enabled
-- the legacy row, so the new connector should be active too.
INSERT INTO dex_connectors (name, type, display_name, config, enabled, created_at, updated_at)
SELECT
    'legacy-' || provider,
    provider,
    COALESCE(NULLIF(display_name, ''), provider),
    CASE provider
        WHEN 'github' THEN
            jsonb_build_object(
                'clientID',     client_id,
                'clientSecret', client_secret_encrypted,
                'orgs',         COALESCE(allowed_organizations, '[]'::jsonb)
            ) || COALESCE(config, '{}'::jsonb)
        WHEN 'google' THEN
            jsonb_build_object(
                'clientID',      client_id,
                'clientSecret',  client_secret_encrypted,
                'hostedDomains', COALESCE(allowed_domains, '[]'::jsonb)
            ) || COALESCE(config, '{}'::jsonb)
        WHEN 'oidc' THEN
            jsonb_build_object(
                'issuer',       COALESCE(config->>'issuer_url', ''),
                'clientID',     client_id,
                'clientSecret', client_secret_encrypted,
                'scopes',       COALESCE(config->'scopes', '[]'::jsonb)
            ) || COALESCE(config, '{}'::jsonb)
        ELSE
            jsonb_build_object(
                'clientID',     client_id,
                'clientSecret', client_secret_encrypted
            ) || COALESCE(config, '{}'::jsonb)
    END,
    is_enabled,
    created_at,
    now()
FROM sso_configurations
WHERE is_enabled = true
ON CONFLICT (name) DO NOTHING;

-- Phase 2: stamp the legacy rows so the deprecation warning + cleanup
-- migration can tell the migrated set from rows added post-cutover.
ALTER TABLE sso_configurations
    ADD COLUMN IF NOT EXISTS migrated_to_dex_at TIMESTAMPTZ;

UPDATE sso_configurations
SET migrated_to_dex_at = now()
WHERE is_enabled = true
  AND migrated_to_dex_at IS NULL;
