-- Restore migration 136's connector trigger exactly: connector writes advance
-- the runtime generation, preserve enabled provenance, and disable Dex SSO,
-- without migration 137's advisory lock or staged-writer bypass.
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

DROP TRIGGER IF EXISTS dex_connectors_runtime_generation ON dex_connectors;
CREATE TRIGGER dex_connectors_runtime_generation
AFTER INSERT OR UPDATE OR DELETE ON dex_connectors
FOR EACH ROW EXECUTE FUNCTION bump_dex_runtime_generation();
