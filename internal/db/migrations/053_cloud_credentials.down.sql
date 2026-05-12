-- Down-migration for 053_cloud_credentials.
--
-- Drops the materializations table FIRST so the FK to cloud_credentials
-- doesn't dangle when the parent table goes; the indexes go with their
-- table per DROP TABLE semantics.

DROP TABLE IF EXISTS cloud_credential_materializations;
DROP TABLE IF EXISTS cloud_credentials;
