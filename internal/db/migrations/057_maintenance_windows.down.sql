-- Down-migration for 057_maintenance_windows.
--
-- Drops deferred_operations first so the FK reference to
-- maintenance_windows doesn't dangle when the parent table goes.
-- Indexes are dropped implicitly with their tables.

DROP TABLE IF EXISTS deferred_operations;
DROP TABLE IF EXISTS maintenance_windows;
