-- The backfilled rows are indistinguishable from later immutable lifecycle
-- records and deleting them would destroy legitimate history. Schema rollback
-- is handled by migration 027; this data-only migration is intentionally a no-op.
SELECT 1;
