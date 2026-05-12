-- Sprint 077 — seed the prometheus-community helm repository.
--
-- Sprint 075 seeded bitnami / aqua / jetstack. During live verification we
-- discovered that "prometheus-node-exporter" (referenced by the platform-
-- baseline cluster_template after sprint 076's slug fix) is actually
-- published by prometheus-community, not bitnami. Seeding the fourth repo
-- closes the last gap so all five baseline slugs resolve out-of-the-box.
--
-- Idempotent: ON CONFLICT (name) DO NOTHING preserves any operator-added
-- row already present.

INSERT INTO helm_repositories (id, name, url, repo_type, description, enabled, auth_type, created_at, updated_at)
VALUES (
    gen_random_uuid(),
    'prometheus-community',
    'https://prometheus-community.github.io/helm-charts',
    'helm',
    'Prometheus community charts — prometheus-node-exporter, prometheus, alertmanager, kube-prometheus-stack. Seeded by sprint 077.',
    true,
    'none',
    now(),
    now()
)
ON CONFLICT (name) DO NOTHING;
