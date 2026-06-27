-- name: GetClusterByID :one
SELECT * FROM clusters WHERE id = $1;

-- name: EnsureLocalCluster :one
-- Idempotently create-or-return the singleton "local" cluster row that
-- represents the Kubernetes cluster the server itself runs in. Uses a CTE
-- so the round-trip both inserts (when no local row exists yet) and selects
-- (when one already does). The clusters_one_local partial unique index makes
-- the ON CONFLICT branch reachable; if the conflicting row was inserted by a
-- concurrent server replica, the SELECT in the UNION returns it.
WITH inserted AS (
    INSERT INTO clusters (
        name,
        display_name,
        description,
        status,
        api_server_url,
        distribution,
        kubernetes_version,
        node_count,
        is_local,
        environment,
        provider
    )
    SELECT
        sqlc.arg(name)::varchar,
        sqlc.arg(display_name)::varchar,
        sqlc.arg(description)::text,
        sqlc.arg(status)::varchar,
        sqlc.arg(api_server_url)::varchar,
        sqlc.arg(distribution)::varchar,
        sqlc.arg(kubernetes_version)::varchar,
        sqlc.arg(node_count)::integer,
        true,
        'production',
        'other'
    WHERE NOT EXISTS (SELECT 1 FROM clusters WHERE is_local = true)
    ON CONFLICT DO NOTHING
    RETURNING *
)
SELECT * FROM inserted
UNION ALL
SELECT * FROM clusters WHERE is_local = true AND NOT EXISTS (SELECT 1 FROM inserted)
LIMIT 1;

-- name: GetClusterByName :one
SELECT * FROM clusters WHERE name = $1 AND decommissioned_at IS NULL;

-- name: ListClusters :many
-- Excludes tombstoned (sprint 038) rows. Decommissioned clusters keep
-- their row in the DB for forensics but never appear in the UI list.
SELECT * FROM clusters WHERE decommissioned_at IS NULL ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: ListClustersByStatus :many
SELECT * FROM clusters WHERE status = sqlc.arg(status) AND decommissioned_at IS NULL ORDER BY created_at DESC LIMIT sqlc.arg(query_limit) OFFSET sqlc.arg(query_offset);

