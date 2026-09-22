-- migration-phase: contract
-- compatibility-window: down-migration only
-- destructive-change-approved: advisor-plan-020

ALTER TABLE public.user_preferences DROP COLUMN IF EXISTS starred_types;
