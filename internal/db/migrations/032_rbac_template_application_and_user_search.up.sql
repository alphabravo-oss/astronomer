-- Materialized RBAC templates are immutable, catalog-managed project roles.
-- The digest makes each effective rule set append-only: a catalog change
-- creates a new role instead of silently widening existing bindings.
ALTER TABLE public.project_roles
    ADD COLUMN source_template varchar(128),
    ADD COLUMN source_digest char(64),
    ADD CONSTRAINT project_roles_template_source_complete CHECK (
        (source_template IS NULL AND source_digest IS NULL)
        OR (source_template IS NOT NULL AND source_template <> '' AND source_digest IS NOT NULL)
    );

CREATE UNIQUE INDEX uq_project_roles_template_digest
    ON public.project_roles (source_template, source_digest)
    WHERE source_template IS NOT NULL AND source_digest IS NOT NULL;

-- Back server-side identity lookup used by RBAC subject pickers. The query
-- uses this exact expression so PostgreSQL can use the trigram index for
-- substring matches across username, email, and display name fields.
CREATE INDEX idx_users_directory_search_trgm
    ON public.users USING gin (
        lower(
            coalesce(username, '') || ' ' ||
            coalesce(email, '') || ' ' ||
            coalesce(first_name, '') || ' ' ||
            coalesce(last_name, '')
        ) public.gin_trgm_ops
    )
    WHERE is_service = false;
