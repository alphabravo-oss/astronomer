DROP TRIGGER IF EXISTS durable_json_validate_write ON public.user_table_views;

DELETE FROM public.durable_json_schemas
WHERE table_schema = 'public'
  AND table_name = 'user_table_views'
  AND column_name = 'state';

DROP TABLE IF EXISTS public.user_table_views;
