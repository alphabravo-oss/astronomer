-- +goose Up
-- +goose StatementBegin
-- migration-phase: contract
-- Phase 1 item 3 (ADR-0010): age column encryption with an explicit
-- (key_id, algorithm, ciphertext, nonce) row shape replaces the Fernet
-- envelope strings ("astronomer:v1:fernet:<id>:<token>") that every secret
-- column stored in a single <col>_encrypted TEXT column.
--
-- Hard cutover, greenfield: existing Fernet ciphertexts are DISCARDED, not
-- converted (no Fernet code remains to read them). Development installs must
-- re-enter their secrets (SSO client secrets, SMTP password, registry and
-- repository credentials, cloud credentials, webhook/SIEM/Vault auth, ...)
-- after this migration. Rows that are useless without their sealed input are
-- deleted below; everything else keeps its row with an empty sealed value.
--
-- Every encrypted column family <c> is four columns:
--   <c>_key_id     text  NOT NULL DEFAULT ''  -- keyring id that sealed it
--   <c>_algorithm  text  NOT NULL DEFAULT ''  -- 'age-x25519-chacha20poly1305'
--   <c>_ciphertext bytea                      -- NULL when no secret is stored
--   <c>_nonce      bytea                      -- NULL for age (its nonce is
--                                                inside the ciphertext);
--                                                reserved for AEAD algorithms
--                                                that need an external nonce
-- and a CHECK that key_id, algorithm and ciphertext are all set or all empty.
-- Rotation (astro admin rotate-keys) walks WHERE <c>_key_id = <old>; these
-- tables are small configuration tables, so the walk needs no index.
--
-- The registry of these families lives in internal/db/sealedcols and an
-- integration test cross-checks it against information_schema.

-- Rows whose only purpose is the discarded sealed payload.
DELETE FROM public.charlie_action_receipts;
DELETE FROM public.node_operations;
DELETE FROM public.resource_operations;
DELETE FROM public.dex_operations;
DELETE FROM public.sso_sessions;
DELETE FROM public.user_totp_recovery_codes;
DELETE FROM public.user_totp_enrollments;

ALTER TABLE public.backup_storage_configs
    DROP COLUMN encrypted_credentials,
    ADD COLUMN credentials_key_id text NOT NULL DEFAULT '',
    ADD COLUMN credentials_algorithm text NOT NULL DEFAULT '',
    ADD COLUMN credentials_ciphertext bytea,
    ADD COLUMN credentials_nonce bytea,
    ADD CONSTRAINT backup_storage_configs_credentials_sealed_shape CHECK (
        (credentials_key_id = '' AND credentials_algorithm = '' AND credentials_ciphertext IS NULL AND credentials_nonce IS NULL)
        OR (credentials_key_id <> '' AND credentials_algorithm <> '' AND octet_length(credentials_ciphertext) > 0)
    );

ALTER TABLE public.charlie_action_receipts
    DROP COLUMN arguments_encrypted,
    ADD COLUMN arguments_key_id text NOT NULL DEFAULT '',
    ADD COLUMN arguments_algorithm text NOT NULL DEFAULT '',
    ADD COLUMN arguments_ciphertext bytea,
    ADD COLUMN arguments_nonce bytea,
    ADD CONSTRAINT charlie_action_receipts_arguments_sealed_shape CHECK (
        (arguments_key_id = '' AND arguments_algorithm = '' AND arguments_ciphertext IS NULL AND arguments_nonce IS NULL)
        OR (arguments_key_id <> '' AND arguments_algorithm <> '' AND octet_length(arguments_ciphertext) > 0)
    );

ALTER TABLE public.charlie_action_receipts
    DROP COLUMN result_encrypted,
    ADD COLUMN result_key_id text NOT NULL DEFAULT '',
    ADD COLUMN result_algorithm text NOT NULL DEFAULT '',
    ADD COLUMN result_ciphertext bytea,
    ADD COLUMN result_nonce bytea,
    ADD CONSTRAINT charlie_action_receipts_result_sealed_shape CHECK (
        (result_key_id = '' AND result_algorithm = '' AND result_ciphertext IS NULL AND result_nonce IS NULL)
        OR (result_key_id <> '' AND result_algorithm <> '' AND octet_length(result_ciphertext) > 0)
    );

