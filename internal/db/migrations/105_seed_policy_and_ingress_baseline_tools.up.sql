-- Sprint 105 - keep the platform-baseline catalog in sync with the Argo baseline.
--
-- Earlier Argo baseline work added ingress-nginx as an Argo-managed component
-- and this sprint adds Gatekeeper as the policy-stack engine. The ApplicationSet
-- fallbacks can deploy both without catalog rows, but the platform baseline
-- coverage endpoint and manual tool install paths should resolve them from the
-- first-boot catalog sync as well.

INSERT INTO helm_repositories (id, name, url, repo_type, description, enabled, auth_type, created_at, updated_at)
VALUES (
    gen_random_uuid(),
    'ingress-nginx',
    'https://kubernetes.github.io/ingress-nginx',
    'helm',
    'ingress-nginx Helm charts. Seeded so the platform baseline and manual tool install surfaces resolve ingress-nginx consistently.',
    true,
    'none',
    now(),
    now()
)
ON CONFLICT (name) DO NOTHING;

INSERT INTO helm_repositories (id, name, url, repo_type, description, enabled, auth_type, created_at, updated_at)
VALUES (
    gen_random_uuid(),
    'open-policy-agent',
    'https://open-policy-agent.github.io/gatekeeper/charts',
    'helm',
    'Open Policy Agent Gatekeeper Helm charts. Seeded for the platform baseline policy-stack component.',
    true,
    'none',
    now(),
    now()
)
ON CONFLICT (name) DO NOTHING;

INSERT INTO cluster_tools (
    slug, name, description, icon, category,
    charts, version_constraint, default_namespace,
    is_builtin, is_enabled,
    presets, service_name, service_path, sub_services
) VALUES (
    'ingress-nginx',
    'ingress-nginx',
    'Ingress controller for adopted clusters. The Argo-managed platform baseline installs it with metrics enabled.',
    'route',
    'networking',
    '[{"chart_name":"ingress-nginx","repo_url":"https://kubernetes.github.io/ingress-nginx","namespace":"ingress-nginx","order":0}]'::jsonb,
    '',
    'ingress-nginx',
    true,
    true,
    '{"default":"controller:\n  metrics:\n    enabled: true\n"}'::jsonb,
    '',
    '',
    '[]'::jsonb
)
ON CONFLICT (slug) DO NOTHING;

INSERT INTO cluster_tools (
    slug, name, description, icon, category,
    charts, version_constraint, default_namespace,
    is_builtin, is_enabled,
    presets, service_name, service_path, sub_services
) VALUES (
    'gatekeeper',
    'Gatekeeper',
    'Open Policy Agent Gatekeeper admission policy engine for baseline policy enforcement and future constraint bundles.',
    'shield-check',
    'security',
    '[{"chart_name":"gatekeeper","repo_url":"https://open-policy-agent.github.io/gatekeeper/charts","namespace":"gatekeeper-system","order":0}]'::jsonb,
    '',
    'gatekeeper-system',
    true,
    true,
    '{"default":""}'::jsonb,
    '',
    '',
    '[]'::jsonb
)
ON CONFLICT (slug) DO NOTHING;
