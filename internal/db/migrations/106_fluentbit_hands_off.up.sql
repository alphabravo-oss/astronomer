-- 106: finish the hands-off Fluent Bit logging data plane.
--
-- The logging controller writes the aggregate config into a single ConfigMap
-- (astronomer-fluent-bit-config) in the astronomer-logging namespace. For an
-- operator's one-click "install fluent-bit" to actually consume that config,
-- the catalog tool must (a) install INTO astronomer-logging (co-located with
-- the ConfigMap, which is namespace-scoped) and (b) point Fluent Bit at it via
-- the chart's existingConfigMap + enable hot reload so new outputs/pipelines
-- take effect without a pod restart.
--
-- Distribution-specific volume/SCC overrides (k3s/k3d machine-id, OpenShift)
-- are injected automatically by the install path, so they are NOT baked here.
UPDATE cluster_tools
SET charts = '[{"chart_name":"fluent-bit","repo_url":"https://fluent.github.io/helm-charts","namespace":"astronomer-logging","order":0}]'::jsonb,
    default_namespace = 'astronomer-logging',
    presets = '{"default":"existingConfigMap: astronomer-fluent-bit-config\nhotReload:\n  enabled: true\n"}'::jsonb,
    updated_at = now()
WHERE slug = 'fluent-bit';