ALTER TABLE public.charlie_connections
    DROP COLUMN local_trust_material_encrypted,
    ADD COLUMN local_trust_material_key_id text NOT NULL DEFAULT '',
    ADD COLUMN local_trust_material_algorithm text NOT NULL DEFAULT '',
    ADD COLUMN local_trust_material_ciphertext bytea,
    ADD COLUMN local_trust_material_nonce bytea,
    ADD CONSTRAINT charlie_connections_local_trust_material_sealed_shape CHECK (
        (local_trust_material_key_id = '' AND local_trust_material_algorithm = '' AND local_trust_material_ciphertext IS NULL AND local_trust_material_nonce IS NULL)
        OR (local_trust_material_key_id <> '' AND local_trust_material_algorithm <> '' AND octet_length(local_trust_material_ciphertext) > 0)
    );

ALTER TABLE public.cloud_credentials
    DROP COLUMN data_encrypted,
    ADD COLUMN data_key_id text NOT NULL DEFAULT '',
    ADD COLUMN data_algorithm text NOT NULL DEFAULT '',
    ADD COLUMN data_ciphertext bytea,
    ADD COLUMN data_nonce bytea,
    ADD CONSTRAINT cloud_credentials_data_sealed_shape CHECK (
        (data_key_id = '' AND data_algorithm = '' AND data_ciphertext IS NULL AND data_nonce IS NULL)
        OR (data_key_id <> '' AND data_algorithm <> '' AND octet_length(data_ciphertext) > 0)
    );

ALTER TABLE public.cluster_registry_configs
    DROP COLUMN registry_password_encrypted,
    ADD COLUMN registry_password_key_id text NOT NULL DEFAULT '',
    ADD COLUMN registry_password_algorithm text NOT NULL DEFAULT '',
    ADD COLUMN registry_password_ciphertext bytea,
    ADD COLUMN registry_password_nonce bytea,
    ADD CONSTRAINT cluster_registry_configs_registry_password_sealed_shape CHECK (
        (registry_password_key_id = '' AND registry_password_algorithm = '' AND registry_password_ciphertext IS NULL AND registry_password_nonce IS NULL)
        OR (registry_password_key_id <> '' AND registry_password_algorithm <> '' AND octet_length(registry_password_ciphertext) > 0)
    );

ALTER TABLE public.delivery_sources
    DROP COLUMN ca_bundle_encrypted,
    ADD COLUMN ca_bundle_key_id text NOT NULL DEFAULT '',
    ADD COLUMN ca_bundle_algorithm text NOT NULL DEFAULT '',
    ADD COLUMN ca_bundle_ciphertext bytea,
    ADD COLUMN ca_bundle_nonce bytea,
    ADD CONSTRAINT delivery_sources_ca_bundle_sealed_shape CHECK (
        (ca_bundle_key_id = '' AND ca_bundle_algorithm = '' AND ca_bundle_ciphertext IS NULL AND ca_bundle_nonce IS NULL)
        OR (ca_bundle_key_id <> '' AND ca_bundle_algorithm <> '' AND octet_length(ca_bundle_ciphertext) > 0)
    );

ALTER TABLE public.delivery_sources
    DROP COLUMN credential_encrypted,
    ADD COLUMN credential_key_id text NOT NULL DEFAULT '',
    ADD COLUMN credential_algorithm text NOT NULL DEFAULT '',
    ADD COLUMN credential_ciphertext bytea,
    ADD COLUMN credential_nonce bytea,
    ADD CONSTRAINT delivery_sources_credential_sealed_shape CHECK (
        (credential_key_id = '' AND credential_algorithm = '' AND credential_ciphertext IS NULL AND credential_nonce IS NULL)
        OR (credential_key_id <> '' AND credential_algorithm <> '' AND octet_length(credential_ciphertext) > 0)
    );

