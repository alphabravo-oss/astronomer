-- Match Rancher's dependency-aware application behavior for tools whose
-- upstream charts are not independently installable with Astronomer's prior
-- defaults.  Fluent Bit's old preset referenced a ConfigMap created only by
-- the separate logging workflow.  NeuVector is a Rancher two-release app: its
-- CRD chart must be installed first and both releases must use the curated
-- Rancher chart coordinates.
UPDATE public.cluster_tools
SET presets = jsonb_build_object(
        'default', 'hotReload:' || E'\n' || '  enabled: true' || E'\n'
    )
WHERE slug = 'fluent-bit';

UPDATE public.cluster_tools
SET default_namespace = 'cattle-neuvector-system',
    version_constraint = '110.0.2+up2.11.2',
    charts = jsonb_build_array(
        jsonb_build_object(
            'order', 0,
            'repo_url', 'https://charts.rancher.io',
            'namespace', 'cattle-neuvector-system',
            'chart_name', 'neuvector-crd',
            'release_name', 'neuvector-crd',
            'version', '110.0.2+up2.11.2'
        ),
        jsonb_build_object(
            'order', 1,
            'repo_url', 'https://charts.rancher.io',
            'namespace', 'cattle-neuvector-system',
            'chart_name', 'neuvector',
            'release_name', 'neuvector',
            'version', '110.0.2+up2.11.2'
        )
    )
WHERE slug = 'neuvector';
