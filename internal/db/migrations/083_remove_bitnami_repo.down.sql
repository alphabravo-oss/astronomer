-- Restore the Bitnami repo row as migration 075 originally seeded it.
-- The cascaded helm_charts + helm_chart_versions are NOT restored —
-- they'll be re-populated on the next catalog reconciler tick (or
-- manual sync) against the now-stale upstream index.

INSERT INTO helm_repositories (id, name, url, repo_type, description, enabled, auth_type, created_at, updated_at)
VALUES
    (gen_random_uuid(), 'bitnami', 'https://charts.bitnami.com/bitnami',
     'helm', 'Bitnami chart repository — kube-state-metrics, node-exporter, fluent-bit, others. Seeded by sprint 075.',
     true, 'none', now(), now())
ON CONFLICT (name) DO NOTHING;
