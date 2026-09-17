-- Durable JSONB is an application contract, not an untyped escape hatch. This
-- registry gives every durable JSONB column a versioned writer contract and a
-- bounded encoded size. The trigger validates every INSERT/UPDATE centrally,
-- including worker and maintenance writers that do not pass through HTTP.
CREATE TABLE public.durable_json_schemas (
    table_schema       text NOT NULL DEFAULT 'public',
    table_name         text NOT NULL,
    column_name        text NOT NULL,
    schema_version     integer NOT NULL DEFAULT 1 CHECK (schema_version > 0),
    json_type          text NOT NULL CHECK (json_type IN ('any', 'array', 'object', 'string', 'number', 'boolean')),
    max_bytes          integer NOT NULL DEFAULT 16777216 CHECK (max_bytes BETWEEN 2 AND 16777216),
    nullable           boolean NOT NULL,
    required_keys      text[] NOT NULL DEFAULT '{}',
    compatibility_mode text NOT NULL DEFAULT 'additive' CHECK (compatibility_mode IN ('additive', 'exact', 'opaque')),
    owner              text NOT NULL DEFAULT 'platform',
    PRIMARY KEY (table_schema, table_name, column_name)
);

-- Keep catalog discovery inside PL/pgSQL so the application role executes it
-- at migration time; sqlc's offline schema parser deliberately has no system
-- catalog and must not mistake pg_attribute for an application relation.
DO $json_contract_inventory$
BEGIN
WITH json_columns AS (
    SELECT
        namespace.nspname AS table_schema,
        relation.relname AS table_name,
        attribute.attname AS column_name,
        attribute.attnotnull AS not_null,
        pg_get_expr(default_value.adbin, default_value.adrelid) AS default_expression
    FROM pg_attribute AS attribute
    JOIN pg_class AS relation ON relation.oid = attribute.attrelid
    JOIN pg_namespace AS namespace ON namespace.oid = relation.relnamespace
    LEFT JOIN pg_attrdef AS default_value
        ON default_value.adrelid = relation.oid
       AND default_value.adnum = attribute.attnum
    WHERE namespace.nspname = 'public'
      AND relation.relkind IN ('r', 'p')
      AND attribute.attnum > 0
      AND NOT attribute.attisdropped
      AND attribute.atttypid = 'jsonb'::regtype
      -- A trigger on a partitioned parent governs its child partitions. Keep
      -- the registry canonical by omitting inherited partition columns.
      AND NOT EXISTS (
          SELECT 1 FROM pg_inherits WHERE inhrelid = relation.oid
      )
)
INSERT INTO public.durable_json_schemas (
    table_schema, table_name, column_name, json_type, nullable,
    compatibility_mode, owner
)
SELECT
    table_schema,
    table_name,
    column_name,
    CASE
        WHEN default_expression LIKE '''[]''::jsonb%' THEN 'array'
        WHEN default_expression LIKE '''{}''::jsonb%' THEN 'object'
        ELSE 'any'
    END,
    NOT not_null,
    CASE WHEN column_name IN ('raw', 'payload', 'response', 'value') THEN 'opaque' ELSE 'additive' END,
    CASE
        WHEN table_name LIKE 'audit_%' OR table_name = 'audit_log' THEN 'audit'
        WHEN table_name LIKE 'delivery_%' OR table_name IN ('component_bundle_versions', 'cluster_deployments') THEN 'delivery'
        WHEN table_name LIKE 'security_%' OR table_name LIKE 'mirrored_%' THEN 'security'
        ELSE 'platform'
    END
FROM json_columns;
END;
$json_contract_inventory$;

-- Columns without a literal object/array default still have known domain
-- shapes. Nullable values remain valid, but every non-null write must match.
UPDATE public.durable_json_schemas AS schema
SET json_type = override.json_type,
    compatibility_mode = override.compatibility_mode
FROM (VALUES
    ('alert_rules', 'configuration', 'object', 'additive'),
    ('apiserver_allowlist_snapshots', 'desired_cidrs', 'array', 'additive'),
    ('apiserver_allowlist_snapshots', 'effective_cidrs', 'array', 'additive'),
    ('audit_export_operations', 'request_spec', 'object', 'additive'),
    ('cluster_deployments', 'previous_overrides', 'object', 'additive'),
    ('compliance_baseline_applications', 'previous_state', 'object', 'additive'),
    ('compliance_baselines', 'spec', 'object', 'additive'),
    ('delivery_rollouts', 'frozen_plan', 'object', 'additive'),
    ('delivery_system_releases', 'verification_policy', 'object', 'additive'),
    ('delivery_system_rollouts', 'strategy', 'object', 'additive'),
    ('logging_outputs', 'configuration', 'object', 'additive'),
    ('notification_channels', 'configuration', 'object', 'additive'),
    ('siem_forward_queue', 'payload', 'object', 'opaque'),
    ('ui_extensions', 'manifest', 'object', 'additive')
) AS override(table_name, column_name, json_type, compatibility_mode)
WHERE schema.table_schema = 'public'
  AND schema.table_name = override.table_name
  AND schema.column_name = override.column_name;

