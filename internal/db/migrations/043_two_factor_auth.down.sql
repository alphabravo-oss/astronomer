-- Rollback for 043_two_factor_auth.up.sql.

DROP INDEX IF EXISTS uidx_totp_recovery_hash;
DROP INDEX IF EXISTS idx_totp_recovery_user;
DROP TABLE IF EXISTS user_totp_recovery_codes;
DROP TABLE IF EXISTS user_totp_enrollments;
