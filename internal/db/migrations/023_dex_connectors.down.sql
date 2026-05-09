-- Phase B4: rollback Dex shim additions.
DELETE FROM cluster_tools WHERE slug = 'dex';

DROP INDEX IF EXISTS idx_dex_connectors_enabled;
DROP INDEX IF EXISTS idx_dex_connectors_type;

DROP TABLE IF EXISTS dex_settings;
DROP TABLE IF EXISTS dex_connectors;
