-- name: GetDefaultControlPlanePolicy :one
SELECT * FROM control_plane_policies WHERE name = 'default' LIMIT 1;

-- name: UpsertDefaultControlPlanePolicy :one
INSERT INTO control_plane_policies (
    name,
    monitoring_queue_depth_threshold,
    argocd_queue_depth_threshold,
    tools_queue_depth_threshold,
    catalog_queue_depth_threshold,
    monitoring_stale_running_threshold,
    argocd_stale_running_threshold,
    tools_stale_running_threshold,
    catalog_stale_running_threshold,
    monitoring_recent_failure_threshold,
    argocd_recent_failure_threshold,
    tools_recent_failure_threshold,
    catalog_recent_failure_threshold,
    recent_failure_window_minutes
)
VALUES (
    'default',
    $1, $2, $3, $4,
    $5, $6, $7, $8,
    $9, $10, $11, $12,
    $13
)
ON CONFLICT (name) DO UPDATE SET
    monitoring_queue_depth_threshold = EXCLUDED.monitoring_queue_depth_threshold,
    argocd_queue_depth_threshold = EXCLUDED.argocd_queue_depth_threshold,
    tools_queue_depth_threshold = EXCLUDED.tools_queue_depth_threshold,
    catalog_queue_depth_threshold = EXCLUDED.catalog_queue_depth_threshold,
    monitoring_stale_running_threshold = EXCLUDED.monitoring_stale_running_threshold,
    argocd_stale_running_threshold = EXCLUDED.argocd_stale_running_threshold,
    tools_stale_running_threshold = EXCLUDED.tools_stale_running_threshold,
    catalog_stale_running_threshold = EXCLUDED.catalog_stale_running_threshold,
    monitoring_recent_failure_threshold = EXCLUDED.monitoring_recent_failure_threshold,
    argocd_recent_failure_threshold = EXCLUDED.argocd_recent_failure_threshold,
    tools_recent_failure_threshold = EXCLUDED.tools_recent_failure_threshold,
    catalog_recent_failure_threshold = EXCLUDED.catalog_recent_failure_threshold,
    recent_failure_window_minutes = EXCLUDED.recent_failure_window_minutes,
    updated_at = now()
RETURNING *;

-- name: ListControlPlaneAlerts :many
SELECT * FROM control_plane_alerts
WHERE (
    sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text
) AND (
    sqlc.narg(controller)::text IS NULL OR controller = sqlc.narg(controller)::text
)
ORDER BY fired_at DESC
LIMIT $1 OFFSET $2;

-- name: GetActiveControlPlaneAlert :one
SELECT * FROM control_plane_alerts
WHERE controller = $1 AND condition_type = $2 AND status = 'active'
LIMIT 1;

-- name: CreateControlPlaneAlert :one
INSERT INTO control_plane_alerts (
    controller,
    condition_type,
    status,
    message,
    detail
)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: ResolveControlPlaneAlert :one
UPDATE control_plane_alerts
SET
    status = 'resolved',
    resolved_at = now(),
    updated_at = now(),
    detail = $2
WHERE id = $1
RETURNING *;

-- name: AcknowledgeControlPlaneAlert :one
UPDATE control_plane_alerts
SET
    acknowledged_by_id = $2,
    acknowledged_at = now(),
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: CreateControlPlaneSilence :one
INSERT INTO control_plane_silences (
    controller,
    condition_type,
    reason,
    starts_at,
    ends_at,
    created_by_id
)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: ListControlPlaneSilences :many
SELECT * FROM control_plane_silences
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: GetActiveControlPlaneSilences :many
SELECT * FROM control_plane_silences
WHERE starts_at <= now() AND ends_at > now()
ORDER BY ends_at ASC;

-- name: DeleteControlPlaneSilence :exec
DELETE FROM control_plane_silences WHERE id = $1;
