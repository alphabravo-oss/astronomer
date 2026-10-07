UPDATE public.cluster_tools
SET charts = jsonb_build_array(
        jsonb_build_object(
            'order', 0,
            'repo_url', 'https://charts.rancher.io',
            'namespace', 'cis-operator-system',
            'chart_name', 'rancher-cis-benchmark',
            'version', '106.8.0+up8.10.0'
        )
    )
WHERE slug = 'cis-operator';
