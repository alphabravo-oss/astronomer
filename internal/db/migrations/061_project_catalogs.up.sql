-- Per-project ("BYO") Helm catalogs — sprint 061.
--
-- The existing helm_repositories table (added in migration 001) was implicitly
-- "global": every row was visible to every project, and only superusers could
-- mutate. Sprint 061 makes catalogs project-scoped so a project admin can
-- subscribe their project to e.g. https://charts.bitnami.com/bitnami without
-- superuser involvement, while still seeing the operator-curated global set.
--
-- Model:
--   - owner_project_id IS NULL  ⇒ "public" / global catalog (legacy behaviour;
--     all existing rows fall in this bucket).
--   - owner_project_id IS NOT NULL ⇒ private to that project. The row is
--     auto-subscribed via project_catalog_subscriptions on create. Foreign
--     projects can't see or subscribe to a private catalog unless the caller
--     is a superuser (shared-curation projects bypass).
--
-- Project deletion semantics:
--   - ON DELETE CASCADE on helm_repositories.owner_project_id drops project-
--     owned catalogs along with the project (and, by transitive cascade, all
--     of their charts/versions and any subscription rows).
--   - Public catalogs (owner_project_id IS NULL) survive a project delete —
--     other projects still rely on them.
--
-- Unsubscribe semantics (enforced in handler):
--   - Deleting a subscription to a global keeps the catalog row alive.
--   - Deleting a subscription to a project-owned catalog deletes the catalog
--     row itself (the subscribing project IS the owner; the catalog has no
--     other reason to live). The CASCADE on the subscriptions table picks up
--     any other subscriptions.
--
-- Migration safety:
--   - The new column is nullable with no DEFAULT, satisfying the
--     check-migrations.sh T30 lint requirement around populated tables.
--   - The partial index on owner_project_id only covers private rows so the
--     existing-global index footprint stays flat.

ALTER TABLE helm_repositories ADD COLUMN IF NOT EXISTS owner_project_id UUID
    REFERENCES projects(id) ON DELETE CASCADE;
CREATE INDEX IF NOT EXISTS idx_helm_repositories_owner_project
    ON helm_repositories (owner_project_id) WHERE owner_project_id IS NOT NULL;

CREATE TABLE project_catalog_subscriptions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id      UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    catalog_id      UUID NOT NULL REFERENCES helm_repositories(id) ON DELETE CASCADE,
    created_by      UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (project_id, catalog_id)
);
CREATE INDEX idx_project_catalog_subs_project ON project_catalog_subscriptions (project_id);
CREATE INDEX idx_project_catalog_subs_catalog ON project_catalog_subscriptions (catalog_id);
