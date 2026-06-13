ALTER TABLE cluster_registry_configs
    ADD COLUMN IF NOT EXISTS registry_password_encrypted TEXT NOT NULL DEFAULT '';
