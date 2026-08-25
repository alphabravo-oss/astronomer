-- Private, API-backed logging saved searches. Query text is bounded and never
-- copied into audit detail; ownership and output/cluster authorization are
-- rechecked by the handler on every read and mutation.
CREATE TABLE public.logging_saved_searches (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    output_id uuid NOT NULL REFERENCES public.logging_outputs(id) ON DELETE CASCADE,
    owner_user_id uuid NOT NULL REFERENCES public.users(id) ON DELETE CASCADE,
    name varchar(120) NOT NULL,
    query_text text NOT NULL DEFAULT '',
    namespaces text[] NOT NULL DEFAULT '{}'::text[],
    result_limit integer NOT NULL DEFAULT 100,
    direction varchar(16) NOT NULL DEFAULT 'backward',
    live_tail boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT logging_saved_searches_name_nonempty CHECK (length(btrim(name)) BETWEEN 1 AND 120),
    CONSTRAINT logging_saved_searches_query_bounded CHECK (octet_length(query_text) <= 16384),
    CONSTRAINT logging_saved_searches_namespaces_bounded CHECK (cardinality(namespaces) <= 20),
    CONSTRAINT logging_saved_searches_result_limit_bounded CHECK (result_limit BETWEEN 1 AND 1000),
    CONSTRAINT logging_saved_searches_direction_valid CHECK (direction IN ('forward', 'backward')),
    CONSTRAINT logging_saved_searches_owner_name_unique UNIQUE (owner_user_id, output_id, name)
);

CREATE INDEX logging_saved_searches_owner_output_updated_idx
    ON public.logging_saved_searches (owner_user_id, output_id, updated_at DESC, id DESC);
