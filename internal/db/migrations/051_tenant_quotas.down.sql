-- Reverse of 051_tenant_quotas.up.sql. Drop the FK + columns on projects /
-- users first so the quota_plans table can be removed without a constraint
-- failure. Indexes are dropped explicitly for symmetry; CASCADE on the
-- table drop would handle them, but being explicit makes the rollback
-- path easier to read.

DROP INDEX IF EXISTS idx_users_quota_plan;
DROP INDEX IF EXISTS idx_projects_quota_plan;

ALTER TABLE users    DROP COLUMN IF EXISTS quota_overrides;
ALTER TABLE users    DROP COLUMN IF EXISTS quota_plan;
ALTER TABLE projects DROP COLUMN IF EXISTS quota_overrides;
ALTER TABLE projects DROP COLUMN IF EXISTS quota_plan;

DROP TABLE IF EXISTS quota_plans;
