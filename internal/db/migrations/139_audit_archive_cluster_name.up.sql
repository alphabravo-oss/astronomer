-- audit_archive rows must be self-describing about which cluster they belong
-- to. resource_name is only populated when the row itself was a cluster event
-- carrying a name (e.g. cluster.create); rows swept in via detail->>'cluster_id'
-- (e.g. agent.token.rotated) leave it blank, so the tombstoned clusters row is
-- currently the only way to answer "which cluster was this?" — which blocks
-- ever purging tombstones. archived_cluster_name denormalizes the cluster name
-- onto the archive row itself.
--
-- Deliberately its OWN column, not resource_name: the archive sweep captures
-- non-cluster rows that merely reference the cluster, and writing the cluster
-- name into their resource_name would mislabel them as clusters.
ALTER TABLE audit_archive
    ADD COLUMN IF NOT EXISTS archived_cluster_name VARCHAR(255) NOT NULL DEFAULT '';

COMMENT ON COLUMN audit_archive.archived_cluster_name IS
    'Denormalized name of the decommissioned cluster this row was archived for; makes the row self-describing so the cluster tombstone can eventually be purged.';

-- Backfill from the tombstones while they still exist.
UPDATE audit_archive a
   SET archived_cluster_name = COALESCE(NULLIF(c.display_name, ''), c.name, '')
  FROM clusters c
 WHERE a.archived_cluster_id = c.id
   AND a.archived_cluster_name = '';