-- name: CreateCluster :one
INSERT INTO clusters (name, display_name, description, environment, region, provider, distribution, labels, annotations, created_by_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;

-- name: UpdateCluster :one
UPDATE clusters SET
    display_name = $2,
    description = $3,
    environment = $4,
    region = $5,
    labels = $6,
    annotations = $7
WHERE id = $1
RETURNING *;

-- name: UpdateClusterStatus :exec
-- Guarded: never overwrite a cluster that has already been tombstoned.
-- The decommission reconciler is the sole writer for decommissioned
-- clusters; the health-check + metrics sweepers can race against it
-- and would otherwise flip 'decommissioned' back to 'disconnected' or
-- 'active', producing the half-deleted "ghost" rows observed on .247.
UPDATE clusters SET status = $2 WHERE id = $1 AND decommissioned_at IS NULL;

-- name: UpdateClusterHeartbeat :exec
UPDATE clusters SET
    last_heartbeat = now(),
    agent_version = $2,
    kubernetes_version = $3,
    node_count = $4,
    distribution = $5
WHERE id = $1;

-- name: DeleteCluster :exec
DELETE FROM clusters WHERE id = $1;

-- name: CountClusters :one
SELECT count(*) FROM clusters WHERE decommissioned_at IS NULL;

-- name: GetClusterHealthStatus :one
SELECT * FROM cluster_health_statuses WHERE cluster_id = $1;

-- name: UpsertClusterHealthStatus :one
INSERT INTO cluster_health_statuses (cluster_id, cpu_usage_percent, memory_usage_percent, pod_count, node_count, conditions)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (cluster_id) DO UPDATE SET
    cpu_usage_percent = EXCLUDED.cpu_usage_percent,
    memory_usage_percent = EXCLUDED.memory_usage_percent,
    pod_count = EXCLUDED.pod_count,
    node_count = EXCLUDED.node_count,
    conditions = EXCLUDED.conditions,
    last_check = now()
RETURNING *;

-- name: CreateClusterRegistrationToken :one
INSERT INTO cluster_registration_tokens (cluster_id, token, token_hash, expires_at)
VALUES (
    sqlc.arg(cluster_id),
    sqlc.arg(token)::text,
    COALESCE(NULLIF(sqlc.arg(token_hash)::text, ''), encode(digest(sqlc.arg(token)::text, 'sha256'), 'hex')),
    sqlc.arg(expires_at)
)
RETURNING *;

-- name: GetRegistrationTokenByToken :one
-- The is_used filter is intentionally NOT applied: until the server issues a
-- long-lived agent token in CONNECT_ACK, the same registration token is the
-- only credential the agent has, and reconnect attempts must succeed up to
-- expires_at. is_used remains a tracking column for the future flow.
SELECT * FROM cluster_registration_tokens
WHERE (token_hash = encode(digest($1::text, 'sha256'), 'hex') OR (token_hash = '' AND token = $1::text))
  AND expires_at > now();

-- name: MarkRegistrationTokenUsed :exec
UPDATE cluster_registration_tokens SET is_used = true WHERE id = $1;

-- name: GetClusterAgentTokenByClusterID :one
SELECT * FROM cluster_agent_tokens WHERE cluster_id = $1 AND revoked_at IS NULL;

-- name: GetClusterAgentTokenByToken :one
-- During a rotation grace window the agent may still be presenting the OLD
-- token (it has not yet adopted the freshly-minted one), so we accept EITHER
-- the live token_hash OR the previous_token_hash. revoked_at still hard-gates:
-- a revoked row matches neither branch.
SELECT * FROM cluster_agent_tokens
WHERE (token_hash = encode(digest($1::text, 'sha256'), 'hex')
   OR (token_hash = '' AND token = $1::text)
   OR (previous_token_hash IS NOT NULL
       AND previous_token_hash = encode(digest($1::text, 'sha256'), 'hex')))
  AND revoked_at IS NULL;

-- name: UpsertClusterAgentToken :one
INSERT INTO cluster_agent_tokens (cluster_id, token, token_hash, last_used_at)
VALUES (
    sqlc.arg(cluster_id),
    sqlc.arg(token)::text,
    COALESCE(NULLIF(sqlc.arg(token_hash)::text, ''), encode(digest(sqlc.arg(token)::text, 'sha256'), 'hex')),
    now()
)
ON CONFLICT (cluster_id) DO UPDATE SET
    token = EXCLUDED.token,
    token_hash = EXCLUDED.token_hash,
    last_used_at = now(),
    revoked_at = NULL
RETURNING *;

-- name: TouchClusterAgentToken :exec
UPDATE cluster_agent_tokens SET last_used_at = now() WHERE id = $1 AND revoked_at IS NULL;

-- name: RotateClusterAgentToken :one
-- Performs the grace rotation atomically: the current token_hash moves to
-- previous_token_hash (so the old token keeps validating until the agent
-- adopts the new one), token/token_hash become the freshly-minted values,
-- last_rotated_at is stamped and rotation_pending_at is cleared.
UPDATE cluster_agent_tokens
SET previous_token_hash = token_hash,
    token = sqlc.arg(token)::text,
    token_hash = COALESCE(NULLIF(sqlc.arg(token_hash)::text, ''), encode(digest(sqlc.arg(token)::text, 'sha256'), 'hex')),
    last_used_at = now(),
    last_rotated_at = now(),
    rotation_pending_at = NULL,
    revoked_at = NULL
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: ClearPreviousClusterAgentTokenHash :exec
-- Called on the first CONNECT that authenticates with the NEW current token:
-- the old token is no longer needed, so retire it (revoke the previous hash).
UPDATE cluster_agent_tokens
SET previous_token_hash = NULL
WHERE id = $1 AND previous_token_hash IS NOT NULL;

-- name: SetClusterAgentTokenRotationPending :execrows
-- Trigger a rotation. Does NOT touch the live token — the NEXT CONNECT mints
-- the fresh one. No-op (0 rows) when the cluster has no (non-revoked) token OR
-- when a rotation is already in flight: rotation_pending_at already set (trigger
-- not yet consumed) or previous_token_hash still present (grace window — the
-- agent hasn't adopted the last new token yet). Gating here prevents a
-- double-rotation that would demote the still-in-use previous hash a second
-- time and strand an agent still holding the old token.
UPDATE cluster_agent_tokens
SET rotation_pending_at = now()
WHERE cluster_id = $1
  AND revoked_at IS NULL
  AND rotation_pending_at IS NULL
  AND previous_token_hash IS NULL;

-- name: RevokeClusterAgentToken :execrows
-- Standalone revocation: the durable token (and any grace token) is denied
-- from the next CONNECT onward. Clears previous_token_hash so the grace
-- window can't keep an already-revoked credential alive.
UPDATE cluster_agent_tokens
SET revoked_at = now(),
    previous_token_hash = NULL,
    rotation_pending_at = NULL
WHERE cluster_id = $1 AND revoked_at IS NULL;

-- name: ClearExpiredAgentTokenRotationGrace :execrows
-- Backstop sweep: clear previous_token_hash for rows whose rotation completed
-- more than the supplied interval ago but whose old hash was never cleared by
-- a new-token CONNECT (e.g. the agent crashed before reconnecting).
UPDATE cluster_agent_tokens
SET previous_token_hash = NULL
WHERE previous_token_hash IS NOT NULL
  AND last_rotated_at IS NOT NULL
  AND last_rotated_at < now() - make_interval(mins => sqlc.arg(grace_minutes)::int);

-- name: ListClustersDueForAgentTokenRotation :many
-- Periodic policy: join each cluster's durable token to its registration
-- policy and surface clusters whose token_rotation_days policy (>0) has
-- elapsed since the last rotation (or token creation, if never rotated) and
-- which don't already have a pending/active rotation. last_rotated_at NULL
-- means the token has never been rotated, so created_at is the age reference.
SELECT t.cluster_id, p.token_rotation_days
FROM cluster_agent_tokens t
JOIN cluster_registration_policies p ON p.cluster_id = t.cluster_id
WHERE t.revoked_at IS NULL
  AND t.rotation_pending_at IS NULL
  AND t.previous_token_hash IS NULL
  AND p.token_rotation_days > 0
  AND COALESCE(t.last_rotated_at, t.created_at) < now() - make_interval(days => p.token_rotation_days)
ORDER BY COALESCE(t.last_rotated_at, t.created_at) ASC
LIMIT sqlc.arg(row_limit);

-- name: DeleteExpiredRegistrationTokens :execrows
DELETE FROM cluster_registration_tokens WHERE expires_at < now() OR (is_used = true AND updated_at < now() - INTERVAL '7 days');

-- name: GetClusterRegistryConfig :one
SELECT * FROM cluster_registry_configs WHERE cluster_id = $1;

-- name: UpsertClusterRegistryConfig :one
INSERT INTO cluster_registry_configs (cluster_id, private_registry_url, registry_username, registry_password, registry_password_encrypted, insecure, ca_bundle)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (cluster_id) DO UPDATE SET
    private_registry_url = EXCLUDED.private_registry_url,
    registry_username = EXCLUDED.registry_username,
    registry_password = EXCLUDED.registry_password,
    registry_password_encrypted = EXCLUDED.registry_password_encrypted,
    insecure = EXCLUDED.insecure,
    ca_bundle = EXCLUDED.ca_bundle
RETURNING *;

-- name: DeleteClusterRegistryConfig :exec
DELETE FROM cluster_registry_configs WHERE cluster_id = $1;

-- Migration 050: multi-registry-per-cluster CRUD. The legacy
-- Get/Upsert/Delete by cluster_id above is kept for back-compat with the old
-- single-registry route; the queries below operate on the row id so multiple
-- registry configs can co-exist under one cluster.

-- name: ListClusterRegistryConfigs :many
SELECT * FROM cluster_registry_configs
WHERE cluster_id = $1
ORDER BY created_at ASC;

-- name: ListAllClusterRegistryConfigs :many
-- Used by the drift-reconcile sweep — walks every row across every cluster.
SELECT * FROM cluster_registry_configs
ORDER BY cluster_id, created_at ASC;

-- name: GetClusterRegistryConfigByID :one
SELECT * FROM cluster_registry_configs WHERE id = $1;

-- name: CreateClusterRegistryConfig :one
INSERT INTO cluster_registry_configs (
    cluster_id,
    private_registry_url,
    registry_username,
    registry_password,
    registry_password_encrypted,
    insecure,
    ca_bundle,
    namespaces,
    inject_default_sa,
    secret_name
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;

-- name: UpdateClusterRegistryConfig :one
UPDATE cluster_registry_configs SET
    private_registry_url = $2,
    registry_username    = $3,
    registry_password    = $4,
    registry_password_encrypted = $5,
    insecure             = $6,
    ca_bundle            = $7,
    namespaces           = $8,
    inject_default_sa    = $9,
    secret_name          = $10,
    updated_at           = now()
WHERE id = $1
RETURNING *;

-- name: DeleteClusterRegistryConfigByID :exec
DELETE FROM cluster_registry_configs WHERE id = $1;

-- name: MarkClusterRegistryApplied :exec
UPDATE cluster_registry_configs SET
    last_applied_at  = now(),
    last_apply_error = ''
WHERE id = $1;

-- name: MarkClusterRegistryApplyError :exec
UPDATE cluster_registry_configs SET
    last_apply_error = $2
WHERE id = $1;

-- name: ListClusterConditions :many
SELECT * FROM cluster_conditions WHERE cluster_id = $1 ORDER BY type;

-- name: UpsertClusterCondition :one
-- Match metav1.Condition semantics: when status flips, bump
-- last_transition_time; on every probe, bump last_probe_time and
-- updated_at. reason/message are always refreshed.
INSERT INTO cluster_conditions (
    cluster_id, type, status, reason, message,
    last_transition_time, last_probe_time
) VALUES (
    $1, $2, $3, $4, $5, now(), now()
)
ON CONFLICT (cluster_id, type) DO UPDATE SET
    status               = EXCLUDED.status,
    reason               = EXCLUDED.reason,
    message              = EXCLUDED.message,
    last_probe_time      = now(),
    last_transition_time = CASE
        WHEN cluster_conditions.status = EXCLUDED.status
            THEN cluster_conditions.last_transition_time
            ELSE now()
        END,
    updated_at           = now()
RETURNING *;
