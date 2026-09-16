-- name: SearchPrincipalUsers :many
SELECT
    u.id AS user_id,
    u.email,
    u.username,
    u.first_name,
    u.last_name,
    u.last_login,
    CASE WHEN ep.id IS NULL THEN ''::text ELSE ep.id::text END AS external_principal_id,
    CASE WHEN ep.connector_id IS NULL THEN ''::text ELSE ep.connector_id::text END AS connector_id,
    coalesce(dc.name, '') AS connector_name,
    coalesce(dc.type, '') AS connector_type,
    coalesce(ep.subject, '') AS subject,
    CASE WHEN ep.id IS NOT NULL AND ep.linked_at IS NULL THEN true ELSE false END AS pending
FROM users u
LEFT JOIN LATERAL (
    SELECT candidate.*
    FROM external_principals candidate
    WHERE candidate.user_id = u.id
    ORDER BY candidate.linked_at NULLS FIRST, candidate.created_at ASC
    LIMIT 1
) ep ON true
LEFT JOIN dex_connectors dc ON dc.id = ep.connector_id
WHERE u.is_service = false
  AND u.is_active = true
  AND lower(
    coalesce(u.username, '') || ' ' ||
    coalesce(u.email, '') || ' ' ||
    coalesce(u.first_name, '') || ' ' ||
    coalesce(u.last_name, '')
  ) LIKE '%' || lower(sqlc.arg(search)) || '%'
ORDER BY
    CASE
        WHEN lower(u.username) = lower(sqlc.arg(search)) THEN 0
        WHEN lower(u.email) = lower(sqlc.arg(search)) THEN 1
        ELSE 2
    END,
    u.created_at DESC,
    u.id DESC
LIMIT sqlc.arg(result_limit);

-- name: MaterializeExternalPrincipal :one
WITH existing_principal AS MATERIALIZED (
    SELECT ep.user_id
    FROM external_principals ep
    WHERE ep.connector_id = sqlc.arg(target_connector_id)
      AND ep.subject = sqlc.arg(target_subject)
), upserted_user AS (
    INSERT INTO users (
        email, username, first_name, last_name, password,
        is_active, is_staff, is_superuser
    )
    SELECT
        sqlc.arg(principal_email), sqlc.arg(local_username), sqlc.arg(principal_first_name),
        sqlc.arg(principal_last_name), '!', true, false, false
    WHERE NOT EXISTS (SELECT 1 FROM existing_principal)
    ON CONFLICT (email) DO UPDATE SET email = EXCLUDED.email
    RETURNING id
), target_user AS MATERIALIZED (
    SELECT user_id AS id FROM existing_principal
    UNION ALL
    SELECT id FROM upserted_user
    LIMIT 1
), materialized AS (
    INSERT INTO external_principals (
        connector_id, subject, email, username, display_name, user_id
    )
    SELECT
        sqlc.arg(target_connector_id), sqlc.arg(target_subject), sqlc.arg(principal_email),
        sqlc.arg(external_username), sqlc.arg(principal_display_name), target_user.id
    FROM target_user
    ON CONFLICT (connector_id, subject) DO UPDATE SET
        email = EXCLUDED.email,
        username = EXCLUDED.username,
        display_name = EXCLUDED.display_name
    RETURNING *
)
SELECT * FROM materialized;

-- name: ClaimExternalPrincipal :one
WITH claimed AS (
    UPDATE external_principals ep
    SET linked_at = COALESCE(ep.linked_at, now()),
        email = sqlc.arg(principal_email),
        username = sqlc.arg(principal_username),
        display_name = sqlc.arg(principal_display_name)
    FROM dex_connectors dc
    WHERE dc.id = ep.connector_id
      AND dc.name = sqlc.arg(target_connector_name)
      AND ep.subject = sqlc.arg(target_subject)
    RETURNING ep.user_id
)
SELECT users.*
FROM users
JOIN claimed ON claimed.user_id = users.id;
