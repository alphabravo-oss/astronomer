-- Audit explorer/export access paths. Creating these on the partitioned parent
-- gives every existing child the same local index and makes PostgreSQL create
-- matching indexes for future children.
CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA public;

CREATE INDEX IF NOT EXISTS idx_audit_log_created_id
    ON public.audit_log (created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS idx_audit_log_search_document_trgm
    ON public.audit_log USING gin ((lower(
        coalesce(action, '') || ' ' ||
        coalesce(resource_type, '') || ' ' ||
        coalesce(resource_id, '') || ' ' ||
        coalesce(resource_name, '') || ' ' ||
        coalesce(path, '') || ' ' ||
        coalesce(actor_auth_method, '')
    )) public.gin_trgm_ops);

CREATE INDEX IF NOT EXISTS idx_audit_log_source_document_trgm
    ON public.audit_log USING gin ((lower(
        coalesce(source, '') || ' ' ||
        coalesce(user_agent, '')
    )) public.gin_trgm_ops);

CREATE INDEX IF NOT EXISTS idx_audit_log_detail_path
    ON public.audit_log USING gin (detail jsonb_path_ops);

CREATE INDEX IF NOT EXISTS idx_kubectl_sessions_active_started_at
    ON public.kubectl_sessions (started_at DESC, id DESC)
    WHERE status IN ('starting', 'active');
