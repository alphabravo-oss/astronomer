-- Revert the baseline component namespace relocation: astronomer-* -> original.
-- Idempotent, keyed on the new value so re-running is a no-op once reverted.

-- cluster_tools.default_namespace
UPDATE cluster_tools SET default_namespace = 'trivy-system'
WHERE slug = 'trivy-operator' AND default_namespace = 'astronomer-trivy-system';

UPDATE cluster_tools SET default_namespace = 'monitoring'
WHERE slug IN ('kube-state-metrics', 'prometheus-node-exporter') AND default_namespace = 'astronomer-monitoring';

UPDATE cluster_tools SET default_namespace = 'logging'
WHERE slug = 'fluent-bit' AND default_namespace = 'astronomer-logging';

UPDATE cluster_tools SET default_namespace = 'ingress-nginx'
WHERE slug = 'ingress-nginx' AND default_namespace = 'astronomer-ingress-nginx';

UPDATE cluster_tools SET default_namespace = 'cert-manager'
WHERE slug = 'cert-manager' AND default_namespace = 'astronomer-cert-manager';

UPDATE cluster_tools SET default_namespace = 'gatekeeper-system'
WHERE slug = 'gatekeeper' AND default_namespace = 'astronomer-gatekeeper-system';

-- cluster_tools.charts JSONB
UPDATE cluster_tools
SET charts = jsonb_set(charts, '{0,namespace}', '"trivy-system"', false)
WHERE slug = 'trivy-operator' AND charts #>> '{0,namespace}' = 'astronomer-trivy-system';

UPDATE cluster_tools
SET charts = jsonb_set(charts, '{0,namespace}', '"monitoring"', false)
WHERE slug IN ('kube-state-metrics', 'prometheus-node-exporter') AND charts #>> '{0,namespace}' = 'astronomer-monitoring';

UPDATE cluster_tools
SET charts = jsonb_set(charts, '{0,namespace}', '"logging"', false)
WHERE slug = 'fluent-bit' AND charts #>> '{0,namespace}' = 'astronomer-logging';

UPDATE cluster_tools
SET charts = jsonb_set(charts, '{0,namespace}', '"ingress-nginx"', false)
WHERE slug = 'ingress-nginx' AND charts #>> '{0,namespace}' = 'astronomer-ingress-nginx';

UPDATE cluster_tools
SET charts = jsonb_set(charts, '{0,namespace}', '"cert-manager"', false)
WHERE slug = 'cert-manager' AND charts #>> '{0,namespace}' = 'astronomer-cert-manager';

UPDATE cluster_tools
SET charts = jsonb_set(charts, '{0,namespace}', '"gatekeeper-system"', false)
WHERE slug = 'gatekeeper' AND charts #>> '{0,namespace}' = 'astronomer-gatekeeper-system';

-- cluster_templates 'Platform baseline' spec tools[]
UPDATE cluster_templates ct
SET spec = jsonb_set(
    ct.spec,
    '{tools}',
    (
        SELECT jsonb_agg(
            CASE
                WHEN tool->>'namespace' = 'astronomer-trivy-system'      THEN jsonb_set(tool, '{namespace}', '"trivy-system"')
                WHEN tool->>'namespace' = 'astronomer-monitoring'        THEN jsonb_set(tool, '{namespace}', '"monitoring"')
                WHEN tool->>'namespace' = 'astronomer-logging'           THEN jsonb_set(tool, '{namespace}', '"logging"')
                WHEN tool->>'namespace' = 'astronomer-ingress-nginx'     THEN jsonb_set(tool, '{namespace}', '"ingress-nginx"')
                WHEN tool->>'namespace' = 'astronomer-cert-manager'      THEN jsonb_set(tool, '{namespace}', '"cert-manager"')
                WHEN tool->>'namespace' = 'astronomer-gatekeeper-system' THEN jsonb_set(tool, '{namespace}', '"gatekeeper-system"')
                ELSE tool
            END
        )
        FROM jsonb_array_elements(ct.spec->'tools') AS tool
    )
)
WHERE ct.name = 'Platform baseline'
  AND ct.spec ? 'tools'
  AND jsonb_typeof(ct.spec->'tools') = 'array'
  AND EXISTS (
      SELECT 1 FROM jsonb_array_elements(ct.spec->'tools') AS t
      WHERE t->>'namespace' IN (
          'astronomer-trivy-system', 'astronomer-monitoring', 'astronomer-logging',
          'astronomer-ingress-nginx', 'astronomer-cert-manager', 'astronomer-gatekeeper-system'
      )
  );
