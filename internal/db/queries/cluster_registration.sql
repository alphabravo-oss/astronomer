-- Wizard registration phase + steps (migration 078).
-- sqlc is hand-shimmed in this tree; the canonical Go source for these
-- queries is internal/db/sqlc/cluster_registration.sql.go. This file is
-- kept in sync so a future sqlc-CLI run reproduces the same shapes.

-- name: GetClusterRegistrationRecord :one
SELECT id, registration_phase, registration_started_at, registration_completed_at, install_baseline
FROM clusters
WHERE id = $1;

-- name: UpdateClusterRegistrationPhase :one
UPDATE clusters
SET registration_phase = $2,
    registration_started_at = COALESCE(registration_started_at, $3),
    registration_completed_at = $4
WHERE id = $1
RETURNING id, registration_phase, registration_started_at, registration_completed_at, install_baseline;

-- name: SetClusterInstallBaseline :one
UPDATE clusters
SET install_baseline = $2
WHERE id = $1
RETURNING id, registration_phase, registration_started_at, registration_completed_at, install_baseline;

-- name: InsertClusterRegistrationStep :one
INSERT INTO cluster_registration_steps
    (cluster_id, step_name, label, status, progress_pct, detail_json, started_at, completed_at, error_message, step_order)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING id, cluster_id, step_name, label, status, progress_pct, detail_json, started_at, completed_at, error_message, created_at, step_order;

-- name: UpdateClusterRegistrationStep :one
UPDATE cluster_registration_steps
SET status = $2,
    progress_pct = $3,
    detail_json = COALESCE($4, detail_json),
    started_at = COALESCE(started_at, $5),
    completed_at = $6,
    error_message = $7
WHERE id = $1
RETURNING id, cluster_id, step_name, label, status, progress_pct, detail_json, started_at, completed_at, error_message, created_at, step_order;

-- name: ListClusterRegistrationSteps :many
SELECT id, cluster_id, step_name, label, status, progress_pct, detail_json, started_at, completed_at, error_message, created_at, step_order
FROM cluster_registration_steps
WHERE cluster_id = $1
ORDER BY step_order ASC, created_at ASC;

-- name: GetClusterRegistrationStep :one
SELECT id, cluster_id, step_name, label, status, progress_pct, detail_json, started_at, completed_at, error_message, created_at, step_order
FROM cluster_registration_steps
WHERE id = $1;

-- name: MaxStepOrderForCluster :one
SELECT COALESCE(MAX(step_order), 0)::int
FROM cluster_registration_steps
WHERE cluster_id = $1;

-- name: CloseRunningStepsForCluster :exec
-- Sprint 086 — closes orphan "running" step rows on a given
-- (cluster_id, step_name). The orchestrator's auto-retry path was
-- writing a fresh `template_applying` row on every retry without
-- closing the previous one, leaving the Provisioning tab showing
-- "running" forever even after the apply finished. Called from
-- OnTemplateApplyStart before the new row is written.
UPDATE cluster_registration_steps
   SET status = 'failed',
       completed_at = COALESCE(completed_at, now()),
       error_message = CASE
           WHEN error_message = '' THEN 'superseded by retry'
           ELSE error_message
       END
 WHERE cluster_id = $1
   AND step_name = $2
   AND status = 'running';
