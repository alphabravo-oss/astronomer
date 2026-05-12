-- Rollback 040: drop policy columns and the enum check constraint.

ALTER TABLE projects
    DROP CONSTRAINT IF EXISTS pod_security_profile_valid;

ALTER TABLE projects
    DROP COLUMN IF EXISTS pod_security_profile,
    DROP COLUMN IF EXISTS resource_quota_cpu_limit,
    DROP COLUMN IF EXISTS resource_quota_memory_limit,
    DROP COLUMN IF EXISTS resource_quota_pod_count;
