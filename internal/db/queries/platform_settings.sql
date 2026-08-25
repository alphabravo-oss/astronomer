-- name: GetPlatformSetting :one
SELECT key, value, description, updated_by, updated_at, created_at
FROM platform_settings
WHERE key = $1;

-- name: ListPlatformSettings :many
SELECT key, value, description, updated_by, updated_at, created_at
FROM platform_settings
ORDER BY key;

-- name: ListPlatformSettingsByPrefix :many
-- The prefix scan is range-indexable thanks to text_pattern_ops in 046.
-- Pass exact prefix WITHOUT a trailing wildcard — the LIKE pattern is
-- assembled here so callers can't accidentally smuggle a wildcard mid-
-- string.
SELECT key, value, description, updated_by, updated_at, created_at
FROM platform_settings
WHERE key LIKE sqlc.arg(prefix)::text || '%'
ORDER BY key;

-- name: UpsertPlatformSetting :one
INSERT INTO platform_settings (key, value, description, updated_by, updated_at)
VALUES ($1, $2, $3, $4, now())
ON CONFLICT (key) DO UPDATE SET
    value      = EXCLUDED.value,
    -- Preserve the seeded description if the caller passes empty.
    description = CASE WHEN EXCLUDED.description = '' THEN platform_settings.description ELSE EXCLUDED.description END,
    updated_by = EXCLUDED.updated_by,
    updated_at = now()
RETURNING key, value, description, updated_by, updated_at, created_at;

-- name: BatchUpsertPlatformSettings :many
-- Apply a validated settings form as one PostgreSQL statement. The handler
-- passes a JSON array of {key,value,description} records only after every
-- entry has passed the registry/type checks, so one bad write can never leave
-- an operator with a partially saved configuration.
WITH input AS (
    SELECT *
    FROM jsonb_to_recordset(sqlc.arg(payload)::jsonb)
        AS x(key text, value jsonb, description text)
)
INSERT INTO platform_settings (key, value, description, updated_by, updated_at)
SELECT key, value, description, sqlc.arg(updated_by)::uuid, now()
FROM input
ON CONFLICT (key) DO UPDATE SET
    value       = EXCLUDED.value,
    description = CASE WHEN EXCLUDED.description = '' THEN platform_settings.description ELSE EXCLUDED.description END,
    updated_by  = EXCLUDED.updated_by,
    updated_at  = now()
RETURNING key, value, description, updated_by, updated_at, created_at;

-- name: DeletePlatformSetting :exec
-- DELETE resets to handler-side defaults — the row is gone and the
-- handler's registry default is what subsequent GETs return.
DELETE FROM platform_settings WHERE key = $1;
