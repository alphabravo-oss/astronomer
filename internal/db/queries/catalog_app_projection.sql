-- The Apps read model projects durable Delivery state before pagination. It
-- never mutates/deletes compatibility rows while serving a read.
-- name: ListProjectedClusterApps :many
SELECT sqlc.embed(ic),
  (CASE
    WHEN dt.id IS NULL THEN ic.status
    WHEN dt.deletion_state = 'deleting' THEN 'uninstalling'
    WHEN d.phase IN ('failed', 'timed_out', 'rollback_failed') THEN
      CASE WHEN ic.status IN ('upgrading','pending_upgrade') THEN 'failed_upgrade'
           WHEN ic.status IN ('rolling_back','pending_rollback') THEN 'failed_rollback'
           ELSE 'failed_install' END
    WHEN d.phase = 'degraded' THEN 'degraded'
    WHEN dt.suspended OR d.phase = 'suspended' THEN 'suspended'
    WHEN d.phase = 'ready' AND d.desired_generation = dt.generation
      AND d.observed_generation = d.desired_generation
      AND d.observed_spec_digest = d.desired_spec_digest THEN 'installed'
    WHEN d.phase IS NULL OR d.phase IN ('pending','blocked','applying','ready') THEN
      CASE WHEN ic.status IN ('upgrading','pending_upgrade') THEN 'upgrading'
           WHEN ic.status IN ('rolling_back','pending_rollback') THEN 'rolling_back'
           ELSE 'installing' END
    ELSE 'unknown'
  END)::text AS projected_status,
  hcv.version AS chart_version, hcv.app_version AS chart_app_version,
  hc.id AS chart_id, hc.name AS chart_name, hc.display_name AS chart_display_name,
  hc.description AS chart_description, hc.icon_url AS chart_icon_url,
  hc.category AS chart_category, hr.name AS repo_name, hr.repo_type AS repo_type
FROM installed_charts ic
LEFT JOIN delivery_targets dt ON dt.id = ic.request_id
LEFT JOIN cluster_deployments d ON d.target_id = dt.id AND d.cluster_id = ic.cluster_id
LEFT JOIN helm_chart_versions hcv ON ic.chart_version_id = hcv.id
LEFT JOIN helm_charts hc ON hcv.chart_id = hc.id
LEFT JOIN helm_repositories hr ON hc.repository_id = hr.id
WHERE ic.cluster_id = sqlc.arg(cluster_id)
  AND COALESCE(dt.deletion_state, 'active') <> 'deleted'
  AND ic.status <> 'uninstalled'
ORDER BY ic.created_at DESC, ic.id DESC
LIMIT sqlc.arg(query_limit) OFFSET sqlc.arg(query_offset);

-- name: CountProjectedClusterApps :one
SELECT count(*) FROM installed_charts ic
LEFT JOIN delivery_targets dt ON dt.id = ic.request_id
WHERE ic.cluster_id = $1
  AND COALESCE(dt.deletion_state, 'active') <> 'deleted'
  AND ic.status <> 'uninstalled';
