-- Sprint 076 down — revert the slug back to "node-exporter" to match
-- sprint 074's original seed. Idempotent.

UPDATE cluster_templates
SET spec = jsonb_set(
    spec,
    '{tools}',
    (
        SELECT jsonb_agg(
            CASE
                WHEN tool->>'slug' = 'prometheus-node-exporter'
                    THEN jsonb_set(tool, '{slug}', '"node-exporter"')
                ELSE tool
            END
        )
        FROM jsonb_array_elements(spec->'tools') AS tool
    )
),
updated_at = now()
WHERE name = 'Platform baseline'
  AND spec ? 'tools'
  AND spec->'tools' @> '[{"slug": "prometheus-node-exporter"}]'::jsonb;
