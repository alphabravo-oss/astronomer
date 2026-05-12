-- Reverse of migration 068.
-- Cascading FKs on network_policy_applications.template_id /cluster_id
-- handle cleanup; we drop the applications table first to be explicit.
DROP INDEX IF EXISTS idx_np_apps_template;
DROP INDEX IF EXISTS idx_np_apps_cluster;
DROP TABLE IF EXISTS network_policy_applications;
DROP TABLE IF EXISTS network_policy_templates;
