DELETE FROM audit_log
WHERE actor_auth_method = 'legacy_backfill';
