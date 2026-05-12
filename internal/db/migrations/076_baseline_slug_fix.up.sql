-- Sprint 076 — fix the platform-baseline tool slug for node-exporter.
--
-- Sprint 074 seeded the Platform baseline cluster_template with five tool
-- slugs, including "node-exporter". Sprint 075 then seeded the Bitnami
-- helm_repository, which publishes the chart as "prometheus-node-exporter"
-- — so the apply reconciler's slug lookup misses on that one tool.
--
-- This migration rewrites the JSONB in place to use the actual upstream
-- chart name. Idempotent: re-running on a row that already has
-- "prometheus-node-exporter" is a no-op.
--
-- Operators who cloned the baseline before this fix will still carry the
-- broken slug in their custom template. They can apply this same UPDATE
-- to their cloned row, or just re-add the chart via the cluster_templates
-- UI with the correct name. We don't touch operator-owned clones from a
-- migration.

UPDATE cluster_templates
SET spec = jsonb_set(
    spec,
    '{tools}',
    (
        SELECT jsonb_agg(
            CASE
                WHEN tool->>'slug' = 'node-exporter'
                    THEN jsonb_set(tool, '{slug}', '"prometheus-node-exporter"')
                ELSE tool
            END
        )
        FROM jsonb_array_elements(spec->'tools') AS tool
    )
),
updated_at = now()
WHERE name = 'Platform baseline'
  AND spec ? 'tools'
  AND spec->'tools' @> '[{"slug": "node-exporter"}]'::jsonb;
