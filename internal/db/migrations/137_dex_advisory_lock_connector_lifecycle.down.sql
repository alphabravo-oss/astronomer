CREATE OR REPLACE FUNCTION bump_dex_runtime_generation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    UPDATE dex_settings SET runtime_generation = runtime_generation + 1, updated_at = now()
    WHERE id = '00000000-0000-0000-0000-000000000001'::uuid;
    RETURN COALESCE(NEW, OLD);
END;
$$;
CREATE TRIGGER dex_connectors_runtime_generation
AFTER INSERT OR UPDATE OR DELETE ON dex_connectors
FOR EACH ROW EXECUTE FUNCTION bump_dex_runtime_generation();
