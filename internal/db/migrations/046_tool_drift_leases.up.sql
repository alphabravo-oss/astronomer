ALTER TABLE public.installed_charts
    ADD COLUMN drift_locked_until timestamptz,
    ADD COLUMN drift_claim_token uuid;

CREATE INDEX installed_charts_drift_claim_idx
    ON public.installed_charts (drift_locked_until, drift_checked_at, updated_at)
    WHERE status IN ('installed', 'deployed', 'upgraded');

ALTER TABLE public.project_namespaces
    ADD COLUMN reconcile_claim_token uuid;

ALTER TABLE public.cluster_decommissions
    ADD COLUMN decommission_claim_token uuid,
    ADD COLUMN decommission_lease_until timestamptz;

-- The periodic decommission sweep orders by updated_at so a row that was just
-- attempted moves behind never-tried and older work. Live leases are filtered
-- by the claim query; SKIP LOCKED provides disjoint batches across replicas.
CREATE INDEX cluster_decommissions_claim_idx
    ON public.cluster_decommissions (updated_at, created_at, id)
    WHERE status IN ('pending', 'failed', 'running');
