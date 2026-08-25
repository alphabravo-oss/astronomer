\set ON_ERROR_STOP on

INSERT INTO clusters (
  id, name, display_name, status, environment, provider, managed_by
) VALUES (
  :'cluster_id'::uuid,
  'upgrade-fixture-' || :'fixture_id',
  'Upgrade fixture ' || :'fixture_id',
  'active',
  'production',
  'other',
  'api'
);

INSERT INTO platform_settings (key, value, description)
VALUES (
  'upgrade.fixture',
  jsonb_build_object('id', :'fixture_id', 'source_schema', :'schema_version'::integer),
  'Upgrade-matrix preservation sentinel'
)
ON CONFLICT (key) DO UPDATE
SET value = EXCLUDED.value, description = EXCLUDED.description;
