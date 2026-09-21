-- migration-phase: contract
-- compatibility-window: down-migration only
-- destructive-change-approved: advisor-plan-019

ALTER TABLE public.user_preferences DROP COLUMN IF EXISTS pinned_clusters;
