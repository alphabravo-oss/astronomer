DROP INDEX IF EXISTS public.idx_users_directory_search_trgm;
DROP INDEX IF EXISTS public.uq_project_roles_template_digest;

ALTER TABLE public.project_roles
    DROP COLUMN IF EXISTS source_digest,
    DROP COLUMN IF EXISTS source_template;
