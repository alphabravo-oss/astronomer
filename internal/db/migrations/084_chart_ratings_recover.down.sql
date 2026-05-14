-- No-op down. The chart_rating tables are owned by migration 073;
-- if you want to remove them, run 073's down. This recovery migration
-- only ever creates rows that 073 forgot to leave behind, so there's
-- nothing of its own to remove.
SELECT 1;
