-- Fleet operations (migration 056). Backs:
--   * /api/v1/fleet-operations/*       — CRUD + lifecycle endpoints
--   * fleet:orchestrate worker         — periodic, idempotent dispatcher
--
-- Two tables: fleet_operations (the coordinated action) +
-- fleet_operation_targets (one row per matched cluster).
--
-- Operator-facing CRUD is restricted to create / list / get; updates
-- happen via pause/resume/abort/retry-failed which are status
-- transitions only. Once dispatched, the operation's selector and
-- operation_spec are frozen — the persisted target list is the
-- contract.

-- ─────────────────────────────────────────────────────────────────────
-- fleet_operations
-- ─────────────────────────────────────────────────────────────────────

-- name: CreateFleetOperation :one
INSERT INTO fleet_operations (
    name,
    description,
    operation_type,
    operation_spec,
    selector,
    strategy,
    max_concurrent,
    on_error,
    respect_maintenance_windows,
    created_by
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;

-- name: GetFleetOperation :one
SELECT * FROM fleet_operations WHERE id = $1;

-- name: ListFleetOperations :many
-- Paginated list, optional status filter. The handler passes the
-- empty string when no filter is requested; the COALESCE-style guard
-- below skips the WHERE clause cleanly in that case.
SELECT * FROM fleet_operations
WHERE (
    sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text
)
ORDER BY created_at DESC
LIMIT sqlc.arg(query_limit) OFFSET sqlc.arg(query_offset);

-- name: CountFleetOperations :one
SELECT count(*) FROM fleet_operations
WHERE (
    sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text
);

-- name: ListPendingFleetOperations :many
-- Drives the orchestrator tick. Returns every operation that's
-- pending (still needs a launch) OR running (still needs dispatch /
-- polling). Ordered by created_at so the orchestrator drains older
-- operations first.
SELECT * FROM fleet_operations
WHERE status IN ('pending','running')
ORDER BY created_at ASC
LIMIT $1;

-- name: MarkFleetOperationTransition :one
-- Atomic state transition. The orchestrator uses this to move
-- pending → running, running → completed/failed/aborted, etc.
-- The from_status guard prevents two concurrent ticks from racing on
-- the same operation: only the tick whose claimed-from status matches
-- the live row wins.
UPDATE fleet_operations SET
    status        = sqlc.arg(to_status),
    started_at    = COALESCE(started_at, sqlc.arg(started_at)),
    completed_at  = sqlc.arg(completed_at),
    last_error    = sqlc.arg(last_error),
    updated_at    = now()
WHERE id = sqlc.arg(id)
  AND status = sqlc.arg(from_status)
RETURNING *;

-- name: UpdateFleetOperationCounters :one
-- Bulk counter refresh. The orchestrator recomputes the aggregate
-- counts from fleet_operation_targets when it observes a target
-- transition, then writes them back here so the read endpoints
-- don't have to GROUP BY.
UPDATE fleet_operations SET
    total_clusters     = $2,
    completed_clusters = $3,
    failed_clusters    = $4,
    skipped_clusters   = $5,
    updated_at         = now()
WHERE id = $1
RETURNING *;

-- name: SetFleetOperationStatus :one
-- Unconditional status set. The pause/resume/abort handler endpoints
-- use this because the operator's intent overrides whatever the
-- orchestrator was about to do — the orchestrator re-checks status
-- on every tick and reconciles.
UPDATE fleet_operations SET
    status     = $2,
    last_error = $3,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteFleetOperation :exec
DELETE FROM fleet_operations WHERE id = $1;

-- ─────────────────────────────────────────────────────────────────────
-- fleet_operation_targets
-- ─────────────────────────────────────────────────────────────────────

-- name: CreateFleetOperationTarget :one
-- ON CONFLICT DO NOTHING — defensive. The orchestrator evaluates the
-- selector exactly once at launch, but a duplicate INSERT (e.g. the
-- launch step ran twice because a tick crashed mid-INSERT) must be
-- caught at the DB so the operation never has two rows competing
-- for one cluster's terminal state.
INSERT INTO fleet_operation_targets (
    operation_id,
    cluster_id,
    sub_operation_type
)
VALUES ($1, $2, $3)
ON CONFLICT (operation_id, cluster_id) DO NOTHING
RETURNING *;

-- name: ListFleetOperationTargets :many
SELECT * FROM fleet_operation_targets
WHERE operation_id = $1
ORDER BY created_at ASC
LIMIT sqlc.arg(query_limit) OFFSET sqlc.arg(query_offset);

-- name: CountFleetOperationTargets :one
SELECT count(*) FROM fleet_operation_targets WHERE operation_id = $1;

-- name: ListPendingTargetsForOperation :many
-- Next batch to dispatch. Ordered by created_at so the orchestrator
-- preserves the launch ordering across ticks.
SELECT * FROM fleet_operation_targets
WHERE operation_id = $1 AND status = 'pending'
ORDER BY created_at ASC
LIMIT $2;

-- name: ListRunningTargetsForOperation :many
-- Used by the orchestrator to poll sub-operation status for every
-- running target. Bounded by the operation's max_concurrent so this
-- can never return more than that many rows.
SELECT * FROM fleet_operation_targets
WHERE operation_id = $1 AND status = 'running'
ORDER BY started_at ASC;

-- name: CountRunningTargetsForOperation :one
-- Gate for the max_concurrent dispatcher.
SELECT count(*) FROM fleet_operation_targets
WHERE operation_id = $1 AND status = 'running';

-- name: CountFailedTargetsForOperation :one
-- Used by the abort-on-failure check.
SELECT count(*) FROM fleet_operation_targets
WHERE operation_id = $1 AND status = 'failed';

-- name: CountTerminalTargetsForOperation :one
-- A target is terminal when it's completed/failed/skipped/aborted.
-- The orchestrator transitions the parent operation to completed/failed
-- once every target is terminal.
SELECT count(*) FROM fleet_operation_targets
WHERE operation_id = $1
  AND status IN ('completed','failed','skipped','aborted');

-- name: CountFleetOperationTargetsByStatus :many
-- Single round-trip aggregate used to refresh the parent operation's
-- counter columns. Returns (status, count) for every group present.
SELECT status, count(*)::bigint AS n FROM fleet_operation_targets
WHERE operation_id = $1
GROUP BY status;

-- name: MarkFleetTargetDispatched :one
-- Atomic transition pending → running with the sub-operation reference
-- and timestamps stamped in. The status guard makes the call idempotent
-- against a duplicate tick: only one tick succeeds, the second is a
-- no-op (zero rows updated → pgx.ErrNoRows).
UPDATE fleet_operation_targets SET
    status            = 'running',
    sub_operation_id  = $2,
    sub_operation_type = $3,
    started_at        = now(),
    updated_at        = now()
WHERE id = $1 AND status = 'pending'
RETURNING *;

-- name: MarkFleetTargetCompleted :one
UPDATE fleet_operation_targets SET
    status       = 'completed',
    completed_at = now(),
    last_error   = '',
    updated_at   = now()
WHERE id = $1
RETURNING *;

-- name: MarkFleetTargetFailed :one
UPDATE fleet_operation_targets SET
    status       = 'failed',
    completed_at = now(),
    last_error   = $2,
    updated_at   = now()
WHERE id = $1
RETURNING *;

-- name: MarkFleetTargetSkipped :one
UPDATE fleet_operation_targets SET
    status       = 'skipped',
    completed_at = now(),
    last_error   = $2,
    updated_at   = now()
WHERE id = $1
RETURNING *;

-- name: RequeueFailedTargets :exec
-- Bulk reset for the retry-failed endpoint. Resets every 'failed'
-- target on this operation back to 'pending' so the next orchestrator
-- tick re-dispatches them. sub_operation_id is cleared so the
-- orchestrator doesn't see a stale reference.
UPDATE fleet_operation_targets SET
    status           = 'pending',
    sub_operation_id = NULL,
    started_at       = NULL,
    completed_at     = NULL,
    last_error       = '',
    updated_at       = now()
WHERE operation_id = $1 AND status = 'failed';

-- name: ListClustersForSelectorEvaluation :many
-- All non-decommissioned clusters. The orchestrator's selector
-- evaluator walks this list in Go (matchLabels intersection is a
-- string-map comparison that's easier to reason about than a JSONB
-- predicate, and the cluster count never exceeds a few thousand
-- in any deployment we've seen).
SELECT id, name, labels FROM clusters
WHERE decommissioned_at IS NULL
ORDER BY name ASC;
