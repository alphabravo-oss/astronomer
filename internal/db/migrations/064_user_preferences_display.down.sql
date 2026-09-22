-- migration-phase: contract
-- compatibility-window: down-migration only
-- destructive-change-approved: advisor-plan-024

ALTER TABLE public.user_preferences DROP COLUMN IF EXISTS rows_per_page;
ALTER TABLE public.user_preferences DROP COLUMN IF EXISTS date_format;
