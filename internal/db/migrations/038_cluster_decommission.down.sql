DROP INDEX IF EXISTS idx_audit_archive_archived_at;
DROP INDEX IF EXISTS idx_audit_archive_resource;
DROP INDEX IF EXISTS idx_audit_archive_cluster;
DROP TABLE IF EXISTS audit_archive;

DROP INDEX IF EXISTS idx_clusters_decommissioned_at;
ALTER TABLE clusters DROP COLUMN IF EXISTS decommissioned_at;

DROP INDEX IF EXISTS idx_cluster_decommissions_status;
DROP INDEX IF EXISTS idx_cluster_decommissions_cluster;
DROP TABLE IF EXISTS cluster_decommissions;
