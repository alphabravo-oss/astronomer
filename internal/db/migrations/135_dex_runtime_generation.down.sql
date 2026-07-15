DROP TRIGGER IF EXISTS dex_connectors_runtime_generation ON dex_connectors;
DROP FUNCTION IF EXISTS bump_dex_runtime_generation();

ALTER TABLE dex_settings
    DROP CONSTRAINT IF EXISTS dex_settings_runtime_applied_generation_valid,
    DROP CONSTRAINT IF EXISTS dex_settings_runtime_generation_positive,
    DROP COLUMN IF EXISTS runtime_applied_generation,
    DROP COLUMN IF EXISTS runtime_generation,
    DROP COLUMN IF EXISTS service_name,
    DROP COLUMN IF EXISTS deployment_name,
    DROP COLUMN IF EXISTS chart_release_name;
