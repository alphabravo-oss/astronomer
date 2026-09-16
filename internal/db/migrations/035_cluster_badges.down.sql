ALTER TABLE public.clusters
    DROP CONSTRAINT IF EXISTS clusters_badge_pair_valid,
    DROP CONSTRAINT IF EXISTS clusters_badge_color_valid,
    DROP COLUMN IF EXISTS badge_color,
    DROP COLUMN IF EXISTS badge_text;
