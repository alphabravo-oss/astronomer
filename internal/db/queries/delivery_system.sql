-- Signed downstream delivery-system releases and staged rollout state.
-- Public projections deliberately omit registry_credential_encrypted.

-- name: ListDeliverySystemReleases :many
SELECT id, release_sequence, version, artifact_url, artifact_digest,
       distribution_digest, agent_version, agent_image, minimum_kubernetes,
       maximum_kubernetes, crd_storage_version, previous_storage_version,
       interval, timeout, verification_policy, credential_key_version,
       credential_epoch, spec_digest, state, released_at, retired_at,
       created_by, created_at
FROM delivery_system_releases
ORDER BY release_sequence DESC
LIMIT sqlc.arg(query_limit) OFFSET sqlc.arg(query_offset);

-- name: GetClusterDeliverySystemRelease :one
WITH assigned AS (
    SELECT a.desired_release_id AS release_id, a.generation, a.phase
    FROM delivery_system_cluster_assignments a
    WHERE a.cluster_id = sqlc.arg(cluster_id)
      AND a.phase IN ('released','applying','ready','rolling_back','rolled_back')
), selected AS (
    SELECT release_id, generation, phase FROM assigned
    UNION ALL
    SELECT r.id, r.release_sequence, 'released'::varchar
    FROM delivery_system_releases r
    WHERE r.state = 'released' AND NOT EXISTS (SELECT 1 FROM assigned)
    ORDER BY generation DESC
    LIMIT 1
)
SELECT r.*, selected.generation AS assignment_generation,
       selected.phase AS assignment_phase
FROM selected
JOIN delivery_system_releases r ON r.id = selected.release_id;

-- name: ObserveDeliverySystemAssignment :one
WITH seeded AS (
    INSERT INTO delivery_system_cluster_assignments (
        cluster_id, desired_release_id, generation, phase, released_at
    )
    SELECT sqlc.arg(cluster_id), r.id, r.release_sequence, 'released', now()
    FROM delivery_system_releases r
    WHERE r.state = 'released'
    ON CONFLICT (cluster_id) DO NOTHING
), current AS (
    SELECT a.cluster_id, a.phase
    FROM delivery_system_cluster_assignments a
    WHERE a.cluster_id = sqlc.arg(cluster_id)
    FOR UPDATE
), updated AS (
    UPDATE delivery_system_cluster_assignments a
    SET observed_distribution_digest = sqlc.arg(observed_distribution_digest)::text,
        observed_agent_version = sqlc.arg(observed_agent_version)::text,
        last_observed_at = sqlc.arg(observed_at),
        acknowledged_at = COALESCE(acknowledged_at, sqlc.arg(observed_at)),
        phase = CASE
            WHEN sqlc.arg(inventory_ready)::boolean
             AND sqlc.arg(observed_distribution_digest)::text = r.distribution_digest
             AND sqlc.arg(observed_agent_version)::text = r.agent_version
             AND a.phase IN ('rolling_back','rolled_back') THEN 'rolled_back'
            WHEN sqlc.arg(inventory_ready)::boolean
             AND sqlc.arg(observed_distribution_digest)::text = r.distribution_digest
             AND sqlc.arg(observed_agent_version)::text = r.agent_version THEN 'ready'
            WHEN sqlc.arg(compatibility_status)::text = 'incompatible'
             AND a.phase IN ('rolling_back','rolled_back') THEN 'rollback_failed'
            WHEN sqlc.arg(compatibility_status)::text = 'incompatible' THEN 'failed'
            WHEN a.phase = 'rolled_back' THEN a.phase
            ELSE 'applying'
        END,
        ready_at = CASE
            WHEN sqlc.arg(inventory_ready)::boolean
             AND sqlc.arg(observed_distribution_digest)::text = r.distribution_digest
             AND sqlc.arg(observed_agent_version)::text = r.agent_version
            THEN COALESCE(ready_at, sqlc.arg(observed_at)) ELSE NULL END,
        last_error_code = CASE
            WHEN sqlc.arg(inventory_ready)::boolean
             AND sqlc.arg(observed_distribution_digest)::text = r.distribution_digest
             AND sqlc.arg(observed_agent_version)::text = r.agent_version THEN ''
            ELSE sqlc.arg(error_code)::text END
    FROM delivery_system_releases r
    WHERE a.cluster_id = sqlc.arg(cluster_id) AND r.id = a.desired_release_id
    RETURNING a.*
)
SELECT updated.*, current.phase AS previous_phase
FROM updated JOIN current USING (cluster_id);

-- name: CreateDeliverySystemEvent :one
INSERT INTO delivery_system_events (
    rollout_id, cluster_id, release_id, generation, event_type,
    from_phase, to_phase, reason_code, decision_digest, occurred_at
) VALUES (
    sqlc.narg(rollout_id), sqlc.narg(cluster_id), sqlc.arg(release_id),
    sqlc.arg(generation), sqlc.arg(event_type), sqlc.arg(from_phase),
    sqlc.arg(to_phase), sqlc.arg(reason_code), sqlc.arg(decision_digest),
    sqlc.arg(occurred_at)
)
RETURNING *;
