-- Enforce that a namespace on a given cluster belongs to at most ONE project.
--
-- The AddNamespace handler used a pre-transaction cross-project check
-- (ListProjectsByCluster) to reject a namespace already claimed elsewhere,
-- but that check is a TOCTOU: two admins concurrently POSTing the same
-- namespace to two DIFFERENT projects on the same cluster each lock a
-- different project row (no contention) and both pass the pre-tx read, so
-- both sidecar rows commit. The namespace-scoped RBAC path then resolves the
-- namespace to two projects — a tenant-isolation break.
--
-- A UNIQUE index on (cluster_id, namespace) closes the race at the source:
-- the second committer fails with unique_violation, which the handler maps
-- to 409. Replaces the redundant non-unique lookup index on the same columns.
--
-- NOTE: if pre-existing data already assigns one (cluster_id, namespace) to
-- multiple projects (the bug this closes), this index build will fail until
-- the duplicates are resolved; dedupe those rows before applying.
DROP INDEX IF EXISTS idx_project_namespaces_cluster;

CREATE UNIQUE INDEX IF NOT EXISTS uq_project_namespaces_cluster_namespace
    ON project_namespaces (cluster_id, namespace);
