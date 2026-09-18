DROP INDEX IF EXISTS public.idx_kubectl_sessions_active_started_at;
DROP INDEX IF EXISTS public.idx_audit_log_detail_path;
DROP INDEX IF EXISTS public.idx_audit_log_source_document_trgm;
DROP INDEX IF EXISTS public.idx_audit_log_search_document_trgm;
DROP INDEX IF EXISTS public.idx_audit_log_created_id;

-- pg_trgm is intentionally retained: extensions are database-level shared
-- infrastructure and may be used by operator-created indexes.
