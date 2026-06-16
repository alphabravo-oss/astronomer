-- Revert the fluent-bit catalog tool to its pre-106 namespace + preset.
UPDATE cluster_tools
SET charts = '[{"chart_name":"fluent-bit","repo_url":"https://fluent.github.io/helm-charts","namespace":"logging","order":0}]'::jsonb,
    default_namespace = 'logging',
    presets = '{"default":"config:\n  service: |\n    [SERVICE]\n        Daemon Off\n        Flush 1\n"}'::jsonb,
    updated_at = now()
WHERE slug = 'fluent-bit';
