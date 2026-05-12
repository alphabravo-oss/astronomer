-- Sprint 075 — seed the recommended helm repositories so the platform-baseline
-- cluster_template (sprint 074) resolves its five tool slugs out-of-the-box.
--
-- On a fresh install no helm_repositories are configured, so the reconciler
-- can't resolve the baseline slugs (trivy-operator, kube-state-metrics,
-- node-exporter, fluent-bit, cert-manager) and all five tool installs fail
-- per-row. Seeding three well-known upstream repos here, plus a first-boot
-- catalog:sync kick from the server, closes that gap.
--
-- All inserts are idempotent: re-running on an already-migrated DB is a
-- no-op via ON CONFLICT (name) DO NOTHING. The unique key is `name` —
-- internal/db/migrations/001_initial.up.sql declares `name VARCHAR(255)
-- NOT NULL UNIQUE` on helm_repositories.
--
-- Operators who've already added these repos keep their existing rows
-- (including operator-edited URLs pointing at a private mirror) — the
-- ON CONFLICT clause means this migration never overrides an operator
-- customization. Operators who add or remove repos after this migration
-- runs are likewise never touched on a subsequent re-run.

INSERT INTO helm_repositories (id, name, url, repo_type, description, enabled, auth_type, created_at, updated_at)
VALUES
    (gen_random_uuid(), 'bitnami',  'https://charts.bitnami.com/bitnami',
     'helm', 'Bitnami chart repository — kube-state-metrics, node-exporter, fluent-bit, others. Seeded by sprint 075.',
     true, 'none', now(), now()),
    (gen_random_uuid(), 'aqua',     'https://aquasecurity.github.io/helm-charts',
     'helm', 'Aqua Security — trivy-operator and related security charts. Seeded by sprint 075.',
     true, 'none', now(), now()),
    (gen_random_uuid(), 'jetstack', 'https://charts.jetstack.io',
     'helm', 'Jetstack — cert-manager. Seeded by sprint 075.',
     true, 'none', now(), now())
ON CONFLICT (name) DO NOTHING;
