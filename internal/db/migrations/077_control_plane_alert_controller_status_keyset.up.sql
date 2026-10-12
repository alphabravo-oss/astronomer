-- migration-phase: expand
-- Match both direct cursor predicates and full timestamp/ID ordering.
CREATE INDEX CONCURRENTLY idx_control_plane_alerts_controller_status_keyset
    ON public.control_plane_alerts (controller, status, fired_at DESC, id DESC);
