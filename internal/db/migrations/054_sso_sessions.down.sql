-- Rollback for 054_sso_sessions.up.sql.

DROP INDEX IF EXISTS idx_sso_sessions_expires;
DROP INDEX IF EXISTS idx_sso_sessions_user;
DROP TABLE IF EXISTS sso_sessions;
