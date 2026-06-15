-- Kubectl shell session bookkeeping (migration 065).
--
-- Hand-authored SQL for kubectl_sessions + kubectl_session_commands.
-- The sqlc generator produces a thin Go shim with type-safe arguments
-- around these queries; the worktree's generator is occasionally not
-- runnable so the *.sql.go file is hand-edited to match the same
-- output shape.

-- name: CreateKubectlSession :one
INSERT INTO kubectl_sessions (
    user_id, cluster_id, sa_namespace, sa_name, pod_namespace, pod_name,
    status, client_ip, user_agent
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING id, user_id, cluster_id, sa_namespace, sa_name, pod_namespace, pod_name,
          status, started_at, last_input_at, closed_at, expires_at, last_error,
          client_ip, user_agent;

-- name: GetKubectlSessionByID :one
SELECT id, user_id, cluster_id, sa_namespace, sa_name, pod_namespace, pod_name,
       status, started_at, last_input_at, closed_at, expires_at, last_error,
       client_ip, user_agent
FROM kubectl_sessions
WHERE id = $1;

-- name: ListActiveKubectlSessionsByCluster :many
SELECT id, user_id, cluster_id, sa_namespace, sa_name, pod_namespace, pod_name,
       status, started_at, last_input_at, closed_at, expires_at, last_error,
       client_ip, user_agent
FROM kubectl_sessions
WHERE cluster_id = $1 AND status IN ('starting', 'active')
ORDER BY started_at DESC;

-- name: ListAllActiveKubectlSessions :many
SELECT id, user_id, cluster_id, sa_namespace, sa_name, pod_namespace, pod_name,
       status, started_at, last_input_at, closed_at, expires_at, last_error,
       client_ip, user_agent
FROM kubectl_sessions
WHERE status IN ('starting', 'active')
ORDER BY started_at DESC;

-- name: ListExpiredKubectlSessions :many
SELECT id, user_id, cluster_id, sa_namespace, sa_name, pod_namespace, pod_name,
       status, started_at, last_input_at, closed_at, expires_at, last_error,
       client_ip, user_agent
FROM kubectl_sessions
WHERE status IN ('starting', 'active')
  AND (expires_at < now() OR last_input_at + interval '30 minutes' < now());

-- name: SetKubectlSessionStatus :exec
-- Explicit ::text casts on status keep pgx happy. Without them pgx infers
-- two different types for the same parameter (one for SET status, one
-- inside the CASE WHEN literal IN list) and rejects the query with
-- SQLSTATE 42P08 ("inconsistent types deduced for parameter").
UPDATE kubectl_sessions
SET status = sqlc.arg(status)::text,
    closed_at = CASE WHEN sqlc.arg(status)::text IN ('closed','expired','failed') THEN now() ELSE closed_at END,
    last_error = COALESCE(sqlc.arg(last_error)::text, last_error)
WHERE id = sqlc.arg(id);

-- name: TouchKubectlSessionInput :exec
UPDATE kubectl_sessions
SET last_input_at = now()
WHERE id = $1 AND status IN ('starting', 'active');

-- name: InsertKubectlSessionCommand :exec
INSERT INTO kubectl_session_commands (session_id, command_line)
VALUES ($1, $2);

-- name: ListKubectlSessionCommands :many
SELECT id, session_id, command_at, command_line
FROM kubectl_session_commands
WHERE session_id = $1
ORDER BY command_at ASC, id ASC
LIMIT $2 OFFSET $3;

-- name: CountKubectlSessionCommands :one
SELECT COUNT(*)
FROM kubectl_session_commands
WHERE session_id = $1;
