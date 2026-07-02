-- Reverse 130: drop the uniqueness guard and restore the non-unique lookup
-- index on (cluster_id, namespace).
DROP INDEX IF EXISTS uq_project_namespaces_cluster_namespace;

CREATE INDEX IF NOT EXISTS idx_project_namespaces_cluster
    ON project_namespaces (cluster_id, namespace);
