-- 040: per-project Pod Security Standards + ResourceQuota policy.
--
-- Adds three policy levers to the projects row so the reconciler in
-- internal/worker/tasks/project_reconcile.go can stamp PSS labels and apply
-- a ResourceQuota named "astronomer-project-quota" in every namespace that
-- belongs to the project, across every cluster the project spans.
--
-- Default choice on pod_security_profile = 'privileged'
-- -----------------------------------------------------
-- The 'privileged' profile is a no-op: it does not restrict pods. We pick it
-- as the DEFAULT for *existing* rows so this migration cannot disrupt running
-- workloads. New projects created through the handler explicitly default to
-- 'baseline' (see internal/handler/projects.go), so the desired posture for
-- net-new projects is still the recommended PSS baseline; this default only
-- affects rows that pre-date the migration.
--
-- See: https://kubernetes.io/docs/concepts/security/pod-security-standards/

ALTER TABLE projects
    ADD COLUMN pod_security_profile        VARCHAR(16) NOT NULL DEFAULT 'privileged',
    ADD COLUMN resource_quota_cpu_limit    VARCHAR(64) NOT NULL DEFAULT '',
    ADD COLUMN resource_quota_memory_limit VARCHAR(64) NOT NULL DEFAULT '',
    ADD COLUMN resource_quota_pod_count    INTEGER     NOT NULL DEFAULT 0;

ALTER TABLE projects
    ADD CONSTRAINT pod_security_profile_valid
        CHECK (pod_security_profile IN ('privileged', 'baseline', 'restricted'));
