# Secret Column Inventory

Date: 2026-10-20 (age cutover, migration 00082, ADR-0010)

Every secret column value is sealed with age through `pkg/crypto` into a
**sealed family** of four columns — `<c>_key_id`, `<c>_algorithm`
(`age-x25519-chacha20poly1305`), `<c>_ciphertext` (bytea) and `<c>_nonce`
(bytea, NULL for age; reserved for AEAD algorithms with an external nonce) —
with a CHECK that the value is either fully present or fully absent. The Go
registry `internal/db/sealedcols.Columns` lists every family; an integration
test fails if the live schema has an unregistered `*_ciphertext` column, and
`astro admin rotate-keys` rewrites exactly the registered families. Reads pick
the key by `<c>_key_id`; there is no trial decryption and no fallback to
plaintext or to the ciphertext.

## Sealed families (`sealedcols.Columns`)

| Table / family | Contents | Runtime rule |
| --- | --- | --- |
| `backup_storage_configs.credentials_*` | Complete object-store credential document | Write-only; never returned. |
| `charlie_action_receipts.arguments_*` | Bounded postcondition-reconciliation input | Excluded from logs and support bundles; required (CHECK). |
| `charlie_action_receipts.result_*` | Bounded idempotent replay result | Required once the receipt is terminal (CHECK). |
| `charlie_connections.local_trust_material_*` | Astronomer-owned local CA/private key and bridge/MCP TLS material | Never returned. |
| `cloud_credentials.data_*` | Cloud provider credential document | Write-only. |
| `cluster_registry_configs.registry_password_*` | Cluster private-registry password | Write-only; no plaintext path without a key. |
| `deferred_operations.sealed_spec_*` | Maintenance-gate deferred request (may carry credentials) | Decrypted only by the replayer. |
| `delivery_sources.ca_bundle_*` | Private CA bundle | Never exposed through read APIs or agent status. |
| `delivery_sources.credential_*` | Delivery-source credential map | Write-only; `auth_mode='none'` requires it absent (CHECK). |
| `delivery_system_releases.registry_credential_*` | System-release registry credential | Never exposed through release/status/audit APIs. |
| `dex_connectors.secrets_*` | JSON object of the connector's secret fields (`clientSecret`, `bindPW`) | `config` holds only non-secret fields. |
| `dex_operations.payload_*` | Durable Dex SSO-finalization input | Never returned or logged. |
| `dex_settings.public_clients_*` | Canonical JSON array of Dex static clients (secrets included) | The only copy; the plaintext `public_clients` column was dropped. |
| `gitops_registration_sources.auth_*` | Git credential (PAT / SSH key) | Write-only. |
| `gitops_registration_sources.webhook_secret_*` | Per-source webhook HMAC secret | Write-only; never logged. |
| `helm_repositories.auth_config_*` | Complete repository credential document | `auth_config` JSONB keeps only the non-secret projection (`internal/credential`). |
| `loki_ingest_tokens.token_*` | Hosted Loki ingest bearer | Re-renders the member Secret; `token_hash` is the lookup. |
| `management_backup_destinations.credentials_*` | Management-plane dump object-store credential | Write-only. |
| `monitoring_backends.auth_config_*` | Credential portion of the monitoring config bag | `auth_config` JSONB keeps the allow-listed non-secret keys. |
| `node_operations.parameters_*` | Node operation parameters | Required (CHECK). |
| `prometheus_datasources.auth_*` | Datasource auth blob | Write-only. |
| `resource_operations.manifest_*` | Apply manifest | Required for `apply` (CHECK). |
| `siem_forwarders.auth_*` | SIEM auth blob | Write-only. |
| `smtp_settings.password_*` | SMTP password | Write-only. |
| `sso_configurations.client_secret_*` | OAuth/OIDC client secret | Write-only. |
| `sso_sessions.upstream_id_token_*` | Upstream ID token for logout | Upstream session lifecycle only. |
| `user_totp_enrollments.secret_*` | TOTP seed | Never returned. |
| `vault_connections.auth_*` | Vault auth blob | Write-only. |
| `webhook_subscriptions.secret_*` | Webhook HMAC signing secret | Never returned. |

## Hashed material

