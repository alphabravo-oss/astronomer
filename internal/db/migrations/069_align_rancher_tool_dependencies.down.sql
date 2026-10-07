UPDATE public.cluster_tools
SET presets = jsonb_build_object(
        'default', 'existingConfigMap: astronomer-fluent-bit-config' || E'\n' ||
                   'hotReload:' || E'\n' || '  enabled: true' || E'\n'
    )
WHERE slug = 'fluent-bit';

UPDATE public.cluster_tools
SET default_namespace = 'neuvector',
    version_constraint = '2.11.1',
    charts = jsonb_build_array(
        jsonb_build_object(
            'order', 0,
            'repo_url', 'https://neuvector.github.io/neuvector-helm',
            'namespace', 'neuvector',
            'chart_name', 'core',
            'version', '2.11.1'
        )
    )
WHERE slug = 'neuvector';
