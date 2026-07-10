ALTER TABLE dex_settings
    DROP COLUMN IF EXISTS public_clients_encrypted,
    DROP COLUMN IF EXISTS runtime_secret_name;

COMMENT ON COLUMN dex_settings.configmap_name IS NULL;
COMMENT ON COLUMN dex_settings.public_clients IS NULL;

