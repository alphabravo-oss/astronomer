-- Remove only the registrations owned by this migration. Preference values
-- and the shared writer trigger (which also governs favorites) are preserved.
DELETE FROM public.durable_json_schemas
WHERE table_schema = 'public'
  AND table_name = 'user_preferences'
  AND column_name IN ('pinned_clusters', 'starred_types');
