-- name: ListClusterConditionsByStatus :many
-- Fleet-wide list of conditions in the given status, used by the
-- remediation reconciler to find work each tick. Skips decommissioned
-- clusters because their conditions are about to be deleted by the
-- decommission reconciler anyway.
SELECT c.id, c.cluster_id, c.type, c.status, c.reason, c.message,
       c.last_transition_time, c.last_probe_time, c.created_at, c.updated_at
FROM cluster_conditions c
JOIN clusters cl ON cl.id = c.cluster_id
WHERE c.status = $1 AND cl.decommissioned_at IS NULL
ORDER BY c.last_transition_time ASC;

-- name: InsertClusterConditionRemediation :one
INSERT INTO cluster_condition_remediation_attempts
    (cluster_id, condition_type, action, outcome, error, detail)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetLatestClusterConditionRemediation :one
-- Returns the most recent attempt for the given (cluster, condition_type).
-- Used by the reconciler to compute backoff before its next attempt; the
-- partial-index sort makes this O(log N).
SELECT *
FROM cluster_condition_remediation_attempts
WHERE cluster_id = $1 AND condition_type = $2
ORDER BY attempted_at DESC
LIMIT 1;

-- name: ListClusterConditionRemediationByCluster :many
-- Backs the per-cluster UI panel — shows the 50 most recent attempts so
-- operators can see the trail. 50 is enough to span ~24h at the default
-- 30s reconcile cadence for one stuck condition without becoming a
-- DB-pressure liability.
SELECT *
FROM cluster_condition_remediation_attempts
WHERE cluster_id = $1
ORDER BY attempted_at DESC
LIMIT 50;

-- name: CountClusterConditionRemediationSinceForType :one
-- Counts attempts for a (cluster, type) within a window. The reconciler
-- uses this as a daily-cap circuit breaker so a permanently-broken
-- cluster can't drive unbounded token reissuance / audit-log growth.
SELECT count(*)
FROM cluster_condition_remediation_attempts
WHERE cluster_id = $1
  AND condition_type = $2
  AND outcome <> 'skipped'
  AND attempted_at > $3;
