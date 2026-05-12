-- Reverse of 047_smtp_email.up.sql. Order matters only for cosmetic
-- reasons (matching CREATE order in reverse); none of these tables
-- depend on each other.

DROP TABLE IF EXISTS password_reset_tokens;
DROP TABLE IF EXISTS email_messages;
DROP TABLE IF EXISTS smtp_settings;