CREATE FUNCTION public.validate_durable_jsonb_write()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    contract public.durable_json_schemas%ROWTYPE;
    document jsonb;
    required_key text;
BEGIN
    FOR contract IN
        SELECT *
        FROM public.durable_json_schemas
        WHERE table_schema = TG_TABLE_SCHEMA
          AND (
              table_name = TG_TABLE_NAME
              OR table_name IN (
                  SELECT parent.relname
                  FROM pg_inherits AS inheritance
                  JOIN pg_class AS parent ON parent.oid = inheritance.inhparent
                  JOIN pg_namespace AS parent_namespace ON parent_namespace.oid = parent.relnamespace
                  WHERE inheritance.inhrelid = TG_RELID
                    AND parent_namespace.nspname = TG_TABLE_SCHEMA
              )
          )
        ORDER BY column_name
    LOOP
        document := to_jsonb(NEW) -> contract.column_name;
        IF document IS NULL THEN
            IF NOT contract.nullable THEN
                RAISE EXCEPTION 'durable JSON contract %.%.% v% rejects SQL NULL',
                    contract.table_schema, contract.table_name, contract.column_name, contract.schema_version
                    USING ERRCODE = '23514';
            END IF;
            CONTINUE;
        END IF;

        IF jsonb_typeof(document) = 'null' THEN
            -- to_jsonb(NEW) represents SQL NULL as JSON null. Nullable columns
            -- therefore accept this branch; non-null opaque/any columns may
            -- deliberately use JSON null (for example platform settings).
            IF NOT contract.nullable AND contract.json_type <> 'any' THEN
                RAISE EXCEPTION 'durable JSON contract %.%.% v% rejects JSON null',
                    contract.table_schema, contract.table_name, contract.column_name, contract.schema_version
                    USING ERRCODE = '23514';
            END IF;
            CONTINUE;
        END IF;

        IF contract.json_type <> 'any' AND jsonb_typeof(document) <> contract.json_type THEN
            RAISE EXCEPTION 'durable JSON contract %.%.% v% requires %, received %',
                contract.table_schema, contract.table_name, contract.column_name, contract.schema_version,
                contract.json_type, jsonb_typeof(document)
                USING ERRCODE = '23514';
        END IF;

        IF pg_column_size(document) > contract.max_bytes THEN
            RAISE EXCEPTION 'durable JSON contract %.%.% v% exceeds % bytes',
                contract.table_schema, contract.table_name, contract.column_name, contract.schema_version,
                contract.max_bytes
                USING ERRCODE = '22001';
        END IF;

        FOREACH required_key IN ARRAY contract.required_keys LOOP
            IF jsonb_typeof(document) <> 'object' OR NOT document ? required_key THEN
                RAISE EXCEPTION 'durable JSON contract %.%.% v% requires key %',
                    contract.table_schema, contract.table_name, contract.column_name, contract.schema_version,
                    required_key
                    USING ERRCODE = '23514';
            END IF;
        END LOOP;
    END LOOP;
    RETURN NEW;
END;
$$;

DO $$
DECLARE
    target record;
BEGIN
    FOR target IN
        SELECT DISTINCT table_schema, table_name
        FROM public.durable_json_schemas
        ORDER BY table_schema, table_name
    LOOP
        EXECUTE format(
            'CREATE TRIGGER durable_json_validate_write BEFORE INSERT OR UPDATE ON %I.%I '
            'FOR EACH ROW EXECUTE FUNCTION public.validate_durable_jsonb_write()',
            target.table_schema, target.table_name
        );
    END LOOP;
END;
$$;

-- Readiness consumes this closed inventory. A future JSONB column without a
-- registry row, or a table missing its writer trigger, holds the service out of
-- rotation until the migration supplies an explicit compatibility contract.
DO $json_contract_coverage$
BEGIN
EXECUTE $coverage_view$
CREATE VIEW public.durable_json_schema_coverage AS
SELECT
    namespace.nspname AS table_schema,
    relation.relname AS table_name,
    attribute.attname AS column_name,
    (schema.column_name IS NOT NULL) AS governed,
    EXISTS (
        SELECT 1
        FROM pg_trigger AS trigger
        WHERE trigger.tgrelid = relation.oid
          AND trigger.tgname = 'durable_json_validate_write'
          AND NOT trigger.tgisinternal
    ) AS writer_validation_enabled
FROM pg_attribute AS attribute
JOIN pg_class AS relation ON relation.oid = attribute.attrelid
JOIN pg_namespace AS namespace ON namespace.oid = relation.relnamespace
LEFT JOIN public.durable_json_schemas AS schema
    ON schema.table_schema = namespace.nspname
   AND schema.table_name = relation.relname
   AND schema.column_name = attribute.attname
WHERE namespace.nspname = 'public'
  AND relation.relkind IN ('r', 'p')
  AND attribute.attnum > 0
  AND NOT attribute.attisdropped
  AND attribute.atttypid = 'jsonb'::regtype
  AND relation.relname <> 'durable_json_schemas'
  AND NOT EXISTS (
      SELECT 1 FROM pg_inherits WHERE inhrelid = relation.oid
  )
$coverage_view$;
END;
$json_contract_coverage$;
