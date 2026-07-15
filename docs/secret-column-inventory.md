# Secret Column Inventory

Date: 2026-06-12

This inventory classifies secret-looking Postgres columns. New migrations that
add `password`, `secret`, `token`, `credential`, `access_key`, `secret_key`,
or similar column names must be added to the migration guard in
`internal/db/migrations/migration_secret_columns_test.go` with an explicit
classification.

## Encrypted Or Hashed

| Table / Column | Classification | Notes |
| --- | --- | --- |
| `users.password` | hashed | Bcrypt password hash, not plaintext. |
| `sso_configurations.client_secret_encrypted` | encrypted | Fernet ciphertext. |
| `api_tokens.token_hash` | hashed | API tokens are stored by hash. |
| `argocd_instances.auth_token_encrypted` | encrypted | Fernet ciphertext. |
| `backup_storage_configs.encrypted_credentials` | encrypted | Fernet ciphertext for object-store credentials; new backup storage writes use this column when a Fernet key is configured. |
| `user_totp_enrollments.secret_encrypted` | encrypted | Fernet ciphertext for TOTP shared secret. |
| `smtp_settings.password_encrypted` | encrypted | Fernet ciphertext. |
| `password_reset_tokens.token_hash` | hashed | Reset token hash only. |
| `password_reset_tokens.password_hash_at_issue` | hashed snapshot | Used to invalidate reset tokens after password changes. |
| `webhook_subscriptions.secret_encrypted` | encrypted | Fernet ciphertext for webhook signing secrets. |
| `sso_sessions.upstream_id_token_encrypted` | encrypted | Fernet ciphertext for upstream logout/session token. |
| `argocd_cluster_proxy_tokens.token_hash` | hashed | Lookup hash for ArgoCD proxy service token. |
| `argocd_cluster_proxy_tokens.token_encrypted` | encrypted | Fernet ciphertext for service token material. |
| `cluster_registration_tokens.token_hash` | hashed | Lookup hash for short-lived cluster registration tokens. |
| `cluster_agent_tokens.token_hash` | hashed | Lookup hash for durable cluster agent tokens. |
| `cluster_agent_tokens.previous_token_hash` | hashed | Lookup hash for the previous durable agent token during a rotation grace window; cleared once the agent adopts the new token (or by the grace-TTL backstop). |
| `cluster_registry_configs.registry_password_encrypted` | encrypted | Fernet ciphertext for registry passwords; new registry writes use this column when a Fernet key is configured. |
| `scim_tokens.token_hash` | hashed | Lookup hash for SCIM provisioning bearer tokens; plaintext is shown once at creation and never stored. |
| `dex_settings.public_clients_encrypted` | encrypted | Fernet ciphertext containing canonical Dex static-client JSON. It becomes authoritative after the explicit quiesced cutover. |

## Legacy Plaintext To Migrate

| Table / Column | Current State | Required Fix |
| --- | --- | --- |
| `cluster_registration_tokens.token` | deprecated plaintext token | New writes store only `token_hash`; keep this column for one release so existing plaintext rows can be backfilled and expired safely. |
| `cluster_agent_tokens.token` | deprecated plaintext token | New writes store only `token_hash`; keep existing plaintext rows for one release so currently enrolled agents can reconnect and rotate safely. |
| `cluster_registry_configs.registry_password` | deprecated plaintext credential | New encrypted writes blank this column; `security:migrate_plaintext_credentials` encrypts and blanks legacy rows when a Fernet key is configured. |
| `backup_storage_configs.access_key` | deprecated plaintext credential | New encrypted writes blank this column; `security:migrate_plaintext_credentials` encrypts and blanks legacy rows when a Fernet key is configured. |
| `backup_storage_configs.secret_key` | deprecated plaintext credential | New encrypted writes blank this column; `security:migrate_plaintext_credentials` encrypts and blanks legacy rows when a Fernet key is configured. |
| `dex_settings.public_clients` | mixed-version compatibility credential | Dual-written only until `keyrotate --dex-public-clients-cutover-confirmed` CAS-scrubs it and stamps `public_clients_cutover_at`; later writes are DB-rejected. |

## Non-Secret References Or Metadata

| Table / Column | Classification | Notes |
| --- | --- | --- |
| `cluster_monitoring_configs.object_storage_secret_name` | Kubernetes Secret reference | Name only; secret material is not stored in this column. |
| `argocd_managed_clusters.cluster_secret_name` | Kubernetes Secret reference | Name of ArgoCD cluster Secret. |
| `cluster_registry_configs.secret_name` | Kubernetes Secret reference | Target pull-secret name. |
| `cloud_credential_materializations.credential_id` | foreign key | References credential metadata; not secret material. |
| `cloud_credential_materializations.secret_name` | Kubernetes Secret reference | Target materialized Secret name. |
| `argocd_cluster_proxy_tokens.token_prefix` | token metadata | Prefix only for display/audit correlation. |
| `dex_settings.runtime_secret_name` | Kubernetes Secret reference | Stable retained Dex runtime Secret name; contains no credential material. |
