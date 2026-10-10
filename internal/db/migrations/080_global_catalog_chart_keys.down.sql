-- Application must stop using the candidate producer before this downgrade.
-- 081 must be cleanly downgraded first; exact projection ownership is verified
-- by the reviewed migrator allocation/recovery lane before destructive DDL.
BEGIN;
SET LOCAL lock_timeout='5s';
SET LOCAL statement_timeout='120s';
LOCK TABLE public.helm_repositories IN SHARE ROW EXCLUSIVE MODE;
LOCK TABLE public.helm_charts IN SHARE ROW EXCLUSIVE MODE;
DROP TRIGGER catalog_global_chart_key_after_owner ON public.helm_repositories;
DROP TRIGGER catalog_global_chart_key_after_chart ON public.helm_charts;
DROP FUNCTION public.refresh_catalog_global_repository_keys();
DROP FUNCTION public.refresh_catalog_global_chart_key();
DROP TRIGGER catalog_global_chart_key_lock_parents ON public.helm_charts;
DROP FUNCTION public.lock_catalog_global_chart_repositories();
DROP TABLE public.catalog_global_chart_keys;
COMMIT;
