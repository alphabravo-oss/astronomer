-- Rollback for migration 065.
DROP INDEX IF EXISTS idx_kubectl_session_commands_session;
DROP TABLE IF EXISTS kubectl_session_commands;

DROP INDEX IF EXISTS idx_kubectl_sessions_reap;
DROP INDEX IF EXISTS idx_kubectl_sessions_active;
DROP INDEX IF EXISTS idx_kubectl_sessions_user;
DROP TABLE IF EXISTS kubectl_sessions;
