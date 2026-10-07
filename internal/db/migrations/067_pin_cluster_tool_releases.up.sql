-- Tool actions must resolve to immutable release plans before they are queued.
-- Keep these versions aligned with the curated application catalog where the
-- same product is exposed through both Apps and Tools.  Dex is intentionally
-- excluded: it is managed by the management-plane Auth workflow and may not be
-- installed into an adopted cluster.
WITH pinned(slug, version) AS (
    VALUES
        ('cis-operator', '106.8.0+up8.10.0'),
        ('fluent-bit', '0.58.1'),
        ('trivy-operator', '0.36.0'),
        ('cert-manager', 'v1.21.1'),
        ('gatekeeper', '3.23.0'),
        ('kube-state-metrics', '8.4.0'),
        ('prometheus-node-exporter', '4.56.1'),
        ('ingress-nginx', '4.15.1'),
        ('longhorn', '1.12.1'),
        ('neuvector', '2.11.1')
)
UPDATE public.cluster_tools AS tool
SET version_constraint = pinned.version,
    charts = jsonb_set(tool.charts, '{0,version}', to_jsonb(pinned.version), true)
FROM pinned
WHERE tool.slug = pinned.slug;
