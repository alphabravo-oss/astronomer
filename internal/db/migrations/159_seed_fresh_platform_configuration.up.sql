-- Migration 074 attempted to select the built-in Platform baseline as the
-- default, but a genuinely fresh database did not yet contain the singleton
-- platform_configuration row. That row was historically created later by the
-- server bootstrap path, after migrations had finished, so the baseline
-- UPDATE affected zero rows and the registration wizard's baseline opt-in was
-- silently skipped.
--
-- Seed only the missing singleton. ON CONFLICT DO NOTHING deliberately
-- preserves every existing deployment, including an operator's explicit NULL
-- (baseline disabled) or custom default-template selection.
INSERT INTO platform_configuration (id, default_cluster_template_id)
SELECT 1, id
FROM cluster_templates
WHERE name = 'Platform baseline'
ON CONFLICT (id) DO NOTHING;
