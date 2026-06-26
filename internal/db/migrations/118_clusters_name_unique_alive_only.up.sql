-- Free a cluster name once its row is decommissioned.
--
-- clusters_name_key was a full-table UNIQUE on name, so a tombstoned cluster
-- (decommissioned_at set, hidden from ListClusters/GetClusterByName) kept its
-- name reserved forever. Re-registering that name in the wizard then 409'd
-- against an invisible row. Swap to a partial unique index scoped to live rows
-- only -- the same pattern clusters_external_ref_unique already uses -- so the
-- conflict check matches what the read paths actually see.
-- clusters_name_key is backed by a UNIQUE *constraint* (the auto-named _key
-- index), so DROP INDEX alone errors with a dependency. Drop the constraint
-- first; that removes the backing index. The bare DROP INDEX is a no-op
-- fallback for installs where it was ever a plain index.
ALTER TABLE clusters DROP CONSTRAINT IF EXISTS clusters_name_key;
DROP INDEX IF EXISTS clusters_name_key;
CREATE UNIQUE INDEX clusters_name_alive_unique ON clusters (name) WHERE decommissioned_at IS NULL;
