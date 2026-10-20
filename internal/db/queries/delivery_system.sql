-- Signed downstream delivery-system releases and staged rollout state.
-- Public projections deliberately omit registry_credential_{key_id,algorithm,ciphertext,nonce}.

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

-- name: RetargetLocalDeliverySystemAssignment :execrows
-- The management-plane cluster (is_local) never joins a system rollout: it
-- upgrades with the server. Its assignment is seeded once, so without this it
-- keeps targeting the release that was current at enrollment and, once the
-- embedded agent advances, sits in 'applying' forever. Move it to the
-- released release before every observation.
UPDATE delivery_system_cluster_assignments a
SET desired_release_id = r.id, previous_release_id = a.desired_release_id, rollout_id = NULL,
    generation = a.generation + 1, phase = 'released', fence = a.fence + 1,
    released_at = now(), ready_at = NULL, last_error_code = ''
FROM delivery_system_releases r, clusters c
WHERE a.cluster_id = sqlc.arg(cluster_id) AND c.id = a.cluster_id
  AND c.is_local AND c.decommissioned_at IS NULL
  AND r.state = 'released' AND a.desired_release_id <> r.id;

-- name: ObserveDeliverySystemAssignment :one
WITH seeded AS (
    INSERT INTO delivery_system_cluster_assignments (
        cluster_id, desired_release_id, generation, phase, released_at
    )
    SELECT sqlc.arg(cluster_id), r.id, r.release_sequence, 'released', now()
    FROM delivery_system_releases r
    WHERE r.state = 'released'
    ON CONFLICT (cluster_id) DO NOTHING
), updated AS (
    UPDATE delivery_system_cluster_assignments a
    SET observed_distribution_digest = sqlc.arg(observed_distribution_digest)::text,
        observed_agent_version = sqlc.arg(observed_agent_version)::text,
        last_observed_at = sqlc.arg(observed_at),
        acknowledged_at = COALESCE(acknowledged_at, sqlc.arg(observed_at)),
        -- pending (cohort not yet released by ReleaseSystemRolloutNextCohort)
        -- and aborted assignments only record the observation: promoting them
        -- would count an unreleased cluster as done and stall the engine.
        phase = CASE
            WHEN a.phase IN ('pending','aborted') THEN a.phase
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
            WHEN a.phase IN ('pending','aborted') THEN a.ready_at
            WHEN sqlc.arg(inventory_ready)::boolean
             AND sqlc.arg(observed_distribution_digest)::text = r.distribution_digest
             AND sqlc.arg(observed_agent_version)::text = r.agent_version
            THEN COALESCE(ready_at, sqlc.arg(observed_at)) ELSE NULL END,
        last_error_code = CASE
            WHEN sqlc.arg(inventory_ready)::boolean
             AND sqlc.arg(observed_distribution_digest)::text = r.distribution_digest
             AND sqlc.arg(observed_agent_version)::text = r.agent_version THEN ''
            ELSE sqlc.arg(error_code)::text END
    -- prev captures the pre-update phase inside the UPDATE itself. A separate
    -- `SELECT ... FOR UPDATE` CTE joined afterwards skips the row this same
    -- command already updated, so the statement returned no row at all.
    FROM delivery_system_releases r,
         (SELECT p.cluster_id, p.phase
          FROM delivery_system_cluster_assignments p
          WHERE p.cluster_id = sqlc.arg(cluster_id)
          FOR UPDATE) prev
    WHERE a.cluster_id = sqlc.arg(cluster_id) AND r.id = a.desired_release_id
      AND prev.cluster_id = a.cluster_id
    RETURNING a.*, prev.phase AS previous_phase
)
SELECT * FROM updated;

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
