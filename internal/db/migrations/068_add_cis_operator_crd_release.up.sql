-- Rancher's CIS operator chart refuses to install until its separately
-- packaged CRDs are present.  Model both Helm releases in dependency order so
-- the normal Tool API can install, upgrade, roll back, and uninstall the
-- complete product without out-of-band kubectl or Helm steps.
UPDATE public.cluster_tools
SET charts = jsonb_build_array(
        jsonb_build_object(
            'order', 0,
            'repo_url', 'https://charts.rancher.io',
            'namespace', 'cis-operator-system',
            'chart_name', 'rancher-cis-benchmark-crd',
            'release_name', 'cis-operator-crd',
            'version', '106.8.0+up8.10.0'
        ),
        jsonb_build_object(
            'order', 1,
            'repo_url', 'https://charts.rancher.io',
            'namespace', 'cis-operator-system',
            'chart_name', 'rancher-cis-benchmark',
            'release_name', 'cis-operator',
            'version', '106.8.0+up8.10.0'
        )
    )
WHERE slug = 'cis-operator';
