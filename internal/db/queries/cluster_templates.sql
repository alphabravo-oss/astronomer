-- Cluster templates + applications + registration policies (migration 049).
-- Backs:
--   * /api/v1/cluster-templates/*       — CRUD on cluster_templates
--   * /api/v1/clusters/{id}/template/*  — apply/reapply/detach + status
--   * cluster_template:apply worker     — idempotent convergent apply task
--   * cluster_template:drift_check task — periodic drift surface
--
-- The handler validates spec.* shapes (env enum, PSS enum, unknown keys);
-- this layer stores/returns the JSONB verbatim.

-- name: ListClusterTemplates :many
SELECT * FROM cluster_templates
ORDER BY name
LIMIT $1 OFFSET $2;

-- name: CountClusterTemplates :one
SELECT count(*) FROM cluster_templates;

-- name: GetClusterTemplateByID :one
SELECT * FROM cluster_templates WHERE id = $1;

-- name: GetClusterTemplateByName :one
SELECT * FROM cluster_templates WHERE name = $1;

-- name: CreateClusterTemplate :one
INSERT INTO cluster_templates (name, description, spec, created_by)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: UpdateClusterTemplate :one
-- The handler always passes the full body; we don't try to do partial
-- merges in SQL — JSONB merge semantics are surprising enough that we'd
-- rather keep them in Go where the validator lives.
UPDATE cluster_templates SET
    name        = $2,
    description = $3,
    spec        = $4,
    updated_at  = now()
WHERE id = $1
RETURNING *;

-- name: DeleteClusterTemplate :exec
-- The FK on cluster_template_applications.template_id is ON DELETE RESTRICT,
-- so this raises a foreign_key_violation when at least one cluster still
-- references the template. The handler translates that into a 409 Conflict.
DELETE FROM cluster_templates WHERE id = $1;

-- name: CountClusterTemplateApplicationsByTemplate :one
-- Pre-flight check so the DELETE handler can return a friendly 409 body
-- with the "remove from N clusters first" count BEFORE attempting the
-- FK-restricted delete.
SELECT count(*) FROM cluster_template_applications WHERE template_id = $1;

-- name: GetClusterTemplateApplication :one
SELECT * FROM cluster_template_applications WHERE cluster_id = $1;

-- name: UpsertClusterTemplateApplication :one
-- Single statement to either INSERT a fresh row OR overwrite the
-- existing one on reapply / template change. We reset status to
-- 'pending' on every call so the apply worker re-runs convergence.
INSERT INTO cluster_template_applications (
    cluster_id, template_id, status, spec_snapshot, last_error, applied_at
)
VALUES ($1, $2, 'pending', $3, '', NULL)
ON CONFLICT (cluster_id) DO UPDATE SET
    template_id   = EXCLUDED.template_id,
    status        = 'pending',
    spec_snapshot = EXCLUDED.spec_snapshot,
    last_error    = '',
    applied_at    = NULL,
    updated_at    = now()
RETURNING *;

-- name: MarkClusterTemplateApplicationStatus :one
-- Generic status transition used by the apply worker. The handler-level
-- code keeps the status strings centralized as constants so a typo here
-- doesn't drift from the DB CHECK semantics (we deliberately don't have a
-- CHECK constraint — the small enum is enforced in Go).
UPDATE cluster_template_applications SET
    status     = $2,
    last_error = $3,
    applied_at = $4,
    updated_at = now()
WHERE cluster_id = $1
RETURNING *;

-- name: DeleteClusterTemplateApplication :exec
-- Detach: removes the binding row but leaves any tools/projects the
-- apply worker installed in place. The DELETE handler documents this
-- behavior so an operator who wants a full teardown can chain the
-- individual uninstalls.
DELETE FROM cluster_template_applications WHERE cluster_id = $1;

-- name: ListClusterTemplateApplicationsByStatus :many
-- Drives the periodic drift_check sweep — every 'applied' row gets its
-- live cluster state compared against spec_snapshot. We don't filter on
-- spec equality here because the comparison is structural and lives in
-- Go.
SELECT * FROM cluster_template_applications
WHERE status = $1
ORDER BY updated_at DESC
LIMIT $2;

-- Registration policy table (per-cluster). The apply worker stamps this
-- when spec.registration_policy is set; the existing token cleanup task
-- can read token_rotation_days when rotating.

-- name: UpsertClusterRegistrationPolicy :one
INSERT INTO cluster_registration_policies (
    cluster_id, token_rotation_days, source_template_id
)
VALUES ($1, $2, $3)
ON CONFLICT (cluster_id) DO UPDATE SET
    token_rotation_days = EXCLUDED.token_rotation_days,
    source_template_id  = EXCLUDED.source_template_id,
    updated_at          = now()
RETURNING *;

-- name: DeleteClusterRegistrationPolicy :exec
DELETE FROM cluster_registration_policies WHERE cluster_id = $1;
