-- Flux-native delivery persistence. Public source projections intentionally
-- omit encrypted credentials and private CA material.

-- name: CreateDeliverySource :one
INSERT INTO delivery_sources (
    project_id, name, description, source_type, url, auth_mode,
    credential_encrypted, credential_key_version, credential_epoch,
    ca_bundle_encrypted, proxy_ref, trust_policy, created_by, updated_by
) VALUES (
    sqlc.arg(project_id), sqlc.arg(name), sqlc.arg(description),
    sqlc.arg(source_type), sqlc.arg(url), sqlc.arg(auth_mode),
    sqlc.arg(credential_encrypted), sqlc.arg(credential_key_version),
    sqlc.arg(credential_epoch), sqlc.arg(ca_bundle_encrypted),
    sqlc.arg(proxy_ref), sqlc.arg(trust_policy), sqlc.narg(created_by),
    sqlc.narg(updated_by)
)
RETURNING id, project_id, name, description, source_type, url, auth_mode,
          credential_key_version, credential_epoch, proxy_ref, trust_policy,
          status, last_resolved_at, last_error_code, created_by, updated_by,
          created_at, updated_at;

-- name: GetDeliverySource :one
SELECT id, project_id, name, description, source_type, url, auth_mode,
       credential_key_version, credential_epoch, proxy_ref, trust_policy,
       status, last_resolved_at, last_error_code, created_by, updated_by,
       created_at, updated_at
FROM delivery_sources
WHERE id = sqlc.arg(id) AND project_id = sqlc.arg(project_id);

-- name: ListDeliverySources :many
SELECT id, project_id, name, description, source_type, url, auth_mode,
       credential_key_version, credential_epoch, proxy_ref, trust_policy,
       status, last_resolved_at, last_error_code, created_by, updated_by,
       created_at, updated_at
