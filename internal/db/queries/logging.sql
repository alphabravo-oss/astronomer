-- Logging Outputs

-- name: GetLoggingOutputByID :one
SELECT * FROM logging_outputs WHERE id = $1;

-- name: ListLoggingOutputs :many
SELECT * FROM logging_outputs ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: ListOutputsByCluster :many
SELECT * FROM logging_outputs WHERE cluster_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3;

-- name: CreateLoggingOutput :one
INSERT INTO logging_outputs (name, output_type, configuration, cluster_id, enabled, created_by_id, is_system)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: UpdateLoggingOutput :one
UPDATE logging_outputs SET
    name = $2,
    output_type = $3,
    configuration = $4,
    enabled = $5
WHERE id = $1
RETURNING *;

-- name: DeleteLoggingOutput :exec
DELETE FROM logging_outputs WHERE id = $1;

-- name: CountLoggingOutputs :one
SELECT count(*) FROM logging_outputs;

-- name: CountOutputsByCluster :one
-- Total outputs scoped to a single cluster, matching ListOutputsByCluster so
-- the cluster-scoped list endpoint reports a correct pagination total.
SELECT count(*) FROM logging_outputs WHERE cluster_id = $1;

-- Logging Saved Searches

-- name: ListLoggingSavedSearches :many
SELECT *
FROM logging_saved_searches
WHERE owner_user_id = $1 AND output_id = $2
ORDER BY updated_at DESC, id DESC;

-- name: GetLoggingSavedSearchForOwner :one
SELECT *
FROM logging_saved_searches
WHERE id = $1 AND owner_user_id = $2;

-- name: CreateLoggingSavedSearch :one
INSERT INTO logging_saved_searches (
    output_id, owner_user_id, name, query_text, namespaces,
    result_limit, direction, live_tail
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: UpdateLoggingSavedSearch :one
UPDATE logging_saved_searches
SET name = $3,
    query_text = $4,
    namespaces = $5,
    result_limit = $6,
    direction = $7,
    live_tail = $8,
    updated_at = now()
WHERE id = $1 AND owner_user_id = $2
RETURNING *;

-- name: DeleteLoggingSavedSearch :execrows
DELETE FROM logging_saved_searches
WHERE id = $1 AND owner_user_id = $2;

-- name: GetSystemLoggingOutputByCluster :one
SELECT * FROM logging_outputs WHERE cluster_id = $1 AND is_system = true LIMIT 1;

-- name: ListSystemLoggingOutputs :many
SELECT * FROM logging_outputs WHERE is_system = true;

-- name: DisableSystemLoggingOutputs :many
UPDATE logging_outputs SET enabled = false WHERE is_system = true AND enabled = true
RETURNING *;

-- Logging Pipelines

-- name: GetLoggingPipelineByID :one
SELECT * FROM logging_pipelines WHERE id = $1;

-- name: ListLoggingPipelines :many
SELECT * FROM logging_pipelines ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: ListPipelinesByCluster :many
SELECT * FROM logging_pipelines WHERE cluster_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3;

-- name: CreateLoggingPipeline :one
INSERT INTO logging_pipelines (name, cluster_id, namespaces, labels, filters, enabled, created_by_id)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: UpdateLoggingPipeline :one
UPDATE logging_pipelines SET
    name = $2,
    namespaces = $3,
    labels = $4,
    filters = $5,
    enabled = $6
WHERE id = $1
RETURNING *;

-- name: DeleteLoggingPipeline :exec
DELETE FROM logging_pipelines WHERE id = $1;

-- name: CountLoggingPipelines :one
SELECT count(*) FROM logging_pipelines;

-- name: CountPipelinesByCluster :one
-- Total pipelines scoped to a single cluster, matching ListPipelinesByCluster
-- so the cluster-scoped list endpoint reports a correct pagination total.
SELECT count(*) FROM logging_pipelines WHERE cluster_id = $1;

-- name: ReplaceLoggingPipelineOutputs :one
-- Full-replacement semantics are intentional: the pipeline write API is PUT,
-- and the handler executes this statement in the same transaction as the
-- pipeline row, reconcile operation, and audit outbox intent. The cluster join
-- prevents a pipeline from routing one cluster's logs into another cluster's
-- output, even if a caller supplies a valid foreign output UUID.
WITH removed AS (
    DELETE FROM logging_pipeline_outputs
    WHERE logging_pipeline_outputs.logging_pipeline_id = sqlc.arg(logging_pipeline_id)
),
desired AS (
    SELECT DISTINCT unnest(sqlc.arg(output_ids)::uuid[]) AS output_id
),
inserted AS (
    INSERT INTO logging_pipeline_outputs (logging_pipeline_id, logging_output_id)
    SELECT p.id, o.id
    FROM desired d
    JOIN logging_pipelines p ON p.id = sqlc.arg(logging_pipeline_id)
    JOIN logging_outputs o
      ON o.id = d.output_id
     AND o.cluster_id = p.cluster_id
    RETURNING logging_output_id
)
SELECT count(*) FROM inserted;

-- name: ListLoggingPipelineOutputDetails :many
-- One batch query enriches list responses without an N+1 request pattern.
SELECT lpo.logging_pipeline_id,
       o.id AS logging_output_id,
       o.name AS logging_output_name
FROM logging_pipeline_outputs lpo
JOIN logging_outputs o ON o.id = lpo.logging_output_id
WHERE lpo.logging_pipeline_id = ANY(sqlc.arg(pipeline_ids)::uuid[])
ORDER BY lpo.logging_pipeline_id, lower(o.name), o.id;
