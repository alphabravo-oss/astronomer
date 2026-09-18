DROP TRIGGER IF EXISTS ensure_cluster_default_projects_after_insert ON public.clusters;
DROP FUNCTION IF EXISTS public.ensure_cluster_default_projects_trigger();
DROP FUNCTION IF EXISTS public.ensure_cluster_default_projects(uuid);

