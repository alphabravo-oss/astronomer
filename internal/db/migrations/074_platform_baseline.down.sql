-- Rollback for 074_platform_baseline.up.sql.
--
-- Drop the column FIRST (the FK on cluster_templates blocks the DELETE
-- otherwise), THEN the seed row. We DO NOT touch any
-- cluster_template_applications rows pointing at the baseline — those
-- belong to the operator now; rolling the migration back shouldn't
-- silently uninstall trivy-operator from a fleet of clusters.

ALTER TABLE platform_configuration DROP COLUMN IF EXISTS default_cluster_template_id;

-- Best-effort: only delete the seed row when nothing references it. If
-- it's been cloned-and-modified-then-renamed, the row may already be
-- gone; if it's still bound to clusters, the FK on
-- cluster_template_applications.template_id (ON DELETE RESTRICT) makes
-- this DELETE a no-op rather than a destructive cascade. That's the
-- correct safe rollback semantic.
DELETE FROM cluster_templates
WHERE name = 'Platform baseline'
  AND NOT EXISTS (
      SELECT 1 FROM cluster_template_applications a WHERE a.template_id = cluster_templates.id
  );