ALTER TABLE public.delivery_system_releases
    DROP COLUMN registry_credential_encrypted,
    ADD COLUMN registry_credential_key_id text NOT NULL DEFAULT '',
    ADD COLUMN registry_credential_algorithm text NOT NULL DEFAULT '',
    ADD COLUMN registry_credential_ciphertext bytea,
    ADD COLUMN registry_credential_nonce bytea,
    ADD CONSTRAINT delivery_system_releases_registry_credential_sealed_shape CHECK (
        (registry_credential_key_id = '' AND registry_credential_algorithm = '' AND registry_credential_ciphertext IS NULL AND registry_credential_nonce IS NULL)
        OR (registry_credential_key_id <> '' AND registry_credential_algorithm <> '' AND octet_length(registry_credential_ciphertext) > 0)
    );

ALTER TABLE public.dex_operations
    DROP COLUMN payload_encrypted,
    ADD COLUMN payload_key_id text NOT NULL DEFAULT '',
    ADD COLUMN payload_algorithm text NOT NULL DEFAULT '',
    ADD COLUMN payload_ciphertext bytea,
    ADD COLUMN payload_nonce bytea,
    ADD CONSTRAINT dex_operations_payload_sealed_shape CHECK (
        (payload_key_id = '' AND payload_algorithm = '' AND payload_ciphertext IS NULL AND payload_nonce IS NULL)
        OR (payload_key_id <> '' AND payload_algorithm <> '' AND octet_length(payload_ciphertext) > 0)
    );

ALTER TABLE public.dex_settings
    DROP COLUMN public_clients_encrypted,
    ADD COLUMN public_clients_key_id text NOT NULL DEFAULT '',
    ADD COLUMN public_clients_algorithm text NOT NULL DEFAULT '',
    ADD COLUMN public_clients_ciphertext bytea,
    ADD COLUMN public_clients_nonce bytea,
    ADD CONSTRAINT dex_settings_public_clients_sealed_shape CHECK (
        (public_clients_key_id = '' AND public_clients_algorithm = '' AND public_clients_ciphertext IS NULL AND public_clients_nonce IS NULL)
        OR (public_clients_key_id <> '' AND public_clients_algorithm <> '' AND octet_length(public_clients_ciphertext) > 0)
    );

ALTER TABLE public.gitops_registration_sources
    DROP COLUMN auth_encrypted,
    ADD COLUMN auth_key_id text NOT NULL DEFAULT '',
    ADD COLUMN auth_algorithm text NOT NULL DEFAULT '',
    ADD COLUMN auth_ciphertext bytea,
    ADD COLUMN auth_nonce bytea,
    ADD CONSTRAINT gitops_registration_sources_auth_sealed_shape CHECK (
        (auth_key_id = '' AND auth_algorithm = '' AND auth_ciphertext IS NULL AND auth_nonce IS NULL)
        OR (auth_key_id <> '' AND auth_algorithm <> '' AND octet_length(auth_ciphertext) > 0)
    );

ALTER TABLE public.gitops_registration_sources
    DROP COLUMN webhook_secret_encrypted,
    ADD COLUMN webhook_secret_key_id text NOT NULL DEFAULT '',
    ADD COLUMN webhook_secret_algorithm text NOT NULL DEFAULT '',
    ADD COLUMN webhook_secret_ciphertext bytea,
    ADD COLUMN webhook_secret_nonce bytea,
    ADD CONSTRAINT gitops_registration_sources_webhook_secret_sealed_shape CHECK (
        (webhook_secret_key_id = '' AND webhook_secret_algorithm = '' AND webhook_secret_ciphertext IS NULL AND webhook_secret_nonce IS NULL)
        OR (webhook_secret_key_id <> '' AND webhook_secret_algorithm <> '' AND octet_length(webhook_secret_ciphertext) > 0)
    );

