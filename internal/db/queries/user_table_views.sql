-- name: ListUserTableViews :many
SELECT * FROM user_table_views
WHERE user_id = $1 AND table_key = $2
ORDER BY lower(name), name;

-- name: GetUserTableView :one
SELECT * FROM user_table_views WHERE id = $1 AND user_id = $2;

-- name: CountUserTableViews :one
SELECT count(*) FROM user_table_views WHERE user_id = $1 AND table_key = $2;

-- name: LockUserTableViewScope :exec
-- Serializes the per-table view cap for one user and table.
SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0));

-- name: CreateUserTableView :one
INSERT INTO user_table_views (user_id, table_key, name, state, is_default)
VALUES ($1, $2, $3, $4, false)
RETURNING *;

-- name: UpdateUserTableView :one
UPDATE user_table_views
SET name = COALESCE(sqlc.narg(name)::text, name),
    state = COALESCE(sqlc.narg(state)::jsonb, state),
    updated_at = now()
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)
RETURNING *;

-- name: ClearUserTableViewDefault :exec
UPDATE user_table_views SET is_default = false, updated_at = now()
WHERE user_id = $1 AND table_key = $2 AND is_default AND id <> $3;

-- name: SetUserTableViewDefault :one
UPDATE user_table_views SET is_default = $3, updated_at = now()
WHERE id = $1 AND user_id = $2
RETURNING *;

-- name: DeleteUserTableView :one
DELETE FROM user_table_views WHERE id = $1 AND user_id = $2
RETURNING *;