| Table / column | Classification | Runtime rule |
| --- | --- | --- |
| `users.password` | Password hash | Never plaintext. |
| `api_tokens.token_hash` | Password-style token hash | Plaintext is returned once. |
| `cluster_registration_tokens.token_hash` | Token hash (CHECK non-empty) | Registration authentication uses only the hash. |
| `cluster_agent_tokens.token_hash` | Token hash (CHECK non-empty) | Active agent authentication uses only the hash. |
| `cluster_agent_tokens.previous_token_hash` | Token hash | Rotation grace window only. |
| `password_reset_tokens.token_hash` | Token hash | Plaintext is returned once. |
| `password_reset_tokens.password_hash_at_issue` | Password-hash snapshot | Invalidates reset tokens after a password change. |
| `scim_tokens.token_hash` | Token hash | Plaintext is returned once. |
| `refresh_session_families.family_hash` | SHA-256 lookup hash | Raw family IDs are never stored. |
| `refresh_session_tokens.jti_hash` | SHA-256 lookup hash | Raw JTIs are never stored. |
| `refresh_session_tokens.replaced_by_jti_hash` | SHA-256 lookup hash | Rotation lineage without retaining a bearer identifier. |
| `charlie_delegations.authorization_hash` | SHA-256 lookup hash | Hash of an opaque, short-lived authorization reference. |
| `charlie_connections.agent_secret_hmac` | Keyed digest | Reconciles deterministic Secret content without retaining the secret. |
| `loki_ingest_tokens.token_hash` | Token hash | Projected into the management-cluster hash Secret. |

Hashes are not encryption and are not rotated by `rotate-keys`.

## Removed in migration 00082

`*_encrypted` / `encrypted_credentials` (Fernet envelope strings, discarded —
re-enter dev secrets), `dex_settings.public_clients` (plaintext copy of Dex
static clients) and `public_clients_cutover_at`, and the blank-only plaintext
columns `backup_storage_configs.access_key`, `backup_storage_configs.secret_key`,
`cluster_registry_configs.registry_password`, `cluster_registration_tokens.token`,
`cluster_agent_tokens.token`. Fernet strings inside `dex_connectors.config` and
the Fernet envelope inside `deferred_operations.operation_spec` moved to the
`secrets_*` / `sealed_spec_*` families.

## Not yet sealed (follow-up)

These columns can hold operator-supplied secrets in plaintext today. Sealing
them needs a secret/non-secret split of free-form, operator-authored documents
(the `internal/credential` pattern) and is out of the age cutover's scope:

| Table / column | What can be secret |
| --- | --- |
| `logging_outputs.configuration` | Output passwords/tokens/API keys other than `bearer_token` (stripped on write). |
| `notification_channels.configuration` | Slack/Teams/webhook URLs with embedded tokens, PagerDuty routing keys. |
| `webhook_subscriptions.extra_headers` | Operator-set `Authorization`/API-key headers. |
| `email_messages.body_text`, `body_html` | Rendered password-reset links (single-use, short-lived token) until the retention sweep. |

System (`is_system`) Loki rows in `logging_outputs.configuration` store only `host`, `port`, `tls`, `tenant_id`, and `labels`. The member copy of the ingest bearer lives in Secret `astronomer-loki-ingest-token` (mounted via fluent-bit Helm `extraVolumes` / `extraVolumeMounts`); the ConfigMap references `bearer_token_file` only. Plaintext is loaded at apply time from the `loki_ingest_tokens.token_*` family and is never stored in JSONB or returned by list/get.

## References and non-secret metadata

`installed_charts.drift_claim_token`, `project_namespaces.reconcile_claim_token`,
and `cluster_decommissions.decommission_claim_token` are short-lived random
ownership nonces used only to fence distributed worker writes. They grant no
API authority and are cleared when the claimed work completes or is released.

| Column family | Classification |
| --- | --- |
| `*_secret_name`, `runtime_secret_name`, `object_storage_secret_name`, `agent_secret_name` | Kubernetes Secret object name only. |
| `credential_id` | Foreign-key reference, not credential material. |
| `credential_state` | Bounded lifecycle enum. |
| `delivery_sources.credential_key_version`, `delivery_sources.credential_epoch` | Encryption-key and rotation generation metadata. |
| `delivery_assignment_receipts.credential_content_digest` | SHA-256 over deployment IDs and credential epochs; no secret or ciphertext input. |
| `vulnerability_database_cluster_state.assignment_credential_digest`, `vulnerability_database_scan_claims.assignment_credential_digest` | Copy of the delivery receipt credential epoch digest; no credential material. |
| `vulnerability_database_scan_claims.lease_token` | Short-lived scan-claim fencing nonce; requires existing authenticated claim ownership and grants no standalone API authority. |
| `vulnerability_database_scan_claims.authority_secret_uid`, `vulnerability_database_scan_claims.image_pull_secret_uid` | Kubernetes Secret object UIDs used to bind observed resources; no Secret contents. |
| `delivery_configuration_templates.secret_refs` | Kubernetes Secret names, keys, and Helm target paths only; secret values are never stored. |

The migration classifier also sees the function-local variable `token` in
`017_durable_audit_siem_fanout.up.sql`. It is not a database column: it holds
one character from a bounded SIEM/webhook glob pattern while
`astronomer_event_glob_match` evaluates `*` and `?`. It never contains or
persists authentication material.