ALTER TABLE public.helm_repositories
    DROP COLUMN auth_config_encrypted,
    ADD COLUMN auth_config_key_id text NOT NULL DEFAULT '',
    ADD COLUMN auth_config_algorithm text NOT NULL DEFAULT '',
    ADD COLUMN auth_config_ciphertext bytea,
    ADD COLUMN auth_config_nonce bytea,
    ADD CONSTRAINT helm_repositories_auth_config_sealed_shape CHECK (
        (auth_config_key_id = '' AND auth_config_algorithm = '' AND auth_config_ciphertext IS NULL AND auth_config_nonce IS NULL)
        OR (auth_config_key_id <> '' AND auth_config_algorithm <> '' AND octet_length(auth_config_ciphertext) > 0)
    );

ALTER TABLE public.loki_ingest_tokens
    DROP COLUMN token_encrypted,
    ADD COLUMN token_key_id text NOT NULL DEFAULT '',
    ADD COLUMN token_algorithm text NOT NULL DEFAULT '',
    ADD COLUMN token_ciphertext bytea,
    ADD COLUMN token_nonce bytea,
    ADD CONSTRAINT loki_ingest_tokens_token_sealed_shape CHECK (
        (token_key_id = '' AND token_algorithm = '' AND token_ciphertext IS NULL AND token_nonce IS NULL)
        OR (token_key_id <> '' AND token_algorithm <> '' AND octet_length(token_ciphertext) > 0)
    );

ALTER TABLE public.management_backup_destinations
    DROP COLUMN encrypted_credentials,
    ADD COLUMN credentials_key_id text NOT NULL DEFAULT '',
    ADD COLUMN credentials_algorithm text NOT NULL DEFAULT '',
    ADD COLUMN credentials_ciphertext bytea,
    ADD COLUMN credentials_nonce bytea,
    ADD CONSTRAINT management_backup_destinations_credentials_sealed_shape CHECK (
        (credentials_key_id = '' AND credentials_algorithm = '' AND credentials_ciphertext IS NULL AND credentials_nonce IS NULL)
        OR (credentials_key_id <> '' AND credentials_algorithm <> '' AND octet_length(credentials_ciphertext) > 0)
    );

ALTER TABLE public.monitoring_backends
    DROP COLUMN auth_config_encrypted,
    ADD COLUMN auth_config_key_id text NOT NULL DEFAULT '',
    ADD COLUMN auth_config_algorithm text NOT NULL DEFAULT '',
    ADD COLUMN auth_config_ciphertext bytea,
    ADD COLUMN auth_config_nonce bytea,
    ADD CONSTRAINT monitoring_backends_auth_config_sealed_shape CHECK (
        (auth_config_key_id = '' AND auth_config_algorithm = '' AND auth_config_ciphertext IS NULL AND auth_config_nonce IS NULL)
        OR (auth_config_key_id <> '' AND auth_config_algorithm <> '' AND octet_length(auth_config_ciphertext) > 0)
    );

ALTER TABLE public.node_operations
    DROP COLUMN parameters_encrypted,
    ADD COLUMN parameters_key_id text NOT NULL DEFAULT '',
    ADD COLUMN parameters_algorithm text NOT NULL DEFAULT '',
    ADD COLUMN parameters_ciphertext bytea,
    ADD COLUMN parameters_nonce bytea,
    ADD CONSTRAINT node_operations_parameters_sealed_shape CHECK (
        (parameters_key_id = '' AND parameters_algorithm = '' AND parameters_ciphertext IS NULL AND parameters_nonce IS NULL)
        OR (parameters_key_id <> '' AND parameters_algorithm <> '' AND octet_length(parameters_ciphertext) > 0)
    );

ALTER TABLE public.prometheus_datasources
    DROP COLUMN auth_encrypted,
    ADD COLUMN auth_key_id text NOT NULL DEFAULT '',
    ADD COLUMN auth_algorithm text NOT NULL DEFAULT '',
    ADD COLUMN auth_ciphertext bytea,
    ADD COLUMN auth_nonce bytea,
    ADD CONSTRAINT prometheus_datasources_auth_sealed_shape CHECK (
        (auth_key_id = '' AND auth_algorithm = '' AND auth_ciphertext IS NULL AND auth_nonce IS NULL)
        OR (auth_key_id <> '' AND auth_algorithm <> '' AND octet_length(auth_ciphertext) > 0)
    );

