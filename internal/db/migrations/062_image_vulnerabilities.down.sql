-- Roll back sprint 062 image vulnerability surface.
--
-- Drops indexes before tables to keep the rollback symmetric with the up
-- migration even though DROP TABLE drops dependent indexes implicitly.
-- The catalog seed rows (helm_repositories 'aqua' + helm_charts
-- 'trivy-operator') are intentionally NOT deleted — an operator may have
-- installed the chart and we don't want to break the cluster_tools FK
-- chain at rollback time. If you want them gone, delete them manually.

DROP INDEX IF EXISTS idx_image_vulns_severity;
DROP TABLE IF EXISTS image_vulnerabilities;

DROP INDEX IF EXISTS idx_ivr_cluster_ns;
DROP INDEX IF EXISTS idx_ivr_cluster_severity;
DROP TABLE IF EXISTS image_vulnerability_reports;
