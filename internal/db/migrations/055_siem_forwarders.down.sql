-- Rollback for 055_siem_forwarders.up.sql.
--
-- Order matches the up file's reverse: indexes first, then status +
-- queue tables, then the forwarders table itself. DROP TABLE drops
-- dependent indexes implicitly, but the explicit form keeps the
-- rollback symmetric with the up migration so a future schema diff
-- doesn't yell.

DROP INDEX IF EXISTS idx_siem_forward_queue_forwarder;
DROP TABLE IF EXISTS siem_forwarder_status;
DROP TABLE IF EXISTS siem_forward_queue;
DROP TABLE IF EXISTS siem_forwarders;