FROM delivery_sources
WHERE project_id = sqlc.arg(project_id)
  AND (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(query_limit) OFFSET sqlc.arg(query_offset);

-- name: CountDeliverySources :one
SELECT count(*)
FROM delivery_sources
WHERE project_id = sqlc.arg(project_id)
  AND (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text);

-- name: GetDeliverySourceSecret :one
SELECT id, project_id, source_type, url, auth_mode, credential_encrypted,
       credential_key_version, credential_epoch, ca_bundle_encrypted,
       proxy_ref, trust_policy, status
FROM delivery_sources
WHERE id = sqlc.arg(id);

-- name: UpdateDeliverySourceStatus :one
UPDATE delivery_sources
SET status = sqlc.arg(status),
    last_resolved_at = sqlc.narg(last_resolved_at),
    last_error_code = sqlc.arg(last_error_code),
    updated_by = sqlc.narg(updated_by)
WHERE id = sqlc.arg(id)
RETURNING id, project_id, name, description, source_type, url, auth_mode,
          credential_key_version, credential_epoch, proxy_ref, trust_policy,
          status, last_resolved_at, last_error_code, created_by, updated_by,
          created_at, updated_at;

-- name: RotateDeliverySourceCredential :one
UPDATE delivery_sources
SET auth_mode = sqlc.arg(auth_mode),
    credential_encrypted = sqlc.arg(credential_encrypted),
    credential_key_version = sqlc.arg(credential_key_version),
    credential_epoch = credential_epoch + 1,
    status = 'pending',
    last_error_code = '',
    updated_by = sqlc.narg(updated_by)
WHERE id = sqlc.arg(id) AND project_id = sqlc.arg(project_id)
RETURNING id, project_id, name, description, source_type, url, auth_mode,
          credential_key_version, credential_epoch, proxy_ref, trust_policy,
          status, last_resolved_at, last_error_code, created_by, updated_by,
          created_at, updated_at;

-- name: RevokeDeliverySource :one
UPDATE delivery_sources
SET credential_encrypted = '', credential_key_version = 0,
    credential_epoch = credential_epoch + 1, status = 'revoked',
    last_error_code = 'credential_revoked', updated_by = sqlc.narg(updated_by)
WHERE id = sqlc.arg(id) AND project_id = sqlc.arg(project_id)
RETURNING id, project_id, name, description, source_type, url, auth_mode,
          credential_key_version, credential_epoch, proxy_ref, trust_policy,
          status, last_resolved_at, last_error_code, created_by, updated_by,
          created_at, updated_at;

-- name: DeleteDeliverySource :execrows
DELETE FROM delivery_sources
WHERE id = sqlc.arg(id) AND project_id = sqlc.arg(project_id);

-- name: UpdateDeliverySource :one
UPDATE delivery_sources
SET description = sqlc.arg(description),
    url = sqlc.arg(url),
    proxy_ref = sqlc.arg(proxy_ref),
    trust_policy = sqlc.arg(trust_policy),
    ca_bundle_encrypted = CASE WHEN sqlc.arg(replace_ca_bundle)::boolean THEN sqlc.arg(ca_bundle_encrypted) ELSE ca_bundle_encrypted END,
    updated_by = sqlc.narg(updated_by)
WHERE id = sqlc.arg(id) AND project_id = sqlc.arg(project_id)
RETURNING id, project_id, name, description, source_type, url, auth_mode,
          credential_key_version, credential_epoch, proxy_ref, trust_policy,
          status, last_resolved_at, last_error_code, created_by, updated_by,
          created_at, updated_at;

-- name: CreateComponentBundle :one
INSERT INTO component_bundles (project_id, name, description, created_by, updated_by)
VALUES (sqlc.arg(project_id), sqlc.arg(name), sqlc.arg(description),
        sqlc.narg(created_by), sqlc.narg(updated_by))
RETURNING *;

-- name: GetComponentBundle :one
SELECT * FROM component_bundles
WHERE id = sqlc.arg(id) AND project_id = sqlc.arg(project_id);

-- name: ListComponentBundles :many
SELECT * FROM component_bundles
WHERE project_id = sqlc.arg(project_id)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(query_limit) OFFSET sqlc.arg(query_offset);

-- name: CountComponentBundles :one
SELECT count(*) FROM component_bundles WHERE project_id = sqlc.arg(project_id);

-- name: UpdateComponentBundle :one
UPDATE component_bundles
SET description = sqlc.arg(description),
    updated_by = sqlc.narg(updated_by)
WHERE id = sqlc.arg(id) AND project_id = sqlc.arg(project_id)
RETURNING *;

-- name: DeleteComponentBundle :execrows
DELETE FROM component_bundles
WHERE id = sqlc.arg(id) AND project_id = sqlc.arg(project_id);

-- name: CreateComponentBundleVersion :one
INSERT INTO component_bundle_versions (
    bundle_id, source_id, version, renderer, scope, requested_revision,
    source_spec, renderer_spec, reconciliation_policy, health_policy,
    requirements, dependency_bundle_ids, spec_digest, created_by
) VALUES (
    sqlc.arg(bundle_id), sqlc.arg(source_id), sqlc.arg(version),
    sqlc.arg(renderer), sqlc.arg(scope), sqlc.arg(requested_revision),
    sqlc.arg(source_spec), sqlc.arg(renderer_spec),
    sqlc.arg(reconciliation_policy), sqlc.arg(health_policy),
    sqlc.arg(requirements), sqlc.arg(dependency_bundle_ids),
    sqlc.arg(spec_digest), sqlc.narg(created_by)
)
RETURNING *;

-- name: GetComponentBundleVersion :one
SELECT bv.*
FROM component_bundle_versions bv
JOIN component_bundles b ON b.id = bv.bundle_id
WHERE bv.id = sqlc.arg(id) AND b.project_id = sqlc.arg(project_id);

-- name: ListComponentBundleVersions :many
SELECT * FROM component_bundle_versions
WHERE bundle_id = sqlc.arg(bundle_id)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(query_limit) OFFSET sqlc.arg(query_offset);

-- name: ResolveComponentBundleVersion :one
UPDATE component_bundle_versions
SET resolved_revision = sqlc.arg(resolved_revision),
    artifact_digest = sqlc.arg(artifact_digest),
	source_spec = sqlc.arg(source_spec),
	spec_digest = sqlc.arg(spec_digest),
    verification_status = sqlc.arg(verification_status),
    verification_identity = sqlc.arg(verification_identity),
    state = 'ready', last_error_code = ''
WHERE id = sqlc.arg(id) AND state = 'resolving'
RETURNING *;

-- name: FailComponentBundleVersion :one
UPDATE component_bundle_versions
SET verification_status = sqlc.arg(verification_status),
    state = 'failed', last_error_code = sqlc.arg(last_error_code)
WHERE id = sqlc.arg(id) AND state = 'resolving'
RETURNING *;

-- name: CreateDeliverySourceResolutionAndOutbox :one
WITH resolution AS (
	INSERT INTO delivery_source_resolutions (source_id, bundle_version_id, requested_revision, chart_name)
	VALUES (sqlc.arg(source_id), sqlc.narg(bundle_version_id), sqlc.arg(requested_revision), sqlc.arg(chart_name))
	RETURNING *
), queued AS (
	INSERT INTO task_outbox (
		dedupe_key, task_type, payload, queue_name, max_retry,
		timeout_seconds, unique_seconds, max_delivery_attempts, next_attempt_at
	)
	SELECT 'delivery-source-resolution:' || id::text,
	       'delivery:source-resolve',
	       convert_to(jsonb_build_object('resolution_id', id)::text, 'UTF8'),
	       'default', 8, 180, 1, 20, now()
	FROM resolution
	RETURNING id
)
SELECT resolution.*
FROM resolution
CROSS JOIN (SELECT count(*) FROM queued) queued_count;

-- name: ClaimDeliverySourceResolutions :many
WITH candidates AS (
    SELECT id
    FROM delivery_source_resolutions
	WHERE ((status = 'pending' AND next_attempt_at <= now())
	       OR (status = 'running' AND lease_expires_at <= now()))
    ORDER BY created_at, id
    FOR UPDATE SKIP LOCKED
    LIMIT sqlc.arg(claim_limit)
)
UPDATE delivery_source_resolutions r
SET status = 'running', started_at = COALESCE(started_at, now()),
	resolution_attempt = resolution_attempt + 1,
	lease_owner = sqlc.arg(lease_owner),
	lease_expires_at = now() + sqlc.arg(lease_duration)::interval,
	fence = fence + 1
FROM candidates c
WHERE r.id = c.id
RETURNING r.*;

-- name: ClaimDeliverySourceResolution :one
UPDATE delivery_source_resolutions
SET status = 'running', started_at = COALESCE(started_at, now()),
	resolution_attempt = resolution_attempt + 1,
	lease_owner = sqlc.arg(lease_owner),
	lease_expires_at = now() + sqlc.arg(lease_duration)::interval,
	fence = fence + 1
WHERE id = sqlc.arg(id)
	AND ((status = 'pending' AND next_attempt_at <= now())
	     OR (status = 'running' AND lease_expires_at <= now()))
RETURNING *;

-- name: CompleteDeliverySourceResolution :one
UPDATE delivery_source_resolutions
SET resolved_revision = sqlc.arg(resolved_revision),
    artifact_digest = sqlc.arg(artifact_digest),
    verification_status = sqlc.arg(verification_status),
    verification_identity = sqlc.arg(verification_identity),
	status = 'succeeded', error_code = '', completed_at = now(),
	lease_owner = '', lease_expires_at = NULL
WHERE id = sqlc.arg(id) AND status = 'running'
	AND lease_owner = sqlc.arg(expected_lease_owner)
	AND fence = sqlc.arg(expected_fence) AND lease_expires_at > now()
RETURNING *;

-- name: FailDeliverySourceResolution :one
UPDATE delivery_source_resolutions
SET verification_status = sqlc.arg(verification_status),
	status = 'failed', error_code = sqlc.arg(error_code), completed_at = now(),
	lease_owner = '', lease_expires_at = NULL
WHERE id = sqlc.arg(id) AND status = 'running'
	AND lease_owner = sqlc.arg(expected_lease_owner)
	AND fence = sqlc.arg(expected_fence) AND lease_expires_at > now()
RETURNING *;

-- name: RetryDeliverySourceResolution :one
UPDATE delivery_source_resolutions
SET status = 'pending', verification_status = 'pending',
	error_code = sqlc.arg(error_code), next_attempt_at = sqlc.arg(next_attempt_at),
	lease_owner = '', lease_expires_at = NULL
WHERE id = sqlc.arg(id) AND status = 'running'
	AND lease_owner = sqlc.arg(expected_lease_owner)
	AND fence = sqlc.arg(expected_fence) AND lease_expires_at > now()
RETURNING *;

-- name: GetDeliverySourceResolutionWork :one
SELECT r.*, s.project_id, s.name AS source_name, s.description AS source_description,
	s.source_type, s.url, s.auth_mode, s.credential_encrypted,
	s.credential_key_version, s.ca_bundle_encrypted, s.proxy_ref, s.trust_policy,
	bv.renderer, bv.scope, bv.renderer_spec, bv.reconciliation_policy,
	bv.requirements, bv.dependency_bundle_ids
FROM delivery_source_resolutions r
JOIN delivery_sources s ON s.id = r.source_id
LEFT JOIN component_bundle_versions bv ON bv.id = r.bundle_version_id
WHERE r.id = sqlc.arg(id) AND r.status = 'running'
	AND r.lease_owner = sqlc.arg(expected_lease_owner)
	AND r.fence = sqlc.arg(expected_fence) AND r.lease_expires_at > now();

-- name: CreateDeliveryTarget :one
INSERT INTO delivery_targets (
    project_id, name, description, bundle_version_id, placement, rollout_policy,
    reconciliation_policy, maintenance_window_policy, suspended, created_by, updated_by
) VALUES (
    sqlc.arg(project_id), sqlc.arg(name), sqlc.arg(description),
    sqlc.arg(bundle_version_id), sqlc.arg(placement), sqlc.arg(rollout_policy),
    sqlc.arg(reconciliation_policy), sqlc.arg(maintenance_window_policy),
    sqlc.arg(suspended), sqlc.narg(created_by), sqlc.narg(updated_by)
)
RETURNING *;

-- name: GetDeliveryTarget :one
SELECT * FROM delivery_targets
WHERE id = sqlc.arg(id) AND project_id = sqlc.arg(project_id);

-- name: GetDeliveryTargetByName :one
SELECT * FROM delivery_targets
WHERE project_id = sqlc.arg(project_id) AND name = sqlc.arg(name);

-- name: ListDeliveryTargets :many
SELECT * FROM delivery_targets
WHERE project_id = sqlc.arg(project_id)
  AND deletion_state <> 'deleted'
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(query_limit) OFFSET sqlc.arg(query_offset);

-- name: CountDeliveryTargets :one
SELECT count(*) FROM delivery_targets
WHERE project_id = sqlc.arg(project_id) AND deletion_state <> 'deleted';

-- name: GetDeliveryTargetByBundleVersion :one
SELECT t.*
FROM delivery_targets t
JOIN component_bundle_versions bv ON bv.id = t.bundle_version_id
JOIN component_bundles b ON b.id = bv.bundle_id
WHERE t.id = sqlc.arg(id) AND t.project_id = sqlc.arg(project_id)
  AND bv.id = sqlc.arg(bundle_version_id) AND b.project_id = t.project_id;

-- name: UpdateDeliveryTargetCAS :one
UPDATE delivery_targets
SET description = sqlc.arg(description),
    bundle_version_id = sqlc.arg(bundle_version_id),
    placement = sqlc.arg(placement),
    rollout_policy = sqlc.arg(rollout_policy),
    reconciliation_policy = sqlc.arg(reconciliation_policy),
    maintenance_window_policy = sqlc.arg(maintenance_window_policy),
    suspended = sqlc.arg(suspended),
    generation = generation + 1,
    resource_version = resource_version + 1,
    updated_by = sqlc.narg(updated_by)
WHERE id = sqlc.arg(id) AND project_id = sqlc.arg(project_id)
  AND resource_version = sqlc.arg(expected_resource_version)
  AND deletion_state = 'active'
RETURNING *;

-- name: MarkDeliveryTargetDeleting :one
UPDATE delivery_targets
SET deletion_state = 'deleting', generation = generation + 1,
    resource_version = resource_version + 1, updated_by = sqlc.narg(updated_by)
WHERE id = sqlc.arg(id) AND project_id = sqlc.arg(project_id)
  AND resource_version = sqlc.arg(expected_resource_version)
  AND deletion_state = 'active'
RETURNING *;

-- name: MarkDeliveryTargetOrphaned :one
WITH orphaned AS (
    UPDATE delivery_targets t
    SET deletion_state = 'deleted', resource_version = resource_version + 1,
        updated_by = sqlc.narg(updated_by)
    WHERE t.id = sqlc.arg(id) AND t.project_id = sqlc.arg(project_id)
      AND t.resource_version = sqlc.arg(expected_resource_version)
      AND t.deletion_state = 'deleting'
    RETURNING t.*
), retained AS (
    UPDATE cluster_deployments d
    SET phase = 'removed', last_error_code = 'orphaned_by_operator',
        last_message = ''
    FROM orphaned t
    WHERE d.target_id = t.id AND d.phase <> 'removed'
    RETURNING d.id
)
SELECT t.* FROM orphaned t;

-- name: FinalizeDeliveryTargetDeletionIfComplete :one
UPDATE delivery_targets t
SET deletion_state = 'deleted', resource_version = resource_version + 1
WHERE t.id = sqlc.arg(target_id) AND t.deletion_state = 'deleting'
  AND NOT EXISTS (
      SELECT 1 FROM cluster_deployments d
      WHERE d.target_id = t.id AND d.phase <> 'removed'
  )
RETURNING t.*;

-- name: RequestDeliveryTargetDeletion :many
UPDATE cluster_deployments d
SET action = 'delete', phase = 'pending', desired_generation = desired_generation + 1,
    last_error_code = '', last_message = ''
FROM delivery_targets t
WHERE d.target_id = t.id AND t.id = sqlc.arg(target_id)
  AND t.project_id = sqlc.arg(project_id) AND d.phase <> 'removed'
RETURNING d.*;

-- name: RequestDeliveryTargetDeletionCAS :one
WITH changed_target AS (
    UPDATE delivery_targets t
    SET deletion_state = 'deleting', generation = generation + 1,
        resource_version = resource_version + 1, updated_by = sqlc.narg(updated_by)
    WHERE t.id = sqlc.arg(id) AND t.project_id = sqlc.arg(project_id)
      AND t.resource_version = sqlc.arg(expected_resource_version)
      AND t.deletion_state = 'active'
    RETURNING t.*
), changed_deployments AS (
    UPDATE cluster_deployments d
    SET action = 'delete', phase = 'pending', desired_generation = desired_generation + 1,
        last_error_code = '', last_message = ''
    FROM changed_target t
    WHERE d.target_id = t.id AND d.phase <> 'removed'
    RETURNING d.id
)
SELECT t.*, (SELECT count(*) FROM changed_deployments)::bigint AS deployment_count
FROM changed_target t;

-- name: CreateDeliveryRollout :one
INSERT INTO delivery_rollouts (
    id, target_id, target_generation, from_bundle_version_id, to_bundle_version_id,
    placement_digest, placement_snapshot, strategy, strategy_digest,
    approval_policy, request_digest, plan_digest, frozen_plan, state,
    idempotency_key, progress_deadline, initiated_by
) VALUES (
    sqlc.arg(id), sqlc.arg(target_id), sqlc.arg(target_generation), sqlc.narg(from_bundle_version_id),
    sqlc.arg(to_bundle_version_id), sqlc.arg(placement_digest),
    sqlc.arg(placement_snapshot), sqlc.arg(strategy), sqlc.arg(strategy_digest),
    sqlc.arg(approval_policy), sqlc.arg(request_digest), sqlc.arg(plan_digest),
    sqlc.arg(frozen_plan), sqlc.arg(state), sqlc.arg(idempotency_key),
    sqlc.narg(progress_deadline), sqlc.narg(initiated_by)
)
RETURNING *;

-- name: GetDeliveryRolloutByIdempotency :one
SELECT * FROM delivery_rollouts
WHERE target_id = sqlc.arg(target_id) AND idempotency_key = sqlc.arg(idempotency_key);

-- name: GetDeliveryPlanningSnapshot :one
SELECT t.id AS target_id, t.project_id, t.bundle_version_id, t.placement,
       t.rollout_policy, t.maintenance_window_policy, t.generation,
       t.suspended, t.deletion_state, p.cluster_id AS owner_cluster_id,
       bv.spec_digest, bv.source_spec, bv.requirements, bv.state AS bundle_state
FROM delivery_targets t
JOIN projects p ON p.id = t.project_id
JOIN component_bundle_versions bv ON bv.id = t.bundle_version_id
WHERE t.id = sqlc.arg(target_id)
FOR UPDATE OF t;

-- name: ListDeliveryPlanningCandidates :many
SELECT DISTINCT ON (c.id)
       c.id AS cluster_id, p.id AS project_id, c.name AS cluster_name,
       c.labels, c.group_id, c.status, c.decommissioned_at,
       EXISTS (
           SELECT 1 FROM agent_connections ac
           WHERE ac.cluster_id = c.id AND ac.status = 'connected'
             AND ac.disconnected_at IS NULL
       ) AS connected,
       COALESCE(i.compatibility_status, 'unknown')::text AS compatibility_status,
       COALESCE(i.error_code, 'controller_inventory_missing')::text AS compatibility_reason,
       COALESCE(i.flux_version, '')::text AS flux_version,
       COALESCE(i.components, '{}')::jsonb AS components,
       d.desired_bundle_version_id AS previous_bundle_version_id,
       d.desired_generation AS previous_generation,
       previous.spec_digest AS previous_spec_digest,
       previous.source_spec AS previous_source_spec
FROM projects p
JOIN clusters c ON c.id = p.cluster_id
LEFT JOIN delivery_controller_inventory i ON i.cluster_id = c.id
LEFT JOIN cluster_deployments d
       ON d.target_id = sqlc.arg(target_id) AND d.cluster_id = c.id
      AND d.phase = 'ready'
LEFT JOIN component_bundle_versions previous ON previous.id = d.desired_bundle_version_id
WHERE p.id = ANY(sqlc.arg(project_ids)::uuid[])
ORDER BY c.id, (p.id = sqlc.arg(owner_project_id)) DESC, p.id;

-- name: GetDeliveryRollout :one
SELECT r.*
FROM delivery_rollouts r
JOIN delivery_targets t ON t.id = r.target_id
WHERE r.id = sqlc.arg(id) AND t.project_id = sqlc.arg(project_id);

-- name: ListDeliveryRollouts :many
SELECT r.*
FROM delivery_rollouts r
JOIN delivery_targets t ON t.id = r.target_id
WHERE t.project_id = sqlc.arg(project_id)
  AND (sqlc.narg(state)::text IS NULL OR r.state = sqlc.narg(state)::text)
ORDER BY r.created_at DESC, r.id DESC
LIMIT sqlc.arg(query_limit) OFFSET sqlc.arg(query_offset);

-- name: CountDeliveryRollouts :one
SELECT count(*)
FROM delivery_rollouts r
JOIN delivery_targets t ON t.id = r.target_id
WHERE t.project_id = sqlc.arg(project_id)
  AND (sqlc.narg(state)::text IS NULL OR r.state = sqlc.narg(state)::text);

-- name: GetDeliveryRolloutForAction :one
SELECT r.*
FROM delivery_rollouts r
JOIN delivery_targets t ON t.id = r.target_id
WHERE r.id = sqlc.arg(id) AND t.project_id = sqlc.arg(project_id)
FOR UPDATE OF r;

-- name: TransitionDeliveryRolloutCAS :one
UPDATE delivery_rollouts
SET state = sqlc.arg(to_state), fencing_generation = fencing_generation + 1,
    lease_owner = '', lease_expires_at = NULL,
    started_at = CASE WHEN sqlc.arg(to_state) = 'progressing' THEN COALESCE(started_at, now()) ELSE started_at END,
    completed_at = CASE WHEN sqlc.arg(to_state) IN ('aborted','rejected','rolled_back') THEN now() ELSE NULL END,
    last_error_code = sqlc.arg(reason_code)
WHERE id = sqlc.arg(id)
  AND fencing_generation = sqlc.arg(expected_fence)
  AND state = ANY(sqlc.arg(allowed_states)::text[])
RETURNING *;

-- name: ResetRetryableDeliveryRolloutClusters :execrows
UPDATE delivery_rollout_clusters
SET state = 'pending', assignment_action = 'apply', attempt = attempt + 1,
    fence = fence + 1, released_at = NULL, acknowledged_at = NULL,
    ready_at = NULL, completed_at = NULL, last_error_code = ''
WHERE rollout_id = sqlc.arg(rollout_id) AND state IN ('failed','timed_out','blocked');

-- name: ListDeliveryRolloutClusters :many
SELECT rc.*
FROM delivery_rollout_clusters rc
JOIN delivery_rollouts r ON r.id = rc.rollout_id
JOIN delivery_targets t ON t.id = r.target_id
WHERE rc.rollout_id = sqlc.arg(rollout_id) AND t.project_id = sqlc.arg(project_id)
ORDER BY rc.release_order, rc.cluster_id
LIMIT sqlc.arg(query_limit) OFFSET sqlc.arg(query_offset);

-- name: CountDeliveryRolloutClusters :one
SELECT count(*)
FROM delivery_rollout_clusters rc
JOIN delivery_rollouts r ON r.id = rc.rollout_id
JOIN delivery_targets t ON t.id = r.target_id
WHERE rc.rollout_id = sqlc.arg(rollout_id) AND t.project_id = sqlc.arg(project_id);

-- name: ListDeliveryRolloutEvents :many
SELECT e.*
FROM delivery_rollout_events e
JOIN delivery_rollouts r ON r.id = e.rollout_id
JOIN delivery_targets t ON t.id = r.target_id
WHERE e.rollout_id = sqlc.arg(rollout_id) AND t.project_id = sqlc.arg(project_id)
ORDER BY e.occurred_at DESC, e.id DESC
LIMIT sqlc.arg(query_limit) OFFSET sqlc.arg(query_offset);

-- name: CountDeliveryRolloutEvents :one
SELECT count(*)
FROM delivery_rollout_events e
JOIN delivery_rollouts r ON r.id = e.rollout_id
JOIN delivery_targets t ON t.id = r.target_id
WHERE e.rollout_id = sqlc.arg(rollout_id) AND t.project_id = sqlc.arg(project_id);

-- name: CreateDeliveryRolloutCluster :one
INSERT INTO delivery_rollout_clusters (
    rollout_id, cluster_id, cohort, release_order, previous_bundle_version_id,
    desired_bundle_version_id, desired_spec_digest, deadline
) VALUES (
    sqlc.arg(rollout_id), sqlc.arg(cluster_id), sqlc.arg(cohort),
    sqlc.arg(release_order), sqlc.narg(previous_bundle_version_id),
    sqlc.arg(desired_bundle_version_id), sqlc.arg(desired_spec_digest),
    sqlc.narg(deadline)
)
ON CONFLICT (rollout_id, cluster_id) DO NOTHING
RETURNING *;

-- name: ClaimDeliveryRollout :one
UPDATE delivery_rollouts
SET lease_owner = sqlc.arg(lease_owner),
    lease_expires_at = now() + sqlc.arg(lease_duration)::interval,
    fencing_generation = fencing_generation + 1
WHERE id = sqlc.arg(id)
  AND (state IN ('resolving','awaiting_approval','queued','progressing','rolling_back')
       OR (state = 'failed' AND strategy->>'on_failure' = 'rollback'))
  AND (lease_expires_at IS NULL OR lease_expires_at <= now())
RETURNING *;

-- name: ClaimDeliveryRollouts :many
WITH candidates AS (
    SELECT id
    FROM delivery_rollouts
    WHERE (state IN ('resolving','awaiting_approval','queued','progressing','rolling_back')
           OR (state = 'failed' AND strategy->>'on_failure' = 'rollback'))
      AND (lease_expires_at IS NULL OR lease_expires_at <= now())
    ORDER BY updated_at, id
    FOR UPDATE SKIP LOCKED
    LIMIT sqlc.arg(claim_limit)
)
UPDATE delivery_rollouts r
SET lease_owner = sqlc.arg(lease_owner),
    lease_expires_at = now() + sqlc.arg(lease_duration)::interval,
    fencing_generation = fencing_generation + 1
FROM candidates c
WHERE r.id = c.id
RETURNING r.*;

-- name: GetClaimedDeliveryRolloutForUpdate :one
SELECT * FROM delivery_rollouts
WHERE id = sqlc.arg(id) AND lease_owner = sqlc.arg(lease_owner)
  AND fencing_generation = sqlc.arg(expected_fence)
  AND lease_expires_at > now()
FOR UPDATE;

-- name: ListDeliveryRolloutRuntime :many
SELECT rc.*, c.labels,
       EXISTS (
           SELECT 1 FROM agent_connections ac
           WHERE ac.cluster_id = rc.cluster_id AND ac.status = 'connected'
             AND ac.disconnected_at IS NULL
       ) AS connected,
       d.id AS deployment_id, d.desired_generation, d.observed_generation,
       d.phase AS deployment_phase
FROM delivery_rollout_clusters rc
JOIN clusters c ON c.id = rc.cluster_id
LEFT JOIN cluster_deployments d
       ON d.target_id = (SELECT target_id FROM delivery_rollouts WHERE id = rc.rollout_id)
      AND d.cluster_id = rc.cluster_id
WHERE rc.rollout_id = sqlc.arg(rollout_id)
ORDER BY rc.release_order, rc.cluster_id;

-- name: ListDeliveryRolloutApprovals :many
SELECT * FROM delivery_rollout_approvals
WHERE rollout_id = sqlc.arg(rollout_id)
ORDER BY cohort, decided_at, id;

-- name: CreateDeliveryRolloutApproval :one
INSERT INTO delivery_rollout_approvals (
    rollout_id, cohort, binding_digest, decision, decided_by, expires_at
) VALUES (
    sqlc.arg(rollout_id), sqlc.arg(cohort), sqlc.arg(binding_digest),
    sqlc.arg(decision), sqlc.narg(decided_by), sqlc.arg(expires_at)
)
RETURNING *;

-- name: ApplyDeliveryRolloutClusterTransitionCAS :one
UPDATE delivery_rollout_clusters rc
SET state = sqlc.arg(to_state)::text,
    fence = fence + 1,
    ready_at = CASE WHEN sqlc.arg(to_state)::text IN ('ready','ready_previous') THEN COALESCE(ready_at, now()) ELSE ready_at END,
    completed_at = CASE WHEN sqlc.arg(to_state)::text IN ('ready','failed','skipped','timed_out','ready_previous') THEN now() ELSE completed_at END,
    last_error_code = sqlc.arg(last_error_code)
FROM delivery_rollouts r
WHERE rc.id = sqlc.arg(id) AND rc.rollout_id = r.id
  AND rc.state = sqlc.arg(from_state) AND rc.fence = sqlc.arg(expected_cluster_fence)
  AND r.lease_owner = sqlc.arg(expected_lease_owner)
  AND r.fencing_generation = sqlc.arg(expected_rollout_fence)
  AND r.lease_expires_at > now()
RETURNING rc.*;

-- name: ReleaseDeliveryRolloutClusterCAS :one
UPDATE delivery_rollout_clusters rc
SET state = sqlc.arg(to_state)::text, assignment_action = sqlc.arg(assignment_action),
    attempt = attempt + 1, fence = fence + 1, deadline = sqlc.arg(deadline),
    released_at = COALESCE(released_at, now()),
    last_error_code = ''
FROM delivery_rollouts r
WHERE rc.id = sqlc.arg(id) AND rc.rollout_id = r.id
  AND rc.state = sqlc.arg(from_state) AND rc.fence = sqlc.arg(expected_cluster_fence)
  AND r.lease_owner = sqlc.arg(expected_lease_owner)
  AND r.fencing_generation = sqlc.arg(expected_rollout_fence)
  AND r.lease_expires_at > now()
RETURNING rc.*;

-- name: ApplyDeliveryRolloutTransitionCAS :one
UPDATE delivery_rollouts
SET state = sqlc.arg(to_state)::text, last_decision_digest = sqlc.arg(decision_digest),
    lease_owner = '', lease_expires_at = NULL,
    started_at = CASE WHEN sqlc.arg(to_state)::text = 'progressing' THEN COALESCE(started_at, now()) ELSE started_at END,
    completed_at = CASE WHEN sqlc.arg(to_state)::text IN ('rejected','aborted','succeeded','failed','rolled_back','rollback_failed') THEN now() ELSE completed_at END,
    last_error_code = sqlc.arg(last_error_code)
WHERE id = sqlc.arg(id) AND state = sqlc.arg(from_state)
  AND lease_owner = sqlc.arg(expected_lease_owner)
  AND fencing_generation = sqlc.arg(expected_fence)
  AND lease_expires_at > now()
RETURNING *;

-- name: ReleaseDeliveryRolloutLease :one
UPDATE delivery_rollouts
SET lease_owner = '', lease_expires_at = NULL,
    last_decision_digest = sqlc.arg(decision_digest)
WHERE id = sqlc.arg(id) AND lease_owner = sqlc.arg(expected_lease_owner)
  AND fencing_generation = sqlc.arg(expected_fence)
RETURNING *;

-- name: CreateDeliveryRolloutEvent :one
INSERT INTO delivery_rollout_events (
    rollout_id, cluster_id, decision_digest, event_type,
    from_state, to_state, reason_code, fence, occurred_at
) VALUES (
    sqlc.arg(rollout_id), sqlc.narg(cluster_id), sqlc.arg(decision_digest),
    sqlc.arg(event_type), sqlc.arg(from_state), sqlc.arg(to_state),
    sqlc.arg(reason_code), sqlc.arg(fence), sqlc.arg(occurred_at)
)
RETURNING *;

-- name: RecomputeDeliveryRolloutCounters :one
WITH counts AS (
    SELECT rollout_id,
           count(*)::integer AS total,
           count(*) FILTER (WHERE state IN ('ready','ready_previous'))::integer AS ready,
           count(*) FILTER (WHERE state IN ('failed','timed_out','rollback_failed'))::integer AS failed,
           count(*) FILTER (WHERE state = 'blocked')::integer AS blocked,
           count(*) FILTER (WHERE state NOT IN ('pending','blocked','skipped'))::integer AS released
    FROM delivery_rollout_clusters
    WHERE rollout_id = sqlc.arg(id)
    GROUP BY rollout_id
)
UPDATE delivery_rollouts r
SET total_clusters = c.total, ready_clusters = c.ready,
    failed_clusters = c.failed, blocked_clusters = c.blocked,
    released_clusters = c.released
FROM counts c
WHERE r.id = c.rollout_id
RETURNING r.*;

-- name: UpsertClusterDeploymentDesired :one
INSERT INTO cluster_deployments (
    target_id, cluster_id, current_rollout_id, desired_bundle_version_id,
    previous_bundle_version_id, desired_generation, desired_spec_digest,
    desired_revision, action, phase
) VALUES (
    sqlc.arg(target_id), sqlc.arg(cluster_id), sqlc.arg(current_rollout_id),
    sqlc.arg(desired_bundle_version_id), sqlc.narg(previous_bundle_version_id),
    sqlc.arg(desired_generation), sqlc.arg(desired_spec_digest),
    sqlc.arg(desired_revision), sqlc.arg(action), sqlc.arg(phase)
)
ON CONFLICT (target_id, cluster_id) DO UPDATE
SET current_rollout_id = EXCLUDED.current_rollout_id,
    previous_bundle_version_id = cluster_deployments.desired_bundle_version_id,
    desired_bundle_version_id = EXCLUDED.desired_bundle_version_id,
    desired_generation = EXCLUDED.desired_generation,
    desired_spec_digest = EXCLUDED.desired_spec_digest,
    desired_revision = EXCLUDED.desired_revision,
    action = EXCLUDED.action,
    phase = EXCLUDED.phase,
    last_error_code = '', last_message = ''
WHERE EXCLUDED.desired_generation > cluster_deployments.desired_generation
RETURNING *;

-- name: ListClusterDeliveryAssignments :many
SELECT d.*, t.project_id, bv.source_id, bv.renderer, bv.scope,
       bv.resolved_revision, bv.artifact_digest, bv.source_spec,
       bv.renderer_spec, bv.reconciliation_policy,
       s.source_type, s.url, s.auth_mode, s.credential_epoch,
       s.credential_encrypted, s.ca_bundle_encrypted,
       s.trust_policy, s.proxy_ref
FROM cluster_deployments d
JOIN delivery_targets t ON t.id = d.target_id
JOIN component_bundle_versions bv ON bv.id = d.desired_bundle_version_id
JOIN delivery_sources s ON s.id = bv.source_id
WHERE d.cluster_id = sqlc.arg(cluster_id)
  AND d.phase <> 'removed'
ORDER BY d.id;

-- name: GetClusterDeployment :one
SELECT d.*
FROM cluster_deployments d
JOIN delivery_targets t ON t.id = d.target_id
WHERE d.id = sqlc.arg(id) AND t.project_id = sqlc.arg(project_id);

-- name: GetClusterDeploymentForDeliveryStatus :one
SELECT * FROM cluster_deployments
WHERE id = sqlc.arg(id) AND cluster_id = sqlc.arg(cluster_id)
FOR UPDATE;

-- name: FenceDeliveryAgentSession :one
SELECT id FROM agent_connections
WHERE id = sqlc.arg(connection_id) AND cluster_id = sqlc.arg(cluster_id)
  AND session_id = sqlc.arg(session_id) AND status = 'connected'
FOR SHARE;

-- name: ListClusterDeployments :many
SELECT d.*
FROM cluster_deployments d
JOIN delivery_targets t ON t.id = d.target_id
WHERE t.project_id = sqlc.arg(project_id)
  AND (sqlc.narg(cluster_id)::uuid IS NULL OR d.cluster_id = sqlc.narg(cluster_id)::uuid)
  AND (sqlc.narg(phase)::text IS NULL OR d.phase = sqlc.narg(phase)::text)
ORDER BY d.updated_at DESC, d.id DESC
LIMIT sqlc.arg(query_limit) OFFSET sqlc.arg(query_offset);

-- name: CountClusterDeployments :one
SELECT count(*)
FROM cluster_deployments d
JOIN delivery_targets t ON t.id = d.target_id
WHERE t.project_id = sqlc.arg(project_id)
  AND (sqlc.narg(cluster_id)::uuid IS NULL OR d.cluster_id = sqlc.narg(cluster_id)::uuid)
  AND (sqlc.narg(phase)::text IS NULL OR d.phase = sqlc.narg(phase)::text);

-- name: GetClusterDeploymentForAction :one
SELECT d.*
FROM cluster_deployments d
JOIN delivery_targets t ON t.id = d.target_id
WHERE d.id = sqlc.arg(id) AND t.project_id = sqlc.arg(project_id)
FOR UPDATE OF d;

-- name: TransitionClusterDeploymentCAS :one
UPDATE cluster_deployments
SET action = sqlc.arg(action), phase = 'pending',
    desired_generation = desired_generation + 1,
    agent_session_id = '', agent_sequence = 0,
    last_error_code = '', last_message = ''
WHERE id = sqlc.arg(id) AND desired_generation = sqlc.arg(expected_generation)
RETURNING *;

-- name: ListClusterDeploymentEvents :many
SELECT e.*
FROM cluster_deployment_events e
JOIN cluster_deployments d ON d.id = e.deployment_id
JOIN delivery_targets t ON t.id = d.target_id
WHERE e.deployment_id = sqlc.arg(deployment_id) AND t.project_id = sqlc.arg(project_id)
ORDER BY e.created_at DESC, e.id DESC
LIMIT sqlc.arg(query_limit) OFFSET sqlc.arg(query_offset);

-- name: CountClusterDeploymentEvents :one
SELECT count(*)
FROM cluster_deployment_events e
JOIN cluster_deployments d ON d.id = e.deployment_id
JOIN delivery_targets t ON t.id = d.target_id
WHERE e.deployment_id = sqlc.arg(deployment_id) AND t.project_id = sqlc.arg(project_id);

-- name: UpdateClusterDeploymentObservedCAS :one
UPDATE cluster_deployments
SET observed_generation = sqlc.arg(observed_generation),
    observed_spec_digest = sqlc.arg(observed_spec_digest),
    observed_revision = sqlc.arg(observed_revision),
    phase = sqlc.arg(phase), conditions = sqlc.arg(conditions),
    source_kind = sqlc.arg(source_kind), source_name = sqlc.arg(source_name),
    reconciler_kind = sqlc.arg(reconciler_kind), reconciler_name = sqlc.arg(reconciler_name),
    inventory = sqlc.arg(inventory), agent_session_id = sqlc.arg(agent_session_id),
    agent_sequence = sqlc.arg(agent_sequence), last_error_code = sqlc.arg(last_error_code),
    last_message = sqlc.arg(last_message), last_observed_at = sqlc.arg(last_observed_at)
WHERE id = sqlc.arg(id)
  AND desired_generation = sqlc.arg(observed_generation)
  AND desired_spec_digest = sqlc.arg(observed_spec_digest)
  AND (agent_session_id <> sqlc.arg(agent_session_id) OR agent_sequence < sqlc.arg(agent_sequence))
RETURNING *;

-- name: AdvanceDeliveryRolloutClusterFromStatus :one
WITH current AS (
    SELECT rc.id, rc.state, r.state AS rollout_state
    FROM delivery_rollout_clusters rc
    JOIN cluster_deployments d
      ON rc.rollout_id = d.current_rollout_id AND rc.cluster_id = d.cluster_id
    JOIN delivery_rollouts r ON r.id = rc.rollout_id
    WHERE d.id = sqlc.arg(deployment_id)
    FOR UPDATE OF rc
)
UPDATE delivery_rollout_clusters rc
SET state = CASE
        WHEN sqlc.arg(phase)::text = 'ready' THEN
            CASE WHEN rc.assignment_action = 'rollback' THEN 'ready_previous' ELSE 'ready' END
        WHEN sqlc.arg(phase)::text IN ('failed','degraded') THEN 'failed'
        WHEN sqlc.arg(phase)::text = 'applying' THEN
            CASE WHEN rc.assignment_action = 'rollback' THEN 'rolling_back' ELSE 'reconciling' END
        ELSE 'acknowledged'
    END,
    fence = fence + 1,
    acknowledged_at = COALESCE(acknowledged_at, now()),
    ready_at = CASE
        WHEN sqlc.arg(phase)::text = 'ready' THEN COALESCE(ready_at, now())
        WHEN sqlc.arg(phase)::text IN ('applying','failed','degraded') THEN NULL
        ELSE ready_at
    END,
    completed_at = CASE
        WHEN sqlc.arg(phase)::text IN ('ready','failed','degraded') THEN now()
        WHEN sqlc.arg(phase)::text = 'applying' THEN NULL
        ELSE completed_at
    END,
    last_error_code = sqlc.arg(last_error_code)
FROM cluster_deployments d, current
WHERE d.id = sqlc.arg(deployment_id)
  AND rc.id = current.id
  AND rc.rollout_id = d.current_rollout_id AND rc.cluster_id = d.cluster_id
  AND ((current.rollout_state = 'progressing' AND rc.assignment_action = 'apply')
       OR (current.rollout_state = 'rolling_back' AND rc.assignment_action = 'rollback'))
  AND d.desired_generation = sqlc.arg(observed_generation)
  AND d.desired_spec_digest = sqlc.arg(observed_spec_digest)
  AND (
      (sqlc.arg(phase)::text = 'ready' AND rc.state IN ('released','acknowledged','reconciling','rolling_back'))
      OR (sqlc.arg(phase)::text IN ('failed','degraded') AND rc.state IN ('released','acknowledged','reconciling','ready','rolling_back','ready_previous'))
      OR (sqlc.arg(phase)::text = 'applying' AND rc.state IN ('released','acknowledged','ready','ready_previous'))
      OR (sqlc.arg(phase)::text NOT IN ('ready','failed','degraded','applying') AND rc.state = 'released')
  )
RETURNING rc.*, current.state::text AS from_state;

-- name: CreateClusterDeploymentEvent :one
INSERT INTO cluster_deployment_events (
    deployment_id, rollout_id, event_type, from_phase, to_phase,
    generation, spec_digest, reason_code, message, observed_at
) VALUES (
    sqlc.arg(deployment_id), sqlc.narg(rollout_id), sqlc.arg(event_type),
    sqlc.arg(from_phase), sqlc.arg(to_phase), sqlc.arg(generation),
    sqlc.arg(spec_digest), sqlc.arg(reason_code), sqlc.arg(message),
    sqlc.arg(observed_at)
)
RETURNING *;

-- name: AdvanceDeliveryAssignmentSnapshot :one
INSERT INTO delivery_assignment_receipts (
    cluster_id, desired_snapshot_generation, desired_content_digest,
    credential_content_digest, credential_epoch
) VALUES (
    sqlc.arg(cluster_id), 1, sqlc.arg(content_digest), sqlc.arg(credential_digest), 1
)
ON CONFLICT (cluster_id) DO UPDATE
SET desired_snapshot_generation = CASE
        WHEN delivery_assignment_receipts.desired_content_digest <> EXCLUDED.desired_content_digest
          OR delivery_assignment_receipts.credential_content_digest <> EXCLUDED.credential_content_digest
        THEN delivery_assignment_receipts.desired_snapshot_generation + 1
        ELSE delivery_assignment_receipts.desired_snapshot_generation
    END,
    desired_content_digest = EXCLUDED.desired_content_digest,
    desired_snapshot_etag = CASE
        WHEN delivery_assignment_receipts.desired_content_digest <> EXCLUDED.desired_content_digest
          OR delivery_assignment_receipts.credential_content_digest <> EXCLUDED.credential_content_digest
        THEN ''
        ELSE delivery_assignment_receipts.desired_snapshot_etag
    END,
    credential_epoch = CASE
        WHEN delivery_assignment_receipts.credential_content_digest <> EXCLUDED.credential_content_digest
        THEN delivery_assignment_receipts.credential_epoch + 1
        ELSE delivery_assignment_receipts.credential_epoch
    END,
    credential_content_digest = EXCLUDED.credential_content_digest
RETURNING *;

-- name: FinalizeDeliveryAssignmentSnapshot :one
UPDATE delivery_assignment_receipts
SET desired_snapshot_etag = sqlc.arg(snapshot_etag)
WHERE cluster_id = sqlc.arg(cluster_id)
  AND desired_snapshot_generation = sqlc.arg(snapshot_generation)
  AND desired_content_digest = sqlc.arg(content_digest)
RETURNING *;

-- name: GetDeliveryAssignmentReceipt :one
SELECT * FROM delivery_assignment_receipts WHERE cluster_id = sqlc.arg(cluster_id);

-- name: AcknowledgeDeliveryAssignmentSnapshot :one
UPDATE delivery_assignment_receipts
SET acknowledged_snapshot_generation = sqlc.arg(snapshot_generation),
    acknowledged_snapshot_etag = sqlc.arg(snapshot_etag),
    agent_session_id = sqlc.arg(agent_session_id),
    agent_sequence = sqlc.arg(agent_sequence),
    last_protocol_error_code = sqlc.arg(last_protocol_error_code),
    acknowledged_at = now()
WHERE cluster_id = sqlc.arg(cluster_id)
  AND desired_snapshot_generation = sqlc.arg(snapshot_generation)
  AND desired_snapshot_etag = sqlc.arg(snapshot_etag)
  AND (delivery_assignment_receipts.agent_session_id <> sqlc.arg(agent_session_id)
       OR delivery_assignment_receipts.agent_sequence < sqlc.arg(agent_sequence))
RETURNING *;

-- name: UpsertDeliveryControllerInventory :one
INSERT INTO delivery_controller_inventory (
    cluster_id, agent_version, flux_version, components, api_versions, distribution_digest,
    kubernetes_version, ready, compatibility_status, error_code, observed_at
) VALUES (
    sqlc.arg(cluster_id), sqlc.arg(agent_version), sqlc.arg(flux_version), sqlc.arg(components),
    sqlc.arg(api_versions), sqlc.arg(distribution_digest),
    sqlc.arg(kubernetes_version), sqlc.arg(ready),
    sqlc.arg(compatibility_status), sqlc.arg(error_code), sqlc.narg(observed_at)
)
ON CONFLICT (cluster_id) DO UPDATE
SET agent_version = EXCLUDED.agent_version, flux_version = EXCLUDED.flux_version, components = EXCLUDED.components,
    api_versions = EXCLUDED.api_versions, distribution_digest = EXCLUDED.distribution_digest,
    kubernetes_version = EXCLUDED.kubernetes_version, ready = EXCLUDED.ready,
    compatibility_status = EXCLUDED.compatibility_status,
    error_code = EXCLUDED.error_code, observed_at = EXCLUDED.observed_at
RETURNING *;

-- name: GetDeliveryControllerInventory :one
SELECT i.*
FROM delivery_controller_inventory i
JOIN projects p ON p.cluster_id = i.cluster_id
WHERE i.cluster_id = sqlc.arg(cluster_id) AND p.id = sqlc.arg(project_id);

-- name: CountDeliveryControllerCompatibility :many
SELECT compatibility_status, count(*) AS cluster_count
FROM delivery_controller_inventory
GROUP BY compatibility_status
ORDER BY compatibility_status;

-- name: GetCurrentDeliverySystemRollout :one
SELECT * FROM delivery_system_rollouts
WHERE state IN ('awaiting_approval','queued','progressing','paused','rolling_back')
ORDER BY created_at DESC, id DESC
LIMIT 1;
