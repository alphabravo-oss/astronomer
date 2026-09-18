DROP INDEX IF EXISTS public.installed_charts_drift_claim_idx;

ALTER TABLE public.installed_charts
    DROP COLUMN IF EXISTS drift_locked_until,
    DROP COLUMN IF EXISTS drift_claim_token;

ALTER TABLE public.project_namespaces
    DROP COLUMN IF EXISTS reconcile_claim_token;

DROP INDEX IF EXISTS public.cluster_decommissions_claim_idx;

ALTER TABLE public.cluster_decommissions
    DROP COLUMN IF EXISTS decommission_claim_token,
    DROP COLUMN IF EXISTS decommission_lease_until;
