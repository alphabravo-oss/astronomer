-- Named, per-user saved DataTable views (filters, sort, visible columns,
-- order, pinning). Server-owned so views follow the operator across browsers.
CREATE TABLE public.user_table_views (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES public.users(id) ON DELETE CASCADE,
    table_key text NOT NULL
        CHECK (char_length(table_key) BETWEEN 1 AND 128),
    name text NOT NULL
        CHECK (char_length(name) BETWEEN 1 AND 64),
    state jsonb NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(state) = 'object'),
    is_default boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT user_table_views_user_table_name_key UNIQUE (user_id, table_key, name)
);

-- At most one default view per user and table.
CREATE UNIQUE INDEX user_table_views_one_default_idx
    ON public.user_table_views (user_id, table_key)
    WHERE is_default;

COMMENT ON TABLE public.user_table_views IS
    'Named saved DataTable views per user and table key; capped at 20 per table by the API.';

INSERT INTO public.durable_json_schemas (
    table_schema, table_name, column_name, schema_version, json_type,
    max_bytes, nullable, required_keys, compatibility_mode, owner
)
VALUES
    ('public', 'user_table_views', 'state', 1, 'object', 65536, false, '{}', 'additive', 'platform');

CREATE TRIGGER durable_json_validate_write
    BEFORE INSERT OR UPDATE ON public.user_table_views
    FOR EACH ROW EXECUTE FUNCTION public.validate_durable_jsonb_write();
