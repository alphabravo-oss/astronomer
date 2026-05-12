-- Down migration for 064_compliance_baselines.
--
-- Order matters: applications FK → baselines, so applications has to
-- go first. The application rows are historical audit data; losing
-- them on a downgrade is acceptable (the original audit_log row
-- recording the apply remains, which is the auditor-relevant trail).

DROP INDEX IF EXISTS idx_compliance_apps_active;
DROP INDEX IF EXISTS idx_compliance_apps_baseline;
DROP TABLE IF EXISTS compliance_baseline_applications;
DROP TABLE IF EXISTS compliance_baselines;
