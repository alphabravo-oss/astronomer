\set ON_ERROR_STOP on

-- Exercise the same closed inventory used by server startup after the latest
-- migration, including columns introduced after migration 049's inventory.
DO $coverage$
DECLARE
  uncovered text;
BEGIN
  SELECT string_agg(table_schema || '.' || table_name || '.' || column_name, ', ')
  INTO uncovered
  FROM public.durable_json_schema_coverage
  WHERE NOT governed OR NOT writer_validation_enabled;
  IF uncovered IS NOT NULL THEN
    RAISE EXCEPTION 'durable JSON governance missing for: %', uncovered;
  END IF;
END
$coverage$;

BEGIN;
INSERT INTO public.users (id, email, username)
VALUES ('00000000-0000-4000-8000-000000000066', 'json-governance@example.invalid', 'json-governance-smoke');
INSERT INTO public.user_preferences (user_id, pinned_clusters, starred_types)
VALUES ('00000000-0000-4000-8000-000000000066', '["cluster-one"]', '["pods"]');

DO $writers$
DECLARE
  preference_column text;
BEGIN
  FOREACH preference_column IN ARRAY ARRAY['pinned_clusters', 'starred_types'] LOOP
    IF NOT EXISTS (
      SELECT 1 FROM public.durable_json_schemas
      WHERE table_schema = 'public' AND table_name = 'user_preferences'
        AND column_name = preference_column AND schema_version = 1
        AND json_type = 'array' AND NOT nullable
        AND compatibility_mode = 'additive' AND owner = 'platform'
    ) THEN
      RAISE EXCEPTION 'missing typed preference contract: %', preference_column;
    END IF;
    BEGIN
      EXECUTE format('UPDATE public.user_preferences SET %I = ''{}''::jsonb WHERE user_id = $1', preference_column)
        USING '00000000-0000-4000-8000-000000000066'::uuid;
      RAISE EXCEPTION 'invalid preference object accepted: %', preference_column;
    EXCEPTION WHEN check_violation THEN
      IF SQLERRM NOT LIKE 'durable JSON contract %' THEN
        RAISE EXCEPTION 'preference write bypassed governance trigger: %', SQLERRM;
      END IF;
    END;
  END LOOP;
  IF NOT EXISTS (
    SELECT 1 FROM public.user_preferences
    WHERE user_id = '00000000-0000-4000-8000-000000000066'
      AND pinned_clusters = '["cluster-one"]'::jsonb AND starred_types = '["pods"]'::jsonb
  ) THEN
    RAISE EXCEPTION 'rejected preference writes changed the existing values';
  END IF;
END
$writers$;
ROLLBACK;
