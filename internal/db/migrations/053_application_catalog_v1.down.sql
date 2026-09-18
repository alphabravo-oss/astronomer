DROP TABLE IF EXISTS public.delivery_application_versions;
DROP INDEX IF EXISTS public.idx_catalog_blessed_charts_facets;
DROP INDEX IF EXISTS public.catalog_blessed_charts_slug_source_key;

ALTER TABLE public.catalog_blessed_charts
    DROP COLUMN IF EXISTS revoked,
    DROP COLUMN IF EXISTS verification_identity,
    DROP COLUMN IF EXISTS verification_status,
    DROP COLUMN IF EXISTS catalog_digest,
    DROP COLUMN IF EXISTS raw_entry,
    DROP COLUMN IF EXISTS lifecycle,
    DROP COLUMN IF EXISTS storage,
    DROP COLUMN IF EXISTS resources,
    DROP COLUMN IF EXISTS compatibility,
    DROP COLUMN IF EXISTS artifact,
    DROP COLUMN IF EXISTS presentation,
    DROP COLUMN IF EXISTS documentation_url,
    DROP COLUMN IF EXISTS default_enabled,
    DROP COLUMN IF EXISTS privileged,
    DROP COLUMN IF EXISTS featured,
    DROP COLUMN IF EXISTS support_tier,
    DROP COLUMN IF EXISTS repo_name,
    DROP COLUMN IF EXISTS slug;

DROP TABLE IF EXISTS public.delivery_catalogs;
