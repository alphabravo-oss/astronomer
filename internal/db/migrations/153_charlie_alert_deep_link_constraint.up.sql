-- PostgreSQL uses standard-conforming strings, so one backslash is required
-- for the regular-expression engine to treat the question mark literally.
-- Migration 152 originally supplied two, which made every valid finding deep
-- link fail the CHECK and prevented the delivery/outbox transaction.
ALTER TABLE charlie_alert_deliveries
    DROP CONSTRAINT charlie_alert_delivery_deep_link;

ALTER TABLE charlie_alert_deliveries
    ADD CONSTRAINT charlie_alert_delivery_deep_link
    CHECK (deep_link ~ '^/dashboard/charlie\?tab=findings&finding=[0-9a-f-]{36}$');
