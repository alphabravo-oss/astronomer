-- name: GetPlatformConfig :one
SELECT * FROM platform_configuration WHERE id = 1;

-- name: UpsertPlatformConfig :one
INSERT INTO platform_configuration (id, server_url, platform_name, telemetry_enabled, bootstrapped_at, instance_id)
VALUES (1, $1, $2, $3, $4, $5)
ON CONFLICT (id) DO UPDATE SET
    server_url = EXCLUDED.server_url,
    platform_name = EXCLUDED.platform_name,
    telemetry_enabled = EXCLUDED.telemetry_enabled,
    bootstrapped_at = EXCLUDED.bootstrapped_at,
    instance_id = EXCLUDED.instance_id
RETURNING *;

-- name: SetPlatformDefaultClusterTemplate :one
-- Sprint 074. UPDATE-only (NOT upsert) — the singleton row is seeded by
-- migration 001 and must always exist. Pass pgtype.UUID{} (Valid:false)
-- to clear the auto-attach default; pass a valid UUID to set it. The
-- handler validates that the UUID points to an existing template row
-- before calling this — we don't re-validate here because pg's FK does
-- the second-line check (a stale UUID raises foreign_key_violation,
-- which the handler translates into a 400).
UPDATE platform_configuration
SET default_cluster_template_id = $1
WHERE id = 1
RETURNING *;
