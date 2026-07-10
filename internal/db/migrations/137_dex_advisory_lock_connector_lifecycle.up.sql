-- Mixed-version compatibility: old writers still use direct connector DML.
-- New staged writers and keyrotate set the transaction-local bypass around
-- their own statement so this trigger cannot double-stage.
CREATE OR REPLACE FUNCTION bump_dex_runtime_generation() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE was_enabled boolean;
BEGIN
    IF current_setting('astronomer.dex_connector_stage_bypass', true) = '1' THEN
        PERFORM set_config('astronomer.dex_connector_stage_bypass', '', true);
        RETURN COALESCE(NEW, OLD);
    END IF;
    PERFORM pg_advisory_xact_lock(742193440558879931);
    SELECT COALESCE((SELECT is_enabled FROM sso_configurations WHERE provider='dex'),false)
        OR COALESCE((SELECT saga_previous_sso_enabled FROM dex_settings WHERE id='00000000-0000-0000-0000-000000000001'::uuid),false)
    INTO was_enabled;
    WITH staged AS (
        UPDATE dex_settings SET runtime_generation=runtime_generation+1,
            saga_previous_sso_enabled=was_enabled,updated_at=now()
        WHERE id='00000000-0000-0000-0000-000000000001'::uuid RETURNING 1
    )
    UPDATE sso_configurations SET is_enabled=false,updated_at=now()
    WHERE provider='dex' AND is_enabled=true AND EXISTS(SELECT 1 FROM staged);
    RETURN COALESCE(NEW, OLD);
END;
$$;
DROP TRIGGER IF EXISTS dex_connectors_runtime_generation ON dex_connectors;
CREATE TRIGGER dex_connectors_runtime_generation AFTER INSERT OR UPDATE OR DELETE ON dex_connectors
FOR EACH ROW EXECUTE FUNCTION bump_dex_runtime_generation();
