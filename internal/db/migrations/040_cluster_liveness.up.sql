-- migration-phase: contract
-- compatibility-window: greenfield platform; no mixed-version heartbeat writers
-- destructive-change-approved: PERF-02 full-platform review

-- Heartbeat cadence belongs on a narrow row, not the wide clusters record.
-- The table intentionally has no last_heartbeat index: every connected agent
-- updates this value every 30 seconds, while fleet readers scan/join this small
-- relation on its primary key.
CREATE TABLE public.cluster_liveness (
    cluster_id uuid PRIMARY KEY REFERENCES public.clusters(id) ON DELETE CASCADE,
    last_heartbeat timestamptz,
    heartbeat_count bigint NOT NULL DEFAULT 0,
    commands_pending boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

INSERT INTO public.cluster_liveness (
    cluster_id,
    last_heartbeat,
    heartbeat_count,
    commands_pending
)
SELECT
    c.id,
    c.last_heartbeat,
    CASE WHEN c.last_heartbeat IS NULL THEN 0 ELSE 1 END,
    EXISTS (
        SELECT 1
        FROM public.agent_lifecycle_operations op
        WHERE op.cluster_id = c.id
          AND op.status IN ('pending', 'running')
    )
FROM public.clusters c;

-- The heartbeat dispatch path only enters the lifecycle claim query while a
-- cluster has actionable work. This index covers that uncommon path.
CREATE INDEX idx_agent_lifecycle_operations_actionable
    ON public.agent_lifecycle_operations (cluster_id, created_at, updated_at)
    WHERE status IN ('pending', 'running');

CREATE FUNCTION public.refresh_cluster_commands_pending()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    affected_cluster_id uuid;
BEGIN
    affected_cluster_id := COALESCE(NEW.cluster_id, OLD.cluster_id);

    INSERT INTO public.cluster_liveness (cluster_id, commands_pending)
    SELECT
        affected_cluster_id,
        EXISTS (
            SELECT 1
            FROM public.agent_lifecycle_operations op
            WHERE op.cluster_id = affected_cluster_id
              AND op.status IN ('pending', 'running')
        )
    -- A cascading cluster delete also deletes lifecycle operations. Do not
    -- recreate liveness after its owning cluster has disappeared.
    WHERE EXISTS (
        SELECT 1 FROM public.clusters c WHERE c.id = affected_cluster_id
    )
    ON CONFLICT (cluster_id) DO UPDATE
    SET commands_pending = EXISTS (
            SELECT 1
            FROM public.agent_lifecycle_operations op
            WHERE op.cluster_id = affected_cluster_id
              AND op.status IN ('pending', 'running')
        ),
        updated_at = now();

    -- AFTER-trigger return values are ignored.
    RETURN NULL;
END;
$$;

CREATE TRIGGER refresh_cluster_commands_pending
AFTER INSERT OR UPDATE OF status OR DELETE ON public.agent_lifecycle_operations
FOR EACH ROW EXECUTE FUNCTION public.refresh_cluster_commands_pending();

-- This index made every heartbeat update to the wide clusters tuple maintain
-- another btree entry. Liveness now lives in cluster_liveness.
DROP INDEX public.idx_clusters_heartbeat;
ALTER TABLE public.clusters DROP COLUMN last_heartbeat;