ALTER TABLE public.resource_operations
    DROP COLUMN manifest_encrypted,
    ADD COLUMN manifest_key_id text NOT NULL DEFAULT '',
    ADD COLUMN manifest_algorithm text NOT NULL DEFAULT '',
    ADD COLUMN manifest_ciphertext bytea,
    ADD COLUMN manifest_nonce bytea,
    ADD CONSTRAINT resource_operations_manifest_sealed_shape CHECK (
        (manifest_key_id = '' AND manifest_algorithm = '' AND manifest_ciphertext IS NULL AND manifest_nonce IS NULL)
        OR (manifest_key_id <> '' AND manifest_algorithm <> '' AND octet_length(manifest_ciphertext) > 0)
    );

ALTER TABLE public.siem_forwarders
    DROP COLUMN auth_encrypted,
    ADD COLUMN auth_key_id text NOT NULL DEFAULT '',
    ADD COLUMN auth_algorithm text NOT NULL DEFAULT '',
    ADD COLUMN auth_ciphertext bytea,
    ADD COLUMN auth_nonce bytea,
    ADD CONSTRAINT siem_forwarders_auth_sealed_shape CHECK (
        (auth_key_id = '' AND auth_algorithm = '' AND auth_ciphertext IS NULL AND auth_nonce IS NULL)
        OR (auth_key_id <> '' AND auth_algorithm <> '' AND octet_length(auth_ciphertext) > 0)
    );

ALTER TABLE public.smtp_settings
    DROP COLUMN password_encrypted,
    ADD COLUMN password_key_id text NOT NULL DEFAULT '',
    ADD COLUMN password_algorithm text NOT NULL DEFAULT '',
    ADD COLUMN password_ciphertext bytea,
    ADD COLUMN password_nonce bytea,
    ADD CONSTRAINT smtp_settings_password_sealed_shape CHECK (
        (password_key_id = '' AND password_algorithm = '' AND password_ciphertext IS NULL AND password_nonce IS NULL)
        OR (password_key_id <> '' AND password_algorithm <> '' AND octet_length(password_ciphertext) > 0)
    );

ALTER TABLE public.sso_configurations
    DROP COLUMN client_secret_encrypted,
    ADD COLUMN client_secret_key_id text NOT NULL DEFAULT '',
    ADD COLUMN client_secret_algorithm text NOT NULL DEFAULT '',
    ADD COLUMN client_secret_ciphertext bytea,
    ADD COLUMN client_secret_nonce bytea,
    ADD CONSTRAINT sso_configurations_client_secret_sealed_shape CHECK (
        (client_secret_key_id = '' AND client_secret_algorithm = '' AND client_secret_ciphertext IS NULL AND client_secret_nonce IS NULL)
        OR (client_secret_key_id <> '' AND client_secret_algorithm <> '' AND octet_length(client_secret_ciphertext) > 0)
    );

ALTER TABLE public.sso_sessions
    DROP COLUMN upstream_id_token_encrypted,
    ADD COLUMN upstream_id_token_key_id text NOT NULL DEFAULT '',
    ADD COLUMN upstream_id_token_algorithm text NOT NULL DEFAULT '',
    ADD COLUMN upstream_id_token_ciphertext bytea,
    ADD COLUMN upstream_id_token_nonce bytea,
    ADD CONSTRAINT sso_sessions_upstream_id_token_sealed_shape CHECK (
        (upstream_id_token_key_id = '' AND upstream_id_token_algorithm = '' AND upstream_id_token_ciphertext IS NULL AND upstream_id_token_nonce IS NULL)
        OR (upstream_id_token_key_id <> '' AND upstream_id_token_algorithm <> '' AND octet_length(upstream_id_token_ciphertext) > 0)
    );

