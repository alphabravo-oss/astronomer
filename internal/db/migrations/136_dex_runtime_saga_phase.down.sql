CREATE OR REPLACE FUNCTION bump_dex_runtime_generation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    UPDATE dex_settings
    SET runtime_generation = runtime_generation + 1, updated_at = now()
    WHERE id = '00000000-0000-0000-0000-000000000001'::uuid;
    RETURN COALESCE(NEW, OLD);
END;
$$;

ALTER TABLE dex_settings
    DROP CONSTRAINT IF EXISTS dex_settings_runtime_stage_order_valid,
    DROP CONSTRAINT IF EXISTS dex_settings_runtime_phase_valid,
    DROP COLUMN IF EXISTS saga_previous_sso_enabled,
    DROP COLUMN IF EXISTS runtime_staged_generation,
    DROP COLUMN IF EXISTS runtime_phase;
