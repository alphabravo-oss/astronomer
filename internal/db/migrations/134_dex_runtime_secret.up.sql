-- DEX-01: move Dex runtime material out of ConfigMaps and plaintext JSONB.
--
-- public_clients remains for one compatibility release as a dual-written
-- source. A separately-invoked, quiescence-gated keyrotate cutover encrypts the
-- last observed value, scrubs it, and stamps public_clients_cutover_at.
-- SQL cannot perform that encryption because the key is deliberately not
-- available to the database migration process.
ALTER TABLE dex_settings
    ADD COLUMN IF NOT EXISTS runtime_secret_name VARCHAR(253) NOT NULL DEFAULT 'astronomer-dex-runtime',
    ADD COLUMN IF NOT EXISTS public_clients_encrypted TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS public_clients_cutover_at TIMESTAMPTZ;

ALTER TABLE dex_settings ALTER COLUMN configmap_name TYPE VARCHAR(253);

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'dex_settings_public_clients_cutover'
          AND conrelid = 'dex_settings'::regclass
    ) THEN
        ALTER TABLE dex_settings ADD CONSTRAINT dex_settings_public_clients_cutover
            CHECK (public_clients_cutover_at IS NULL OR public_clients = '[]'::jsonb);
    END IF;
END $$;

COMMENT ON COLUMN dex_settings.configmap_name IS
    'Deprecated compatibility alias. Must never identify a ConfigMap containing Dex runtime configuration.';
COMMENT ON COLUMN dex_settings.runtime_secret_name IS
    'Stable retained Secret mounted read-only by Dex; owned by the Dex runtime reconciler.';
COMMENT ON COLUMN dex_settings.public_clients IS
    'Compatibility copy dual-written until an explicit quiesced Fernet cutover; must be [] after public_clients_cutover_at.';
COMMENT ON COLUMN dex_settings.public_clients_encrypted IS
    'Fernet-encrypted canonical JSON array of Dex static clients.';
COMMENT ON COLUMN dex_settings.public_clients_cutover_at IS
    'Durable cutover marker; non-null means old writers are prohibited and public_clients is scrubbed.';
