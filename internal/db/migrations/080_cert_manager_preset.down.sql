-- Reverse sprint 080: restore the cert-manager default preset to the
-- migration-033 value (no explicit startupapicheck key, which inherits
-- the chart default of enabled=true).

UPDATE cluster_tools
SET presets = jsonb_set(
    presets,
    '{default}',
    to_jsonb($preset$crds:
  enabled: true
prometheus:
  enabled: true
$preset$::text)
)
WHERE slug = 'cert-manager';
