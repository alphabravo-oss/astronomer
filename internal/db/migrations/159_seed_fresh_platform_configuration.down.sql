-- Intentionally irreversible. The up migration may create the platform
-- singleton, which is subsequently populated with operator settings and an
-- installation identity. Deleting or clearing that row during rollback would
-- destroy runtime configuration, and there is no durable marker that can
-- distinguish the seeded row from an operator-managed one.
SELECT 1;
