-- Down-migration for 066_cluster_groups.
--
-- Drop the per-cluster index + column FIRST so the FK from clusters.group_id
-- → cluster_groups.id is gone before we drop the parent table. Then drop
-- the cluster_groups table; its own indexes go with the table per DROP
-- TABLE semantics, and the self-FK cascade cleans up the subtree without
-- extra work.

DROP INDEX IF EXISTS idx_clusters_group;
ALTER TABLE clusters DROP COLUMN IF EXISTS group_id;
DROP TABLE IF EXISTS cluster_groups;
