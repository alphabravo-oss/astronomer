-- Reverse of 048_webhook_subscriptions.up.sql. webhook_deliveries
-- references webhook_subscriptions via ON DELETE CASCADE so the order
-- below is "child first" for symmetry with the up migration's CREATE
-- order in reverse, even though Postgres would happily drop them
-- either way thanks to the cascade.

DROP TABLE IF EXISTS webhook_deliveries;
DROP TABLE IF EXISTS webhook_subscriptions;
