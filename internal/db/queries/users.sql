-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;

-- name: GetUserByIDForUpdate :one
-- Administrative identity mutations lock the row before deriving omitted
-- fields, revocation decisions, and transactional audit evidence.
SELECT * FROM users WHERE id = $1 FOR UPDATE;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = $1;

-- name: GetUserByUsername :one
SELECT * FROM users WHERE username = $1;

-- name: ListUsers :many
-- Human-user enumeration only. Service principals (is_service, migration 116 —
-- e.g. the per-cluster agent-ingest identities) exist solely to own tokens and
-- carry RBAC bindings; surfacing them on the admin user list, SCIM /Users, or a
-- support bundle would hand an IdP one synthetic account per cluster to
-- reconcile.
SELECT * FROM users WHERE is_service = false ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: SearchUsers :many
-- Server-side human-user lookup for RBAC subject pickers. Keep the expression
-- aligned with idx_users_directory_search_trgm in migration 032.
SELECT *
FROM users
WHERE is_service = false
  AND lower(
    coalesce(username, '') || ' ' ||
    coalesce(email, '') || ' ' ||
    coalesce(first_name, '') || ' ' ||
    coalesce(last_name, '')
  ) LIKE '%' || lower(sqlc.arg(search)) || '%'
ORDER BY
  CASE
    WHEN lower(username) = lower(sqlc.arg(search)) THEN 0
    WHEN lower(email) = lower(sqlc.arg(search)) THEN 1
    ELSE 2
  END,
  created_at DESC,
  id DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: CountSearchUsers :one
SELECT count(*)
FROM users
WHERE is_service = false
  AND lower(
    coalesce(username, '') || ' ' ||
    coalesce(email, '') || ' ' ||
    coalesce(first_name, '') || ' ' ||
    coalesce(last_name, '')
  ) LIKE '%' || lower(sqlc.arg(search)) || '%';

-- name: CreateUser :one
INSERT INTO users (email, username, first_name, last_name, password, is_active, is_staff, is_superuser)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: CreateBootstrapAdmin :one
-- Creates the initial admin user that ensure_admin runs on first boot of a
-- fresh database. The password is either operator-provided through Helm values
-- or auto-generated into the bootstrap Secret; the account is immediately
-- usable and is not forced through a first-login password reset.
INSERT INTO users (email, username, first_name, last_name, password, is_active, is_staff, is_superuser)
VALUES ($1, $2, $3, $4, $5, true, true, true)
RETURNING *;

-- name: ClearMustChangePassword :exec
UPDATE users SET must_change_password = false, updated_at = now() WHERE id = $1;

-- name: UpdateUser :one
UPDATE users SET
    email = $2,
    username = $3,
    first_name = $4,
    last_name = $5,
    is_active = $6,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateUserPassword :exec
UPDATE users SET password = $2, updated_at = now() WHERE id = $1;

-- name: UpdateUserPasswordHash :exec
-- Convenience alias used by the login flow when an inherited Django
-- PBKDF2/argon2 hash is upgraded to bcrypt on first successful match.
UPDATE users SET password = $2, updated_at = now() WHERE id = $1;

-- name: UpdateUserLastLogin :exec
UPDATE users SET last_login = now() WHERE id = $1;

-- name: DeactivateInactiveUsers :many
-- Deactivation, credential invalidation, SSO-session cleanup, and audit
-- evidence are deliberately one statement. A failure in any part rolls the
-- whole sweep back, so a retry cannot leave an unaudited account transition.
WITH affected AS (
    UPDATE users
    SET is_active = false,
        tokens_invalidated_at = GREATEST(
            COALESCE(tokens_invalidated_at, '-infinity'::timestamptz),
            sqlc.arg(invalidated_at)::timestamptz
        ),
        updated_at = sqlc.arg(invalidated_at)::timestamptz
    WHERE is_active = true
      AND is_superuser = false
      AND is_service = false
      AND COALESCE(last_login, date_joined, created_at) < sqlc.arg(cutoff)::timestamptz
    RETURNING id, username, email, last_login, date_joined, created_at,
              COALESCE(last_login, date_joined, created_at) AS inactive_since,
              tokens_invalidated_at
), cleared_sessions AS (
    DELETE FROM sso_sessions AS session
    USING affected
    WHERE session.user_id = affected.id
    RETURNING session.user_id
), session_counts AS (
    SELECT user_id, count(*)::bigint AS sessions_cleared
    FROM cleared_sessions
    GROUP BY user_id
), audited AS (
    INSERT INTO audit_log (
        source, action, resource_type, resource_id, resource_name,
        detail, action_class
    )
    SELECT
        'worker',
        'user.inactive_retention.deactivated',
        'user',
        affected.id::text,
        affected.username,
        jsonb_build_object(
            'email', affected.email,
            'inactive_since', affected.inactive_since,
            'cutoff', sqlc.arg(cutoff)::timestamptz,
            'retention_days', sqlc.arg(retention_days)::integer,
            'tokens_invalidated_at', affected.tokens_invalidated_at,
            'sso_sessions_cleared', COALESCE(session_counts.sessions_cleared, 0)
        ),
        'system'
    FROM affected
    LEFT JOIN session_counts ON session_counts.user_id = affected.id
    RETURNING resource_id
)
SELECT
    affected.id,
    affected.username,
    affected.email,
    affected.last_login,
    affected.date_joined,
    affected.created_at,
    affected.inactive_since,
    affected.tokens_invalidated_at,
    COALESCE(session_counts.sessions_cleared, 0)::bigint AS sso_sessions_cleared
FROM affected
LEFT JOIN session_counts ON session_counts.user_id = affected.id
JOIN audited ON audited.resource_id = affected.id::text
ORDER BY affected.id;

-- name: CountUsers :one
-- Pairs with ListUsers: the same is_service exclusion, so seat counts
-- (telemetry) and the "is this a fresh database?" bootstrap check don't count
-- machine principals as users.
SELECT count(*) FROM users WHERE is_service = false;

-- name: DeleteUser :exec
DELETE FROM users WHERE id = $1;
