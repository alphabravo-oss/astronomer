ALTER TABLE installed_charts
    DROP COLUMN IF EXISTS drift_detected,
    DROP COLUMN IF EXISTS drift_detail,
    DROP COLUMN IF EXISTS drift_checked_at;
