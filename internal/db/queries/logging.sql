-- Logging Outputs

-- name: GetLoggingOutputByID :one
SELECT * FROM logging_outputs WHERE id = $1;

-- name: ListLoggingOutputs :many
SELECT * FROM logging_outputs ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: ListOutputsByCluster :many
SELECT * FROM logging_outputs WHERE cluster_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3;

-- name: ListEnabledOutputsByCluster :many
SELECT * FROM logging_outputs WHERE cluster_id = $1 AND enabled = true ORDER BY created_at DESC;

-- name: CreateLoggingOutput :one
INSERT INTO logging_outputs (name, output_type, configuration, cluster_id, enabled, created_by_id)
VALUES ($1, $2, $3, $4, $5, $6)
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

-- Logging Pipelines

-- name: GetLoggingPipelineByID :one
SELECT * FROM logging_pipelines WHERE id = $1;

-- name: ListLoggingPipelines :many
SELECT * FROM logging_pipelines ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: ListPipelinesByCluster :many
SELECT * FROM logging_pipelines WHERE cluster_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3;

-- name: ListEnabledPipelinesByCluster :many
SELECT * FROM logging_pipelines WHERE cluster_id = $1 AND enabled = true ORDER BY created_at DESC;

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

-- Pipeline <-> Output M2M

-- name: AddPipelineOutput :exec
INSERT INTO logging_pipeline_outputs (logging_pipeline_id, logging_output_id)
VALUES ($1, $2)
ON CONFLICT DO NOTHING;

-- name: RemovePipelineOutput :exec
DELETE FROM logging_pipeline_outputs WHERE logging_pipeline_id = $1 AND logging_output_id = $2;

-- name: ListOutputsForPipeline :many
SELECT lo.* FROM logging_outputs lo
INNER JOIN logging_pipeline_outputs lpo ON lo.id = lpo.logging_output_id
WHERE lpo.logging_pipeline_id = $1;

-- name: ListPipelinesForOutput :many
SELECT lp.* FROM logging_pipelines lp
INNER JOIN logging_pipeline_outputs lpo ON lp.id = lpo.logging_pipeline_id
WHERE lpo.logging_output_id = $1;
