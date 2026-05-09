DROP INDEX IF EXISTS clusters_one_local;
ALTER TABLE clusters DROP COLUMN IF EXISTS is_local;
