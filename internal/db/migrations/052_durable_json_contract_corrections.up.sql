-- component_bundle_versions.requirements is a list of capability requirement
-- objects in every delivery writer and reader. The original empty-object
-- default caused migration 049 to infer the wrong durable JSON shape.
UPDATE public.durable_json_schemas
SET json_type = 'array',
    schema_version = 2,
    compatibility_mode = 'additive',
    owner = 'delivery'
WHERE table_schema = 'public'
  AND table_name = 'component_bundle_versions'
  AND column_name = 'requirements';

-- Only the old default shape is safe to translate automatically. Non-empty
-- objects are not valid capability requirements and are rejected below rather
-- than guessed into a new representation.
UPDATE public.component_bundle_versions
SET requirements = '[]'::jsonb
WHERE requirements = '{}'::jsonb;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM public.component_bundle_versions
        WHERE jsonb_typeof(requirements) <> 'array'
    ) THEN
        RAISE EXCEPTION 'component_bundle_versions.requirements contains a non-array value'
            USING ERRCODE = '23514';
    END IF;
END;
$$;

ALTER TABLE public.component_bundle_versions
    ALTER COLUMN requirements SET DEFAULT '[]'::jsonb;

-- security_scan_results.results stores scanner identity metadata as an object;
-- the actual per-check list lives in findings. Older empty-array placeholders
-- predate that split and are the only values that can be converted safely.
UPDATE public.durable_json_schemas
SET json_type = 'object',
    schema_version = 2,
    compatibility_mode = 'additive',
    owner = 'security'
WHERE table_schema = 'public'
  AND table_name = 'security_scan_results'
  AND column_name = 'results';

UPDATE public.security_scan_results
SET results = '{}'::jsonb
WHERE results = '[]'::jsonb;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM public.security_scan_results
        WHERE jsonb_typeof(results) <> 'object'
    ) THEN
        RAISE EXCEPTION 'security_scan_results.results contains a non-object value'
            USING ERRCODE = '23514';
    END IF;
END;
$$;

ALTER TABLE public.security_scan_results
    ALTER COLUMN results SET DEFAULT '{}'::jsonb;