ALTER TABLE public.user_totp_enrollments
    DROP COLUMN secret_encrypted,
    ADD COLUMN secret_key_id text NOT NULL DEFAULT '',
    ADD COLUMN secret_algorithm text NOT NULL DEFAULT '',
    ADD COLUMN secret_ciphertext bytea,
    ADD COLUMN secret_nonce bytea,
    ADD CONSTRAINT user_totp_enrollments_secret_sealed_shape CHECK (
        (secret_key_id = '' AND secret_algorithm = '' AND secret_ciphertext IS NULL AND secret_nonce IS NULL)
        OR (secret_key_id <> '' AND secret_algorithm <> '' AND octet_length(secret_ciphertext) > 0)
    );

ALTER TABLE public.vault_connections
    DROP COLUMN auth_encrypted,
    ADD COLUMN auth_key_id text NOT NULL DEFAULT '',
    ADD COLUMN auth_algorithm text NOT NULL DEFAULT '',
    ADD COLUMN auth_ciphertext bytea,
    ADD COLUMN auth_nonce bytea,
    ADD CONSTRAINT vault_connections_auth_sealed_shape CHECK (
        (auth_key_id = '' AND auth_algorithm = '' AND auth_ciphertext IS NULL AND auth_nonce IS NULL)
        OR (auth_key_id <> '' AND auth_algorithm <> '' AND octet_length(auth_ciphertext) > 0)
    );

ALTER TABLE public.webhook_subscriptions
    DROP COLUMN secret_encrypted,
    ADD COLUMN secret_key_id text NOT NULL DEFAULT '',
    ADD COLUMN secret_algorithm text NOT NULL DEFAULT '',
    ADD COLUMN secret_ciphertext bytea,
    ADD COLUMN secret_nonce bytea,
    ADD CONSTRAINT webhook_subscriptions_secret_sealed_shape CHECK (
        (secret_key_id = '' AND secret_algorithm = '' AND secret_ciphertext IS NULL AND secret_nonce IS NULL)
        OR (secret_key_id <> '' AND secret_algorithm <> '' AND octet_length(secret_ciphertext) > 0)
    );

-- Constraints that referenced a dropped *_encrypted column were dropped with
-- it; restate them over the new key_id column.
ALTER TABLE public.charlie_action_receipts
    ADD CONSTRAINT charlie_receipt_arguments_present CHECK (arguments_key_id <> ''),
    ADD CONSTRAINT charlie_receipt_terminal_result CHECK (
        (state)::text <> ALL (ARRAY['blocked', 'deferred', 'succeeded', 'failed', 'fenced']::text[])
        OR (length((result_digest)::text) > 0 AND length((result_status)::text) > 0 AND result_key_id <> '')
    );

ALTER TABLE public.delivery_sources
    ADD CONSTRAINT delivery_source_credential_pair_valid CHECK (
        ((auth_mode)::text = 'none' AND credential_key_id = '' AND credential_key_version = 0)
        OR (auth_mode)::text <> 'none'
    );

ALTER TABLE public.node_operations
    ADD CONSTRAINT node_operations_identity_nonempty CHECK (
        length(trim(idempotency_scope)) > 0
        AND length(trim(idempotency_key)) > 0
        AND length(trim(node_name)) > 0
        AND parameters_key_id <> ''
    );

ALTER TABLE public.resource_operations
    ADD CONSTRAINT resource_operations_apply_manifest_required CHECK (action <> 'apply' OR manifest_key_id <> '');

COMMENT ON COLUMN public.delivery_sources.credential_ciphertext IS 'age-sealed source credential. Never select into list/detail/status/audit APIs.';
COMMENT ON COLUMN public.delivery_sources.ca_bundle_ciphertext IS 'age-sealed private CA bundle. Never expose through read APIs or agent status.';
COMMENT ON COLUMN public.delivery_system_releases.registry_credential_ciphertext IS 'age-sealed private-registry credential. Never expose through release, status, audit, or event APIs.';
COMMENT ON COLUMN public.dex_settings.public_clients_ciphertext IS 'age-sealed canonical JSON array of Dex static clients.';

