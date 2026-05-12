-- Down-migration for 070_apiserver_allowlist.
--
-- Drops the snapshots table FIRST so the FK to clusters doesn't dangle
-- when its parent table goes; the indexes go with their table per
-- DROP TABLE semantics.

DROP TABLE IF EXISTS apiserver_allowlist_snapshots;
DROP TABLE IF EXISTS apiserver_allowlists;
