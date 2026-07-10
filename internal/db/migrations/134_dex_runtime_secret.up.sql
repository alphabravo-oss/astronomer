-- DEX-01: move Dex runtime material out of ConfigMaps and plaintext JSONB.
--
-- public_clients remains for one compatibility release as a migration source.
-- Application code encrypts its canonical JSON with the platform Fernet key,
-- writes public_clients_encrypted, and atomically scrubs public_clients to [].
-- SQL cannot perform that encryption because the key is deliberately not
-- available to the database migration process.
ALTER TABLE dex_settings
    ADD COLUMN IF NOT EXISTS runtime_secret_name VARCHAR(253) NOT NULL DEFAULT 'astronomer-dex-runtime',
    ADD COLUMN IF NOT EXISTS public_clients_encrypted TEXT NOT NULL DEFAULT '';

COMMENT ON COLUMN dex_settings.configmap_name IS
    'Deprecated compatibility alias. Must never identify a ConfigMap containing Dex runtime configuration.';
COMMENT ON COLUMN dex_settings.runtime_secret_name IS
    'Stable retained Secret mounted read-only by Dex; owned by the Dex runtime reconciler.';
COMMENT ON COLUMN dex_settings.public_clients IS
    'Legacy migration input only. Application code must atomically scrub this column to [] after Fernet migration.';
COMMENT ON COLUMN dex_settings.public_clients_encrypted IS
    'Fernet-encrypted canonical JSON array of Dex static clients.';

