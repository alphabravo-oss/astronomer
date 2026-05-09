DROP TABLE IF EXISTS control_plane_silences CASCADE;
ALTER TABLE control_plane_alerts
    DROP COLUMN IF EXISTS acknowledged_at,
    DROP COLUMN IF EXISTS acknowledged_by_id;
