DROP VIEW IF EXISTS public.durable_json_schema_coverage;

DO $$
DECLARE
    target record;
BEGIN
    FOR target IN
        SELECT DISTINCT table_schema, table_name
        FROM public.durable_json_schemas
    LOOP
        IF to_regclass(format('%I.%I', target.table_schema, target.table_name)) IS NOT NULL THEN
            EXECUTE format(
                'DROP TRIGGER IF EXISTS durable_json_validate_write ON %I.%I',
                target.table_schema, target.table_name
            );
        END IF;
    END LOOP;
END;
$$;

DROP FUNCTION IF EXISTS public.validate_durable_jsonb_write();
DROP TABLE IF EXISTS public.durable_json_schemas;
