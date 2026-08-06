ALTER TABLE charlie_alert_deliveries
    DROP CONSTRAINT charlie_alert_delivery_deep_link;

ALTER TABLE charlie_alert_deliveries
    ADD CONSTRAINT charlie_alert_delivery_deep_link
    CHECK (deep_link ~ '^/dashboard/charlie\\?tab=findings&finding=[0-9a-f-]{36}$');
