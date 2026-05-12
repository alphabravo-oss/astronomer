-- Reverse of 056_fleet_operations.up.sql. Drop order matters: the
-- targets table FKs into fleet_operations, so it must go first.

DROP INDEX IF EXISTS idx_fleet_operation_targets_op;
DROP TABLE IF EXISTS fleet_operation_targets;
DROP INDEX IF EXISTS idx_fleet_operations_status;
DROP TABLE IF EXISTS fleet_operations;
