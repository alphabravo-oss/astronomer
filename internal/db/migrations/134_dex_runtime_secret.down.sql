-- Forward-fix rollback stub. Dropping the envelope after a completed cutover
-- would destroy the only credential copy. The additive schema is understood by
-- the previous binary, so downgrade the application while retaining columns.
-- A later contract migration may remove compatibility state after the supported
-- rollback window has closed.
SELECT 1;
