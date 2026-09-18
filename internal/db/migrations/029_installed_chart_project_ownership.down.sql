DROP INDEX IF EXISTS public.idx_installed_charts_project;
ALTER TABLE public.installed_charts DROP COLUMN IF EXISTS project_id;

