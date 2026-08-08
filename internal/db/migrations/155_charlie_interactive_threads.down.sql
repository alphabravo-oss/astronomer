DROP INDEX IF EXISTS charlie_sessions_thread_idx;
ALTER TABLE charlie_sessions DROP COLUMN IF EXISTS thread_id;
DROP TABLE IF EXISTS charlie_thread_sessions;
DROP TABLE IF EXISTS charlie_interactive_threads;
