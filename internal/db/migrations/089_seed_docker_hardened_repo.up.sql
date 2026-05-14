-- Sprint 089 — seed the Docker Hardened Images chart repository (T7.5).
--
-- Why: todo-mj.md L141 calls out that the seeded helm repos lean on
-- upstream bitnami/aqua/jetstack/prometheus-community. The Docker
-- Hardened Images catalog (github.com/docker-hardened-images/catalog)
-- ships CIS-baselined, minimal-image charts with the same kind of
-- well-known upstreams operators already trust, and is a meaningfully
-- better default for buyers who care about supply-chain hygiene.
--
-- Operators can disable the row via the existing /api/v1/helm-repositories
-- enabled toggle if they prefer not to ship it; the row is added with
-- enabled=true so the default install gets the value without a tour
-- of admin settings.
--
-- Idempotent: ON CONFLICT (name) DO NOTHING. An operator who manually
-- added this repo earlier keeps their existing row (URL + description
-- intact).

INSERT INTO helm_repositories (id, name, url, repo_type, description, enabled, auth_type, created_at, updated_at)
VALUES
    (gen_random_uuid(), 'docker-hardened-images',
     'https://github.com/docker-hardened-images/catalog/raw/main/chart',
     'helm',
     'Docker Hardened Images — CIS-baselined minimal-image charts. Seeded by sprint 089.',
     true, 'none', now(), now())
ON CONFLICT (name) DO NOTHING;
