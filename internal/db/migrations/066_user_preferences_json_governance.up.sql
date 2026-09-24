-- These columns were added after migration 049's closed JSONB inventory.
-- The existing user_preferences trigger validates all registered columns;
-- preserve the array shape, nullability and existing 20-item CHECK constraints.
INSERT INTO public.durable_json_schemas (
    table_schema, table_name, column_name, schema_version, json_type,
    max_bytes, nullable, required_keys, compatibility_mode, owner
)
VALUES
    ('public', 'user_preferences', 'pinned_clusters', 1, 'array', 16777216, false, '{}', 'additive', 'platform'),
    ('public', 'user_preferences', 'starred_types', 1, 'array', 16777216, false, '{}', 'additive', 'platform');
