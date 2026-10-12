-- System-rollout mutation methods run on the existing Serializable transaction.
-- The global advisory lock is transactional, never acquired through a pool.

-- name: LockSystemRolloutTransaction :exec
SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0));

-- name: GetSystemRolloutReleaseStateForUpdate :one
SELECT state FROM delivery_system_releases WHERE id=$1 FOR UPDATE;

-- name: CountActiveSystemRollouts :one
SELECT count(*) FROM delivery_system_rollouts WHERE state IN ('awaiting_approval','queued','progressing','paused','rolling_back');

-- name: GetCurrentSystemRolloutRelease :one
SELECT id FROM delivery_system_releases WHERE state='released';

-- name: ListSystemRolloutEligibleClusters :many
SELECT c.id
FROM clusters c
WHERE c.decommissioned_at IS NULL
  AND c.is_local = false
  AND c.registration_phase = 'ready'
  AND EXISTS (SELECT 1 FROM cluster_agent_tokens t WHERE t.cluster_id=c.id AND t.revoked_at IS NULL)
ORDER BY c.id;

-- name: CreateSystemRollout :one
INSERT INTO delivery_system_rollouts (
	id,release_id,previous_release_id,strategy,strategy_digest,state,total_clusters,
	idempotency_key,progress_deadline,initiated_by
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
RETURNING *;

-- name: EnsureSystemRolloutAssignment :exec
INSERT INTO delivery_system_cluster_assignments (
	cluster_id,desired_release_id,previous_release_id,rollout_id,generation,cohort,release_order,phase,deadline
) VALUES ($1,$2,$3,$4,COALESCE((SELECT generation+1 FROM delivery_system_cluster_assignments WHERE cluster_id=$1),1),$5,$6,'pending',$7)
ON CONFLICT (cluster_id) DO UPDATE SET
	desired_release_id=EXCLUDED.desired_release_id, previous_release_id=EXCLUDED.previous_release_id,
	rollout_id=EXCLUDED.rollout_id, generation=delivery_system_cluster_assignments.generation+1,
	cohort=EXCLUDED.cohort, release_order=EXCLUDED.release_order, phase='pending',
	fence=delivery_system_cluster_assignments.fence+1, released_at=NULL,
	acknowledged_at=NULL, ready_at=NULL, deadline=EXCLUDED.deadline,
	observed_distribution_digest='', observed_agent_version='', last_error_code='';

-- name: ListSystemRolloutAssignments :many
SELECT cluster_id,generation,cohort,release_order,phase,observed_distribution_digest,
       observed_agent_version,last_error_code,last_observed_at
FROM delivery_system_cluster_assignments WHERE rollout_id=$1
ORDER BY cohort,release_order,cluster_id;

-- name: RetrySystemRolloutAssignments :exec
UPDATE delivery_system_cluster_assignments SET phase='pending',last_error_code='',released_at=NULL,acknowledged_at=NULL,ready_at=NULL,fence=fence+1 WHERE rollout_id=$1 AND phase='failed';

-- name: AbortSystemRolloutPendingAssignments :exec
UPDATE delivery_system_cluster_assignments SET phase='aborted',fence=fence+1 WHERE rollout_id=$1 AND phase='pending';

-- name: ActSystemRollout :one
UPDATE delivery_system_rollouts SET state=sqlc.arg(state)::text,fencing_generation=fencing_generation+1,
	lease_owner='',lease_expires_at=NULL,last_error_code=sqlc.arg(last_error_code),
	started_at=CASE WHEN sqlc.arg(state)::text IN ('queued','progressing','rolling_back') THEN COALESCE(started_at,sqlc.arg(started_at)) ELSE started_at END,
	completed_at=COALESCE(sqlc.narg(completed_at)::timestamptz,completed_at)
WHERE id=sqlc.arg(id) AND fencing_generation=sqlc.arg(fencing_generation)
RETURNING *;

-- name: RetirePreviousSystemRolloutReleases :exec
UPDATE delivery_system_releases SET state='retired',retired_at=$2 WHERE state='released' AND id<>$1;

-- name: PromoteSystemRolloutRelease :execrows
UPDATE delivery_system_releases SET state='released',released_at=COALESCE(released_at,$2),retired_at=NULL WHERE id=$1 AND state='draft';

-- name: ListReconcilableSystemRollouts :many
SELECT id FROM delivery_system_rollouts WHERE state IN ('queued','progressing','rolling_back') ORDER BY created_at LIMIT $1::integer;

-- name: GetSystemRolloutAssignmentCounts :one
SELECT
(count(*) FILTER (WHERE phase='ready'))::integer AS ready,
(count(*) FILTER (WHERE phase='failed'))::integer AS failed,
(count(*) FILTER (WHERE phase<>'pending'))::integer AS released,
(count(*) FILTER (WHERE phase='pending'))::integer AS pending,
(count(*) FILTER (WHERE phase IN ('released','applying')))::integer AS in_flight,
(count(*) FILTER (WHERE phase='ready' AND ready_at > now() - (sqlc.arg(min_ready_ms)::bigint * interval '1 millisecond')))::integer AS immature_ready,
(count(*) FILTER (WHERE phase='rolled_back'))::integer AS rolled_back,
(count(*) FILTER (WHERE phase='rollback_failed'))::integer AS rollback_failed,
(count(*) FILTER (WHERE phase IN ('rolling_back','rolled_back','rollback_failed')))::integer AS rollback_targets,
(count(*) FILTER (WHERE phase='pending' AND cohort=0))::integer AS pending_canary,
	COALESCE(max(cohort) FILTER (WHERE phase='ready'),-1)::integer AS max_ready_cohort
FROM delivery_system_cluster_assignments WHERE rollout_id=sqlc.arg(rollout_id);

-- name: ReleaseSystemRolloutNextCohort :execrows
WITH next_assignments AS (
	SELECT cluster_id
	FROM delivery_system_cluster_assignments
	WHERE delivery_system_cluster_assignments.rollout_id=sqlc.arg(rollout_id)
	  AND cohort=(SELECT min(pending.cohort) FROM delivery_system_cluster_assignments AS pending WHERE pending.rollout_id=sqlc.arg(rollout_id) AND pending.phase='pending')
	  AND phase='pending'
	ORDER BY release_order,cluster_id
	LIMIT sqlc.arg(release_limit)::integer
	FOR UPDATE
)
UPDATE delivery_system_cluster_assignments AS assignment
SET phase='released',released_at=sqlc.arg(released_at),fence=assignment.fence+1
FROM next_assignments
WHERE assignment.cluster_id=next_assignments.cluster_id;

-- name: BeginSystemRolloutAssignmentRollback :exec
UPDATE delivery_system_cluster_assignments SET
	desired_release_id=$2,previous_release_id=$3,generation=generation+1,phase='rolling_back',
	fence=fence+1,released_at=$4,acknowledged_at=NULL,ready_at=NULL,
	observed_distribution_digest='',observed_agent_version='',last_error_code=''
WHERE rollout_id=$1 AND phase NOT IN ('pending','aborted');

-- name: FinishSystemRollout :exec
UPDATE delivery_system_rollouts SET state=sqlc.arg(state)::text,fencing_generation=fencing_generation+1,
	ready_clusters=sqlc.arg(ready_clusters),failed_clusters=sqlc.arg(failed_clusters),released_clusters=sqlc.arg(released_clusters),last_error_code=sqlc.arg(last_error_code),
	completed_at=sqlc.arg(completed_at),lease_owner='',lease_expires_at=NULL WHERE id=sqlc.arg(id);

-- name: UpdateSystemRolloutCounters :exec
UPDATE delivery_system_rollouts SET state=sqlc.arg(state)::text,fencing_generation=fencing_generation+1,
	ready_clusters=sqlc.arg(ready_clusters),failed_clusters=sqlc.arg(failed_clusters),released_clusters=sqlc.arg(released_clusters),last_error_code=sqlc.arg(last_error_code),
	started_at=CASE WHEN sqlc.arg(state)::text IN ('progressing','rolling_back') THEN COALESCE(started_at,now()) ELSE started_at END,
	lease_owner='',lease_expires_at=NULL WHERE id=sqlc.arg(id);

-- name: AppendSystemRolloutEvent :exec
INSERT INTO delivery_system_events (rollout_id,release_id,generation,event_type,from_phase,to_phase,reason_code,decision_digest,occurred_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9);

-- name: GetSystemRollout :one
SELECT * FROM delivery_system_rollouts WHERE id=$1;

-- name: GetSystemRolloutForUpdate :one
SELECT * FROM delivery_system_rollouts WHERE id=$1 FOR UPDATE;

-- name: GetSystemRolloutByIdempotency :one
SELECT * FROM delivery_system_rollouts WHERE idempotency_key=$1;