-- dex_settings.public_clients was a plaintext compatibility copy of the Dex
-- static clients (secrets included), dual-written until a "Fernet cutover".
-- The sealed public_clients_* family is the only copy now.
ALTER TABLE public.dex_settings
    DROP CONSTRAINT dex_settings_public_clients_cutover,
    DROP COLUMN public_clients,
    DROP COLUMN public_clients_cutover_at;

-- Blank-only legacy plaintext columns. Writers have stored '' since their
-- sealed/hashed counterparts landed; the read fallbacks and the plaintext
-- sweep task that kept them alive are removed with Fernet.
ALTER TABLE public.backup_storage_configs
    DROP COLUMN access_key,
    DROP COLUMN secret_key;
ALTER TABLE public.cluster_registry_configs
    DROP COLUMN registry_password;
-- Agent/registration tokens authenticate by SHA-256 hash only. A row without a
-- hash could only ever match by its plaintext copy; drop those rows.
DELETE FROM public.cluster_registration_tokens WHERE token_hash = '';
DELETE FROM public.cluster_agent_tokens WHERE token_hash = '';
ALTER TABLE public.cluster_registration_tokens
    DROP COLUMN token,
    ADD CONSTRAINT cluster_registration_tokens_hash_present CHECK (token_hash <> '');
ALTER TABLE public.cluster_agent_tokens
    DROP COLUMN token,
    ADD CONSTRAINT cluster_agent_tokens_hash_present CHECK (token_hash <> '');

-- dex_connectors kept each connector secret (clientSecret, bindPW) as a Fernet
-- string INSIDE the config JSONB, where rotation could not see it. The secret
-- fields now live in their own sealed family as one JSON object
-- {"clientSecret": "..."}; config keeps only the non-secret fields. Stripping the
-- stale Fernet strings goes through the runtime-generation trigger on purpose:
-- the connectors need their secrets re-entered before Dex is re-enabled.
ALTER TABLE public.dex_connectors
    ADD COLUMN secrets_key_id text NOT NULL DEFAULT '',
    ADD COLUMN secrets_algorithm text NOT NULL DEFAULT '',
    ADD COLUMN secrets_ciphertext bytea,
    ADD COLUMN secrets_nonce bytea,
    ADD CONSTRAINT dex_connectors_secrets_sealed_shape CHECK (
        (secrets_key_id = '' AND secrets_algorithm = '' AND secrets_ciphertext IS NULL AND secrets_nonce IS NULL)
        OR (secrets_key_id <> '' AND secrets_algorithm <> '' AND octet_length(secrets_ciphertext) > 0)
    );
UPDATE public.dex_connectors SET config = config - 'clientSecret' - 'bindPW'
WHERE config ?| ARRAY['clientSecret', 'bindPW'];

-- deferred_operations.operation_spec carried the maintenance-gate request (which
-- can hold credentials) as a Fernet envelope inside JSONB. That request now
-- lives in its own sealed family; operation_spec keeps only plaintext,
-- non-secret specs (agent upgrades). Old envelopes cannot be replayed.
DELETE FROM public.deferred_operations WHERE operation_spec ? 'ciphertext';
ALTER TABLE public.deferred_operations
    ADD COLUMN sealed_spec_key_id text NOT NULL DEFAULT '',
    ADD COLUMN sealed_spec_algorithm text NOT NULL DEFAULT '',
    ADD COLUMN sealed_spec_ciphertext bytea,
    ADD COLUMN sealed_spec_nonce bytea,
    ADD CONSTRAINT deferred_operations_sealed_spec_sealed_shape CHECK (
        (sealed_spec_key_id = '' AND sealed_spec_algorithm = '' AND sealed_spec_ciphertext IS NULL AND sealed_spec_nonce IS NULL)
        OR (sealed_spec_key_id <> '' AND sealed_spec_algorithm <> '' AND octet_length(sealed_spec_ciphertext) > 0)
    );
-- +goose StatementEnd
