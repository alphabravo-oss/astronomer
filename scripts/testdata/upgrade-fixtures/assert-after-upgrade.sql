\set ON_ERROR_STOP on

SELECT 1 / CASE WHEN EXISTS (
  SELECT 1
  FROM platform_settings
  WHERE key = 'upgrade.fixture'
    AND value ->> 'id' = :'fixture_id'
    AND (value ->> 'source_schema')::integer = :'schema_version'::integer
) THEN 1 ELSE 0 END AS fixture_setting_preserved;

SELECT 1 / CASE WHEN EXISTS (
  SELECT 1
  FROM clusters
  WHERE id = :'cluster_id'::uuid
    AND name = 'upgrade-fixture-' || :'fixture_id'
    AND decommissioned_at IS NULL
) THEN 1 ELSE 0 END AS fixture_cluster_preserved;
