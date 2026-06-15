-- Cloud credentials CRUD (migration 053).
--
-- Hand-edited SQL for the cloud_credentials + cloud_credential_-
-- materializations tables. The sqlc generator produces a thin Go shim
-- with type-safe arguments around these queries.

-- name: ListCloudCredentialsForProject :many
SELECT id, project_id, name, provider, description, data_encrypted, target_refs,
       created_by, created_at, updated_at
FROM cloud_credentials
WHERE project_id = $1
ORDER BY name ASC;

-- name: GetCloudCredentialByID :one
SELECT id, project_id, name, provider, description, data_encrypted, target_refs,
       created_by, created_at, updated_at
FROM cloud_credentials
WHERE id = $1;

-- name: GetCloudCredentialByProjectAndName :one
SELECT id, project_id, name, provider, description, data_encrypted, target_refs,
       created_by, created_at, updated_at
FROM cloud_credentials
WHERE project_id = $1 AND name = $2;

-- name: CreateCloudCredential :one
INSERT INTO cloud_credentials (
    project_id, name, provider, description, data_encrypted, target_refs, created_by
) VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id, project_id, name, provider, description, data_encrypted, target_refs,
          created_by, created_at, updated_at;

-- name: UpdateCloudCredential :one
UPDATE cloud_credentials
SET description    = $2,
    data_encrypted = $3,
    target_refs    = $4,
    updated_at     = now()
WHERE id = $1
RETURNING id, project_id, name, provider, description, data_encrypted, target_refs,
          created_by, created_at, updated_at;

-- name: DeleteCloudCredential :exec
DELETE FROM cloud_credentials WHERE id = $1;

-- Materializations -------------------------------------------------------

-- name: ListCloudCredentialMaterializations :many
SELECT id, credential_id, cluster_id, namespace, secret_name, status,
       last_applied_at, last_error, created_at, updated_at
FROM cloud_credential_materializations
WHERE credential_id = $1
ORDER BY cluster_id, namespace ASC;

-- name: UpsertCloudCredentialMaterialization :one
INSERT INTO cloud_credential_materializations (
    credential_id, cluster_id, namespace, secret_name, status
) VALUES ($1, $2, $3, $4, 'pending')
ON CONFLICT (credential_id, cluster_id, namespace) DO UPDATE SET
    secret_name = EXCLUDED.secret_name,
    status      = CASE
        WHEN cloud_credential_materializations.secret_name = EXCLUDED.secret_name
            THEN cloud_credential_materializations.status
        ELSE 'pending'
    END,
    updated_at  = now()
RETURNING id, credential_id, cluster_id, namespace, secret_name, status,
          last_applied_at, last_error, created_at, updated_at;

-- name: DeleteOrphanCloudCredentialMaterializations :exec
-- Removes rows whose (cluster_id, namespace) no longer appears in the
-- credential's target_refs JSONB. The handler calls this after writing
-- the new target_refs and upserting the kept rows so the drift sweep
-- doesn't keep re-applying old targets.
DELETE FROM cloud_credential_materializations m
WHERE m.credential_id = sqlc.arg(credential_id)
  AND NOT EXISTS (
      SELECT 1 FROM jsonb_array_elements(sqlc.arg(target_refs)::jsonb) tgt
      WHERE tgt->>'cluster_id' = m.cluster_id::text
        AND tgt->>'namespace'  = m.namespace
  );

-- name: MarkCloudCredentialMaterializationApplied :exec
UPDATE cloud_credential_materializations
SET status          = 'applied',
    last_applied_at = now(),
    last_error      = '',
    updated_at      = now()
WHERE id = $1;

-- name: MarkCloudCredentialMaterializationFailed :exec
UPDATE cloud_credential_materializations
SET status     = 'failed',
    last_error = $2,
    updated_at = now()
WHERE id = $1;

-- name: ListAllPendingCloudCredentialMaterializations :many
-- Used by the drift sweep to fan out across every materialization that's
-- not in steady state. status != 'applied' means either fresh or failed
-- and needing retry.
SELECT id, credential_id, cluster_id, namespace, secret_name, status,
       last_applied_at, last_error, created_at, updated_at
FROM cloud_credential_materializations
WHERE status != 'applied'
ORDER BY credential_id, cluster_id, namespace ASC;
