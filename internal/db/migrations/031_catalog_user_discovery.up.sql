CREATE TABLE public.catalog_user_discovery (
    user_id uuid NOT NULL REFERENCES public.users(id) ON DELETE CASCADE,
    chart_id uuid NOT NULL REFERENCES public.helm_charts(id) ON DELETE CASCADE,
    favorite boolean NOT NULL DEFAULT false,
    favorite_at timestamptz,
    last_viewed_at timestamptz,
    view_count integer NOT NULL DEFAULT 0 CHECK (view_count >= 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, chart_id)
);

CREATE INDEX catalog_user_discovery_recent_idx
    ON public.catalog_user_discovery (user_id, last_viewed_at DESC)
    WHERE last_viewed_at IS NOT NULL;

CREATE INDEX catalog_user_discovery_favorite_idx
    ON public.catalog_user_discovery (user_id, favorite_at DESC)
    WHERE favorite = true;

CREATE TRIGGER set_updated_at
    BEFORE UPDATE ON public.catalog_user_discovery
    FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();
