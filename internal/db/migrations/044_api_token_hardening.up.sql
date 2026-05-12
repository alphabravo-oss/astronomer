-- Enterprise API token hardening: per-token IP allowlists + scope
-- enforcement metadata.
--
-- Compliance drivers: PCI-DSS 8.2.1 (restrict access by identity),
-- SOC 2 CC6.1, ISO 27001 A.9.4.1. A leaked bearer today is usable from
-- anywhere on the internet and is unscoped — banks / regulated tenants
-- need both vectors closed.
--
-- Backward compatibility:
--   - `allowed_cidrs = ''` means "no IP restriction" (current behaviour).
--   - The `scopes` JSONB column already exists (added in 001_initial);
--     this migration does NOT alter it. The runtime validator treats
--     `[]` (the default for legacy rows) as "no enforcement" so existing
--     tokens keep working until they're rotated through the new UI.
--   - `last_seen_remote_ip` is updated best-effort on every successful
--     auth — useful for the operator UI ("where was this token last
--     used from?") and for forensic review after a leak.
--
-- All ALTER COLUMN additions are NOT NULL DEFAULT to keep the migration
-- non-blocking on a populated table (T30 lint).

ALTER TABLE api_tokens
    ADD COLUMN allowed_cidrs       TEXT NOT NULL DEFAULT '',
    ADD COLUMN last_seen_remote_ip TEXT NOT NULL DEFAULT '';

-- Partial index on the hot validation path. 001_initial already created
-- `idx_api_tokens_user_revoked` over `(user_id, is_revoked)`; this
-- companion partial index narrows the working set to the live rows the
-- ListTokensByUser query reads on every dashboard load.
CREATE INDEX IF NOT EXISTS idx_api_tokens_user_active
    ON api_tokens (user_id)
    WHERE is_revoked = false;
