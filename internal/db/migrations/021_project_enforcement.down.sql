DROP INDEX IF EXISTS idx_project_namespaces_lease;
DROP INDEX IF EXISTS idx_project_namespaces_cluster;
DROP TABLE IF EXISTS project_namespaces;

ALTER TABLE projects
    DROP COLUMN IF EXISTS network_policy_mode,
    DROP COLUMN IF EXISTS limit_range;
