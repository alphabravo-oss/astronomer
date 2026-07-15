-- Durable Dex saga phase/state. `runtime_staged_generation` means the exact
-- generation has been written back and verified in the owned Secret. Applied
-- remains stronger: the Secret-mounted Deployment and health endpoint were
-- verified. The previous enabled bit is captured by the atomic staging query
-- and may only be restored through a generation-CAS statement.
ALTER TABLE dex_settings
    ADD COLUMN IF NOT EXISTS runtime_phase TEXT NOT NULL DEFAULT 'fresh',
    ADD COLUMN IF NOT EXISTS runtime_staged_generation BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS saga_previous_sso_enabled BOOLEAN NOT NULL DEFAULT false;

UPDATE dex_settings
SET runtime_staged_generation = runtime_applied_generation;

ALTER TABLE dex_settings
    ADD CONSTRAINT dex_settings_runtime_phase_valid
        CHECK (runtime_phase IN ('fresh', 'prepare', 'cutover')),
    ADD CONSTRAINT dex_settings_runtime_stage_order_valid
        CHECK (
            runtime_staged_generation >= 0
            AND runtime_applied_generation <= runtime_staged_generation
            AND runtime_staged_generation <= runtime_generation
        );

COMMENT ON COLUMN dex_settings.runtime_phase IS
    'fresh/cutover require Secret-mounted rollout+health; prepare stops after verified Secret staging.';
COMMENT ON COLUMN dex_settings.runtime_staged_generation IS
    'Last generation verified in the owned runtime Secret, before Deployment activation.';
COMMENT ON COLUMN dex_settings.saga_previous_sso_enabled IS
    'SSO enabled snapshot captured atomically with generation staging; restoration is generation-CAS guarded.';

-- Connector writes are runtime writes too. Preserve the original enabled
-- provenance across overlapping generations and disable Dex SSO in the same
-- SQL statement that advances the generation. The trigger transaction also
-- rolls back the connector mutation if this statement cannot complete.
CREATE OR REPLACE FUNCTION bump_dex_runtime_generation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    WITH previous_sso AS MATERIALIZED (
        SELECT
            COALESCE((SELECT is_enabled FROM sso_configurations WHERE provider = 'dex'), false)
            OR COALESCE((
                SELECT saga_previous_sso_enabled
                FROM dex_settings
                WHERE id = '00000000-0000-0000-0000-000000000001'::uuid
            ), false) AS was_enabled
    ), staged AS (
        UPDATE dex_settings
        SET runtime_generation = runtime_generation + 1,
            saga_previous_sso_enabled = previous_sso.was_enabled,
            updated_at = now()
        FROM previous_sso
        WHERE id = '00000000-0000-0000-0000-000000000001'::uuid
        RETURNING 1
    )
    UPDATE sso_configurations
    SET is_enabled = false, updated_at = now()
    WHERE provider = 'dex'
      AND is_enabled = true
      AND EXISTS (SELECT 1 FROM staged);
    RETURN COALESCE(NEW, OLD);
END;
$$;
