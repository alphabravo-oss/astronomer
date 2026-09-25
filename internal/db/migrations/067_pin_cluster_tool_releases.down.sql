-- Restore the exact pre-067 seed shape.  Removing the chart-local version is
-- safe because no operation payload is rewritten by this rollback.
UPDATE public.cluster_tools
SET charts = charts #- '{0,version}',
    version_constraint = CASE slug
        WHEN 'kube-state-metrics' THEN '8.0.0'
        WHEN 'prometheus-node-exporter' THEN '4.56.1'
        WHEN 'longhorn' THEN '1.12.1'
        WHEN 'neuvector' THEN '2.11.1'
        ELSE ''
    END
WHERE slug IN (
    'cis-operator', 'fluent-bit', 'trivy-operator', 'cert-manager',
    'gatekeeper', 'kube-state-metrics', 'prometheus-node-exporter',
    'ingress-nginx', 'longhorn', 'neuvector'
);
