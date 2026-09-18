-- Keep values written under the corrected contracts readable during rollback.
-- Restoring either incorrect shape would reject valid data, so the rollback
-- contracts are deliberately opaque while their historical defaults return.
UPDATE public.durable_json_schemas
SET json_type = 'any',
    schema_version = 1,
    compatibility_mode = 'opaque',
    owner = 'delivery'
WHERE table_schema = 'public'
  AND table_name = 'component_bundle_versions'
  AND column_name = 'requirements';

ALTER TABLE public.component_bundle_versions
    ALTER COLUMN requirements SET DEFAULT '{}'::jsonb;

UPDATE public.durable_json_schemas
SET json_type = 'any',
    schema_version = 1,
    compatibility_mode = 'opaque',
    owner = 'security'
WHERE table_schema = 'public'
  AND table_name = 'security_scan_results'
  AND column_name = 'results';

ALTER TABLE public.security_scan_results
    ALTER COLUMN results SET DEFAULT '[]'::jsonb;
