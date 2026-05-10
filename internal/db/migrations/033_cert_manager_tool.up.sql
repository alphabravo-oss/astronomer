-- Supportability / production TLS posture:
--
-- Seed cert-manager into the built-in cluster_tools catalog so operators can
-- install it from Astronomer's tools UI before wiring TLS automation onto the
-- management Gateway or workload-cluster ingresses.
INSERT INTO cluster_tools (
    slug, name, description, icon, category,
    charts, version_constraint, default_namespace,
    is_builtin, is_enabled,
    presets, service_name, service_path, sub_services
) VALUES (
    'cert-manager',
    'cert-manager',
    'Automated certificate management for Kubernetes. Use it to issue and renew TLS certificates for Astronomer''s Gateway and other cluster workloads.',
    'lock',
    'security',
    '[{"chart_name":"cert-manager","repo_url":"https://charts.jetstack.io","namespace":"cert-manager","order":0}]'::jsonb,
    '',
    'cert-manager',
    true,
    true,
    '{"default":"crds:\n  enabled: true\nprometheus:\n  enabled: true\n"}'::jsonb,
    'cert-manager-webhook',
    '/',
    '[]'::jsonb
)
ON CONFLICT (slug) DO NOTHING;
