-- Keep the active fleet's default sort index-backed without blocking writes.
-- One concurrent index per migration is deliberate: PostgreSQL rejects
-- CREATE INDEX CONCURRENTLY when several statements share an implicit txn.
CREATE INDEX CONCURRENTLY IF NOT EXISTS clusters_active_created_id_idx
    ON public.clusters (created_at DESC, id DESC)
    WHERE decommissioned_at IS NULL;
