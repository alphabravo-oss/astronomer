-- name: GetUserPreferences :one
SELECT * FROM user_preferences WHERE user_id = $1;

-- name: UpsertUserPreferences :one
INSERT INTO user_preferences (
    user_id, theme, table_density, landing_route, time_format, favorites, pinned_clusters
) VALUES (
    sqlc.arg(user_id), sqlc.arg(theme), sqlc.arg(table_density),
    sqlc.arg(landing_route), sqlc.arg(time_format), sqlc.arg(favorites), sqlc.arg(pinned_clusters)
)
ON CONFLICT (user_id) DO UPDATE SET
    theme = EXCLUDED.theme,
    table_density = EXCLUDED.table_density,
    landing_route = EXCLUDED.landing_route,
    time_format = EXCLUDED.time_format,
    favorites = EXCLUDED.favorites,
    pinned_clusters = EXCLUDED.pinned_clusters,
    updated_at = now()
RETURNING *;
