-- name: GetPlatformConfig :one
SELECT * FROM platform_configuration WHERE id = 1;

-- name: UpsertPlatformConfig :one
INSERT INTO platform_configuration (id, server_url, platform_name, telemetry_enabled, bootstrapped_at)
VALUES (1, $1, $2, $3, $4)
ON CONFLICT (id) DO UPDATE SET
    server_url = EXCLUDED.server_url,
    platform_name = EXCLUDED.platform_name,
    telemetry_enabled = EXCLUDED.telemetry_enabled,
    bootstrapped_at = EXCLUDED.bootstrapped_at
RETURNING *;
