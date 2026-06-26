-- Sprint 119 — relocate baseline component namespaces under the astronomer-*
-- prefix so every Astronomer-owned namespace is self-management-RBAC bounded.
--
-- Source of truth is internal/baseline/registry.go. The namespaces are also
-- stored in the database in three places, all seeded by earlier (already-applied,
-- never-edited) migrations:
--
--   * cluster_tools.default_namespace + cluster_tools.charts JSONB
--       - cert-manager           (033)  cert-manager        -> astronomer-cert-manager
--       - trivy-operator         (079)  trivy-system        -> astronomer-trivy-system
--       - kube-state-metrics     (079)  monitoring          -> astronomer-monitoring
--       - prometheus-node-exporter (079) monitoring         -> astronomer-monitoring
--       - fluent-bit             (079)  logging             -> astronomer-logging
--                                  (already relocated by 106; re-asserted here idempotently)
--       - ingress-nginx          (105)  ingress-nginx       -> astronomer-ingress-nginx
--       - gatekeeper             (105)  gatekeeper-system   -> astronomer-gatekeeper-system
--   * cluster_templates.spec ('Platform baseline', seeded by 074) tools[].namespace
--
-- Idempotent + convergent: fresh installs run the old seed migrations then this
-- UPDATE; existing installs run this UPDATE alone. Every statement is keyed on
-- the OLD namespace value so re-running it is a no-op once converged. We do NOT
-- edit migrations 033/074/079/105 — this is a forward-only correction.

-- ---------------------------------------------------------------------------
-- cluster_tools: default_namespace column
-- ---------------------------------------------------------------------------
UPDATE cluster_tools SET default_namespace = 'astronomer-trivy-system'
WHERE slug = 'trivy-operator' AND default_namespace = 'trivy-system';

UPDATE cluster_tools SET default_namespace = 'astronomer-monitoring'
WHERE slug IN ('kube-state-metrics', 'prometheus-node-exporter') AND default_namespace = 'monitoring';

UPDATE cluster_tools SET default_namespace = 'astronomer-logging'
WHERE slug = 'fluent-bit' AND default_namespace = 'logging';

UPDATE cluster_tools SET default_namespace = 'astronomer-ingress-nginx'
WHERE slug = 'ingress-nginx' AND default_namespace = 'ingress-nginx';

UPDATE cluster_tools SET default_namespace = 'astronomer-cert-manager'
WHERE slug = 'cert-manager' AND default_namespace = 'cert-manager';

UPDATE cluster_tools SET default_namespace = 'astronomer-gatekeeper-system'
WHERE slug = 'gatekeeper' AND default_namespace = 'gatekeeper-system';

-- ---------------------------------------------------------------------------
-- cluster_tools: charts JSONB (the per-chart "namespace" embedded in element 0).
-- jsonb_set targets {0,namespace}; guarded on the OLD value so it is a no-op
-- once converged and never clobbers an operator's hand-edited value.
-- ---------------------------------------------------------------------------
UPDATE cluster_tools
SET charts = jsonb_set(charts, '{0,namespace}', '"astronomer-trivy-system"', false)
WHERE slug = 'trivy-operator' AND charts #>> '{0,namespace}' = 'trivy-system';

UPDATE cluster_tools
SET charts = jsonb_set(charts, '{0,namespace}', '"astronomer-monitoring"', false)
WHERE slug IN ('kube-state-metrics', 'prometheus-node-exporter') AND charts #>> '{0,namespace}' = 'monitoring';

UPDATE cluster_tools
SET charts = jsonb_set(charts, '{0,namespace}', '"astronomer-logging"', false)
WHERE slug = 'fluent-bit' AND charts #>> '{0,namespace}' = 'logging';

UPDATE cluster_tools
SET charts = jsonb_set(charts, '{0,namespace}', '"astronomer-ingress-nginx"', false)
WHERE slug = 'ingress-nginx' AND charts #>> '{0,namespace}' = 'ingress-nginx';

UPDATE cluster_tools
SET charts = jsonb_set(charts, '{0,namespace}', '"astronomer-cert-manager"', false)
WHERE slug = 'cert-manager' AND charts #>> '{0,namespace}' = 'cert-manager';

UPDATE cluster_tools
SET charts = jsonb_set(charts, '{0,namespace}', '"astronomer-gatekeeper-system"', false)
WHERE slug = 'gatekeeper' AND charts #>> '{0,namespace}' = 'gatekeeper-system';

-- ---------------------------------------------------------------------------
-- cluster_templates: the builtin 'Platform baseline' spec embeds a per-tool
-- namespace in spec->tools[]. Rewrite each element's namespace by old value.
-- ---------------------------------------------------------------------------
UPDATE cluster_templates ct
SET spec = jsonb_set(
    ct.spec,
    '{tools}',
    (
        SELECT jsonb_agg(
            CASE
                WHEN tool->>'namespace' = 'trivy-system'     THEN jsonb_set(tool, '{namespace}', '"astronomer-trivy-system"')
                WHEN tool->>'namespace' = 'monitoring'        THEN jsonb_set(tool, '{namespace}', '"astronomer-monitoring"')
                WHEN tool->>'namespace' = 'logging'           THEN jsonb_set(tool, '{namespace}', '"astronomer-logging"')
                WHEN tool->>'namespace' = 'ingress-nginx'     THEN jsonb_set(tool, '{namespace}', '"astronomer-ingress-nginx"')
                WHEN tool->>'namespace' = 'cert-manager'      THEN jsonb_set(tool, '{namespace}', '"astronomer-cert-manager"')
                WHEN tool->>'namespace' = 'gatekeeper-system' THEN jsonb_set(tool, '{namespace}', '"astronomer-gatekeeper-system"')
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
          'trivy-system', 'monitoring', 'logging',
          'ingress-nginx', 'cert-manager', 'gatekeeper-system'
      )
  );
