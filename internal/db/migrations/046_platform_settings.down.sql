-- Rollback for 046_platform_settings.up.sql.

DROP INDEX IF EXISTS idx_platform_settings_key_prefix;
DROP TABLE IF EXISTS platform_settings;
