-- Sprint 079 — seed cluster_tools rows for the platform-baseline operators.
--
-- Sprint 074 created the "Platform baseline" cluster_template referencing
-- four operator slugs (trivy-operator, kube-state-metrics,
-- prometheus-node-exporter, fluent-bit) plus cert-manager. cert-manager
-- already has a cluster_tools row (migration 033). The other four did
-- NOT have catalog entries, so the apply worker's first call —
-- GetToolBySlug — short-circuited every fresh cluster's baseline apply
-- with "no rows in result set" and the row sat at failed forever.
--
-- The helm repositories these charts live in were seeded in 075/077
-- (aqua, prometheus-community); this migration closes the loop by giving
-- the apply worker a slug→chart mapping for each baseline operator.
--
-- Idempotent: ON CONFLICT (slug) DO NOTHING. An operator who has already
-- hand-curated any of these slugs keeps their version.

INSERT INTO cluster_tools (
    slug, name, description, icon, category,
    charts, version_constraint, default_namespace,
    is_builtin, is_enabled,
    presets, service_name, service_path, sub_services
) VALUES (
    'trivy-operator',
    'Trivy Operator',
    'Continuous image vulnerability + misconfiguration scanning. Drives Astronomer''s Image Scans dashboard via the Trivy CRD ingester.',
    'shield',
    'security',
    '[{"chart_name":"trivy-operator","repo_url":"https://aquasecurity.github.io/helm-charts","namespace":"trivy-system","order":0}]'::jsonb,
    '',
    'trivy-system',
    true,
    true,
    '{"default":"trivy:\n  ignoreUnfixed: true\noperator:\n  scanJobTimeout: 5m\n"}'::jsonb,
    '',
    '',
    '[]'::jsonb
) ON CONFLICT (slug) DO NOTHING;

INSERT INTO cluster_tools (
    slug, name, description, icon, category,
    charts, version_constraint, default_namespace,
    is_builtin, is_enabled,
    presets, service_name, service_path, sub_services
) VALUES (
    'kube-state-metrics',
    'kube-state-metrics',
    'Cluster object metrics (Deployments, Pods, Nodes, …) exposed in Prometheus format. Backs Astronomer dashboards and SLO rules.',
    'activity',
    'observability',
    '[{"chart_name":"kube-state-metrics","repo_url":"https://prometheus-community.github.io/helm-charts","namespace":"monitoring","order":0}]'::jsonb,
    '',
    'monitoring',
    true,
    true,
    '{"default":"metricLabelsAllowlist:\n  - pods=[*]\n  - deployments=[*]\n"}'::jsonb,
    '',
    '',
    '[]'::jsonb
) ON CONFLICT (slug) DO NOTHING;

INSERT INTO cluster_tools (
    slug, name, description, icon, category,
    charts, version_constraint, default_namespace,
    is_builtin, is_enabled,
    presets, service_name, service_path, sub_services
) VALUES (
    'prometheus-node-exporter',
    'Prometheus Node Exporter',
    'Host-level metrics (cpu/mem/disk/net) exported by a DaemonSet on every node. Pairs with kube-state-metrics for full cluster observability.',
    'cpu',
    'observability',
    '[{"chart_name":"prometheus-node-exporter","repo_url":"https://prometheus-community.github.io/helm-charts","namespace":"monitoring","order":0}]'::jsonb,
    '',
    'monitoring',
    true,
    true,
    '{"default":"hostRootFsMount:\n  enabled: true\n"}'::jsonb,
    '',
    '',
    '[]'::jsonb
) ON CONFLICT (slug) DO NOTHING;

INSERT INTO cluster_tools (
    slug, name, description, icon, category,
    charts, version_constraint, default_namespace,
    is_builtin, is_enabled,
    presets, service_name, service_path, sub_services
) VALUES (
    'fluent-bit',
    'Fluent Bit',
    'Lightweight log forwarder. Tails container stdout/stderr and ships records to the Astronomer log sink (or any operator-configured backend).',
    'file-text',
    'observability',
    '[{"chart_name":"fluent-bit","repo_url":"https://fluent.github.io/helm-charts","namespace":"logging","order":0}]'::jsonb,
    '',
    'logging',
    true,
    true,
    '{"default":"config:\n  service: |\n    [SERVICE]\n        Daemon Off\n        Flush 1\n"}'::jsonb,
    '',
    '',
    '[]'::jsonb
) ON CONFLICT (slug) DO NOTHING;

-- Also seed the fluent helm repo so the chart resolves at install time.
-- bitnami / aqua / jetstack / prometheus-community were seeded by
-- migrations 075 + 077; fluent's repo is the last gap for baseline apply.
INSERT INTO helm_repositories (id, name, url, repo_type, description, enabled, auth_type, created_at, updated_at)
VALUES (
    gen_random_uuid(),
    'fluent',
    'https://fluent.github.io/helm-charts',
    'helm',
    'Fluent helm charts — fluent-bit log forwarder + fluentd. Seeded by sprint 079 so the platform-baseline apply can resolve fluent-bit.',
    true,
    'none',
    now(),
    now()
)
ON CONFLICT (name) DO NOTHING;
