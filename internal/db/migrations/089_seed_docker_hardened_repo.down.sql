-- Down migration for 089.
--
-- Only remove the row when no installed_charts reference its versions.
-- The ON DELETE CASCADE on helm_charts → helm_chart_versions is fine
-- (sprint 083 documents the SET NULL on installed_charts.chart_version_id
-- so releases survive intact), but an operator who has charts running
-- from this repo is better served by an explicit no-op than a silent
-- catalog wipe. Delete unconditionally though, matching the symmetry
-- with 083's bitnami removal — the cascade behaviour is already
-- documented there.

DELETE FROM helm_repositories WHERE name = 'docker-hardened-images';
