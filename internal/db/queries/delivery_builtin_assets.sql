-- Builtin assets require a session lock on the checked-out connection before
-- Serializable begins. Generated lock/unlock methods must use that same
-- connection; asset methods use its existing transaction.

-- name: GetBuiltinRegistrationState :one
SELECT c.registration_phase, c.is_local,
COALESCE(c.install_baseline, false)::boolean AS install_baseline,
(COALESCE(c.annotations ->> 'astronomer.io/image-scanning', '') = 'disabled')::boolean AS scanning_disabled,
EXISTS (
SELECT 1 FROM delivery_controller_inventory i
WHERE i.cluster_id=c.id AND i.ready=true AND i.compatibility_status='compatible'
)::boolean AS inventory_ready,
COALESCE(retry.created_at,'1970-01-01T00:00:00Z'::timestamptz) AS latest_retry_time,
(retry.created_at IS NOT NULL)::boolean AS latest_retry_found
FROM clusters c LEFT JOIN LATERAL (SELECT s.created_at FROM cluster_registration_steps s
WHERE s.cluster_id=c.id AND s.step_name='delivery_retry_requested'
ORDER BY s.created_at DESC,s.step_order DESC LIMIT 1) retry ON true
WHERE c.id=$1 AND c.decommissioned_at IS NULL;

-- name: UnlockBuiltinAssetsSession :one
SELECT pg_advisory_unlock(hashtextextended('astronomer-builtins:' || sqlc.arg(cluster_id)::text, 0));

-- name: LockBuiltinAssetsSession :exec
SELECT pg_advisory_lock(hashtextextended('astronomer-builtins:' || sqlc.arg(cluster_id)::text, 0));

-- name: EnsureBuiltinSystemProject :exec
INSERT INTO projects (id,name,display_name,description,cluster_id,namespaces,resource_quota,managed_by)
VALUES ($1,$2,'Astronomer System','Flux-managed platform components',$3,'[]','{}','system')
ON CONFLICT (id) DO NOTHING;

-- name: GetBuiltinSystemProjectIdentity :one
SELECT cluster_id,name,managed_by FROM projects WHERE id=$1;

-- name: EnsureBuiltinCatalogSource :exec
INSERT INTO delivery_sources (
id,project_id,name,description,source_type,url,auth_mode,trust_policy,status,last_resolved_at
) VALUES (sqlc.arg(id),sqlc.arg(project_id),sqlc.arg(name),sqlc.arg(description),sqlc.arg(source_type),sqlc.arg(url),'none',sqlc.arg(trust_policy),'ready',now())
ON CONFLICT (id) DO NOTHING;

-- name: BuiltinCatalogSourceMatches :one
SELECT EXISTS (
SELECT 1 FROM delivery_sources
WHERE id=sqlc.arg(id) AND project_id=sqlc.arg(project_id) AND name=sqlc.arg(name) AND description=sqlc.arg(description)
AND source_type=sqlc.arg(source_type) AND url=sqlc.arg(url) AND auth_mode='none'
AND credential_key_id = '' AND credential_key_version=0 AND credential_epoch=0
AND ca_bundle_key_id = '' AND proxy_ref='' AND trust_policy=sqlc.arg(trust_policy)::jsonb
AND status='ready' AND last_error_code=''
)::boolean;

-- name: EnsureBuiltinComponentBundle :exec
INSERT INTO component_bundles (id,project_id,name,description)
VALUES ($1,$2,$3,$4) ON CONFLICT (id) DO NOTHING;

-- name: GetBuiltinComponentBundleProject :one
SELECT project_id FROM component_bundles WHERE id=$1;

-- name: EnsureBuiltinComponentVersion :exec
INSERT INTO component_bundle_versions (
id,bundle_id,source_id,version,renderer,scope,requested_revision,resolved_revision,
artifact_digest,source_spec,renderer_spec,reconciliation_policy,health_policy,
requirements,dependency_bundle_ids,spec_digest,verification_status,
verification_identity,state
) VALUES ($1,$2,$3,$4,'helm','platform',$5,$5,$6,$7,$8,$9,'{}',$10,'[]',$11,$12,$13,'ready')
ON CONFLICT (id) DO NOTHING;

-- name: GetBuiltinComponentVersionIdentity :one
SELECT bundle_id,spec_digest,state FROM component_bundle_versions WHERE id=$1;

-- name: EnsureBuiltinComponentTarget :one
INSERT INTO delivery_targets (
id,project_id,name,description,bundle_version_id,placement,rollout_policy,
reconciliation_policy,maintenance_window_policy,suspended
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'{}',false)
ON CONFLICT (id) DO UPDATE SET
bundle_version_id=EXCLUDED.bundle_version_id,
placement=EXCLUDED.placement,
rollout_policy=EXCLUDED.rollout_policy,
reconciliation_policy=EXCLUDED.reconciliation_policy,
generation=delivery_targets.generation + CASE
WHEN delivery_targets.bundle_version_id<>EXCLUDED.bundle_version_id THEN 1 ELSE 0 END,
resource_version=delivery_targets.resource_version + CASE
WHEN delivery_targets.bundle_version_id<>EXCLUDED.bundle_version_id THEN 1 ELSE 0 END,
updated_at=CASE WHEN delivery_targets.bundle_version_id<>EXCLUDED.bundle_version_id THEN now() ELSE delivery_targets.updated_at END
RETURNING generation;

-- name: GetBuiltinLatestTargetRollout :one
SELECT state,created_at FROM delivery_rollouts
WHERE target_id=$1 ORDER BY created_at DESC,id DESC LIMIT 1;

-- name: GetBuiltinTargetDeployment :one
SELECT phase,last_error_code FROM cluster_deployments
WHERE target_id=$1 AND cluster_id=$2;

-- name: GetBuiltinRolloutAttempt :one
SELECT state, count(*) OVER ()::integer AS attempt_count, created_at
FROM delivery_rollouts WHERE target_id=$1
ORDER BY created_at DESC,id DESC LIMIT 1;
