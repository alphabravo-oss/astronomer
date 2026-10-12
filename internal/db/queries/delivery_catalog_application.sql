-- Fixed catalog application statements. Serializable mutations bind these
-- generated queries to the existing physical transaction; planners retain
-- their existing post-commit audit/outbox ownership.

-- name: ResolveCatalogApplicationProject :one
SELECT dt.project_id FROM installed_charts ic
JOIN delivery_targets dt ON dt.id=ic.request_id
WHERE ic.id=$1;

-- name: LinkCatalogApplicationTarget :exec
UPDATE installed_charts
SET request_id=$2,status=$3,
revision=revision+CASE WHEN sqlc.arg(advance_revision)::boolean THEN 1 ELSE 0 END,
updated_at=now()
WHERE id=$1;

-- name: GetCatalogApplicationDeploymentStatus :one
SELECT t.deletion_state,COALESCE(d.phase,'') AS deployment_phase,COALESCE(d.last_error_code,'') AS last_error_code
FROM delivery_targets t
LEFT JOIN LATERAL (
SELECT phase,last_error_code FROM cluster_deployments
WHERE target_id=t.id ORDER BY updated_at DESC,id DESC LIMIT 1
) d ON true
WHERE t.id=$1;

-- name: GetCatalogApplicationTargetForDeletion :one
SELECT dt.id,dt.project_id,dt.resource_version
FROM installed_charts ic JOIN delivery_targets dt ON dt.id=ic.request_id
WHERE ic.id=$1 FOR UPDATE OF dt;

-- name: MarkCatalogApplicationUninstalling :exec
UPDATE installed_charts SET status='uninstalling',updated_at=now() WHERE id=$1;

-- name: GetCatalogApplicationTargetForRollback :one
SELECT dt.id,dt.project_id,ic.cluster_id,ic.release_name
FROM installed_charts ic
JOIN delivery_targets dt ON dt.id=ic.request_id
WHERE ic.id=$1 FOR UPDATE OF dt;

-- name: GetCatalogApplicationRollbackRevision :one
SELECT to_bundle_version_id FROM delivery_rollouts
WHERE target_id=$1 AND target_generation=$2
ORDER BY created_at DESC,id DESC LIMIT 1;

-- name: StageCatalogApplicationRollbackTarget :one
UPDATE delivery_targets SET bundle_version_id=$2,generation=generation+1,
resource_version=resource_version+1,updated_by=$3,updated_at=now()
WHERE id=$1 AND deletion_state='active' RETURNING generation;

-- name: MarkCatalogApplicationRollingBack :exec
UPDATE installed_charts SET status='rolling_back',revision=revision+1,
values_override=COALESCE((SELECT (renderer_spec #> '{helm,values}')::text
FROM component_bundle_versions WHERE component_bundle_versions.id=sqlc.arg(bundle_version_id)),values_override),updated_at=now()
WHERE installed_charts.id=$1;

-- name: ListCatalogApplicationRevisions :many
SELECT r.target_generation,r.updated_at,r.state,bv.requested_revision,ic.release_name,
(r.from_bundle_version_id IS NULL)::boolean AS is_install,r.idempotency_key
FROM installed_charts ic
JOIN delivery_targets dt ON dt.id=ic.request_id
JOIN delivery_rollouts r ON r.target_id=dt.id
JOIN component_bundle_versions bv ON bv.id=r.to_bundle_version_id
WHERE ic.id=$1 ORDER BY r.target_generation,r.created_at,r.id;

-- name: EnsureCatalogApplicationSource :exec
INSERT INTO delivery_sources
(id,project_id,name,description,source_type,url,auth_mode,trust_policy,status,last_resolved_at,created_by,updated_by)
VALUES ($1,$2,$3,$4,'helm_http',$5,'none',$6,'ready',now(),$7,$7)
ON CONFLICT (id) DO UPDATE SET last_resolved_at=now(),status='ready',last_error_code='';

-- name: EnsureCatalogApplicationBundle :exec
INSERT INTO component_bundles (id,project_id,name,description,created_by,updated_by)
VALUES ($1,$2,$3,$4,$5,$5) ON CONFLICT (id) DO NOTHING;

-- name: EnsureCatalogApplicationBundleVersion :exec
INSERT INTO component_bundle_versions (
id,bundle_id,source_id,version,renderer,scope,requested_revision,resolved_revision,
artifact_digest,source_spec,renderer_spec,reconciliation_policy,health_policy,
requirements,dependency_bundle_ids,spec_digest,verification_status,verification_identity,state,created_by
) VALUES ($1,$2,$3,$4,'helm','platform',$5,$5,$6,$7,$8,$9,'{}',$10,'[]',$11,'unsigned','upstream-catalog','ready',$12)
ON CONFLICT (id) DO NOTHING;

-- name: RecordCatalogApplicationVersion :exec
INSERT INTO delivery_application_versions (
installation_id,chart_version_id,bundle_version_id,catalog_slug,
version,artifact_digest,values_digest,verification_status
)
SELECT $1,$2,$3,b.slug,$4,$5,$6,b.verification_status
FROM helm_chart_versions v
JOIN helm_charts c ON c.id=v.chart_id
JOIN helm_repositories r ON r.id=c.repository_id
JOIN catalog_blessed_charts b
ON b.repo_url=r.url AND b.chart_name=c.name AND b.source='catalog-v1'
WHERE v.id=$2
ON CONFLICT (installation_id,chart_version_id,values_digest) DO NOTHING;

-- name: EnsureCatalogApplicationTarget :one
INSERT INTO delivery_targets (
id,project_id,name,description,bundle_version_id,placement,rollout_policy,
reconciliation_policy,maintenance_window_policy,suspended,created_by,updated_by
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'{}',false,$9,$9)
ON CONFLICT (id) DO UPDATE SET
bundle_version_id=EXCLUDED.bundle_version_id,
generation=delivery_targets.generation + CASE WHEN delivery_targets.bundle_version_id<>EXCLUDED.bundle_version_id OR sqlc.arg(force_generation)::boolean THEN 1 ELSE 0 END,
resource_version=delivery_targets.resource_version + 1,updated_at=now(),updated_by=EXCLUDED.updated_by
RETURNING generation;

-- name: GetCatalogApplicationRolloutStatus :one
SELECT r.state,r.last_error_code,t.deletion_state,COALESCE(d.last_message,'') AS last_message
FROM delivery_rollouts r
JOIN delivery_targets t ON t.id=r.target_id
LEFT JOIN LATERAL (
SELECT last_message FROM cluster_deployments
WHERE current_rollout_id=r.id ORDER BY updated_at DESC,id DESC LIMIT 1
) d ON true
WHERE r.id=$1 AND r.target_id=$2;

-- name: GetCatalogApplicationDeletionStatus :one
SELECT deletion_state FROM delivery_targets WHERE id=$1;
