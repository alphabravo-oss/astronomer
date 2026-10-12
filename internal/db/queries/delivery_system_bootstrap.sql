-- Trusted startup release bootstrap. Every call is bound to the same existing
-- Serializable transaction; the advisory lock is transaction-scoped.

-- name: LockDeliverySystemReleaseBootstrap :exec
SELECT pg_advisory_xact_lock(hashtextextended('astronomer-delivery-system-release', 0));

-- name: GetDeliverySystemBootstrapReleaseByVersion :one
SELECT id::text, spec_digest, state FROM delivery_system_releases WHERE version = $1;

-- name: CreateDeliverySystemBootstrapRelease :one
INSERT INTO delivery_system_releases (
    version, artifact_url, artifact_digest, distribution_digest,
    agent_version, agent_image, minimum_kubernetes, maximum_kubernetes,
    crd_storage_version, previous_storage_version, interval, timeout,
    verification_policy, spec_digest
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
RETURNING id::text, state;

-- name: CountReleasedDeliverySystems :one
SELECT count(*) FROM delivery_system_releases WHERE state='released';

-- name: PromoteDeliverySystemBootstrapRelease :exec
UPDATE delivery_system_releases
SET state='released', released_at=COALESCE(released_at,now()), retired_at=NULL
WHERE id::text=sqlc.arg(release_id)::text;
