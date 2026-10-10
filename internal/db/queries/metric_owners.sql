-- These are telemetry ownership reads, never actor authorization. Metric
-- labels use bounded verified owner IDs; names/config/endpoints are excluded.
-- Event callbacks capture a generation before work and compare it again before
-- recording. Failure to observe ownership drops telemetry, not product work.

-- name: GetMetricOwnerGeneration :one
SELECT m.generation FROM metric_owner_generations m
WHERE m.family = sqlc.arg(family)::text AND m.owner_id = sqlc.arg(owner_id)::uuid
  AND (m.family <> 'cluster' OR EXISTS (
      SELECT 1 FROM clusters c WHERE c.id = m.owner_id AND c.decommissioned_at IS NULL));

-- name: GetSSOProviderMetricIdentity :one
-- Historical session provider names are NOT themselves ownership proof.
SELECT p.id, g.generation
FROM sso_configurations p
JOIN metric_owner_generations g ON g.family = 'sso_provider' AND g.owner_id = p.id
WHERE p.provider = sqlc.arg(provider)::text;

-- name: ListMetricOwnerGenerationsPage :many
-- The consumer completes every page of one family in a single read-only
-- repeatable-read transaction and commits successfully before any retirement.
SELECT m.owner_id, m.generation
FROM metric_owner_generations m
WHERE m.family = sqlc.arg(family)::text
  AND (m.family <> 'cluster' OR EXISTS (
      SELECT 1 FROM clusters c WHERE c.id = m.owner_id AND c.decommissioned_at IS NULL))
ORDER BY m.owner_id ASC
LIMIT sqlc.arg(query_limit)::integer OFFSET sqlc.arg(query_offset)::integer;

-- name: ListClusterGroupMetricValuesPage :many
-- Direct membership semantics match the existing CountClustersInGroup; this
-- telemetry query does not change subtree semantics or tenant authorization.
SELECT g.id, m.generation,
       (SELECT COUNT(*)::bigint FROM clusters c WHERE c.group_id = g.id) AS cluster_count
FROM cluster_groups g
JOIN metric_owner_generations m ON m.family = 'cluster_group' AND m.owner_id = g.id
WHERE g.enabled = true
ORDER BY g.id ASC
LIMIT sqlc.arg(query_limit)::integer OFFSET sqlc.arg(query_offset)::integer;
