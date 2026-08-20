# Secret Column Inventory

Date: 2026-08-17

The greenfield database is defined only by `001_initial.up.sql`. Every
secret-looking text, JSON, UUID, or byte column in that file is classified by
`internal/db/migrations/migration_secret_columns_test.go`; adding an
unclassified column fails CI.

## Encrypted or hashed material

| Table / column | Classification | Runtime rule |
| --- | --- | --- |
| `users.password` | Password hash | Never plaintext. |
| `sso_configurations.client_secret_encrypted` | Fernet ciphertext | Write-only secret. |
| `smtp_settings.password_encrypted` | Fernet ciphertext | Write-only secret. |
| `user_totp_enrollments.secret_encrypted` | Fernet ciphertext | Never returned. |
| `webhook_subscriptions.secret_encrypted` | Fernet ciphertext | Never returned. |
| `sso_sessions.upstream_id_token_encrypted` | Fernet ciphertext | Used only for upstream session lifecycle. |
| `backup_storage_configs.encrypted_credentials` | Fernet ciphertext | Complete object-store credential envelope. |
| `management_backup_destinations.encrypted_credentials` | Fernet ciphertext | Complete management-plane dump object-store credential envelope. |
| `cluster_registry_configs.registry_password_encrypted` | Fernet ciphertext | Complete cluster registry password. |
| `project_registry_credentials.registry_credential_encrypted` | Fernet ciphertext | Complete project registry credential. |
| `delivery_sources.credential_encrypted` | Fernet ciphertext | Complete write-only delivery-source credential map. |
| `api_tokens.token_hash` | Password-style token hash | Plaintext is returned once. |
| `cluster_registration_tokens.token_hash` | Token hash | Registration authentication uses only the hash. |
| `cluster_agent_tokens.token_hash` | Token hash | Active agent authentication uses only the hash. |
| `cluster_agent_tokens.previous_token_hash` | Token hash | Rotation grace window only. |
| `password_reset_tokens.token_hash` | Token hash | Plaintext is returned once. |
| `password_reset_tokens.password_hash_at_issue` | Password-hash snapshot | Invalidates reset tokens after a password change. |
| `scim_tokens.token_hash` | Token hash | Plaintext is returned once. |
| `charlie_connections.local_trust_material_encrypted` | Fernet ciphertext | Astronomer-owned local CA/private-key and bridge/MCP TLS material only. |
| `charlie_action_receipts.arguments_encrypted` | Fernet ciphertext | Bounded postcondition-reconciliation input; excluded from logs and support bundles. |
| `charlie_action_receipts.result_encrypted` | Fernet ciphertext | Bounded idempotent replay result; excluded from logs and support bundles. |
| `charlie_delegations.authorization_hash` | SHA-256 lookup hash | Hash of an opaque, short-lived authorization reference. |
| `charlie_connections.agent_secret_hmac` | Keyed digest | Reconciles deterministic Kubernetes Secret content without retaining the secret. |
| `loki_ingest_tokens.token_hash` | Token hash | SHA-256 of the hosted Loki ingest bearer. Projected into the management-cluster hash Secret; never plaintext. |
| `loki_ingest_tokens.token_encrypted` | Fernet ciphertext | Re-renders the member Fluent Bit ConfigMap `bearer_token`. List APIs never return it. |

`logging_outputs.configuration` is not a secret column. System (`is_system`) Loki rows store only `host`, `port`, `tls`, `tenant_id`, and `labels`. The member Fluent Bit ConfigMap `bearer_token` is an accepted secret-policy exception (same class as Splunk HEC in ConfigMaps): plaintext is loaded at apply time from `loki_ingest_tokens.token_encrypted` and is never stored in JSONB or returned by list/get.

## References and non-secret metadata

| Column family | Classification |
| --- | --- |
| `*_secret_name`, `runtime_secret_name`, `object_storage_secret_name`, `agent_secret_name` | Kubernetes Secret object name only. |
| `credential_id` | Foreign-key reference, not credential material. |
| `credential_state` | Bounded lifecycle enum. |
| `delivery_sources.credential_key_version`, `delivery_sources.credential_epoch` | Encryption-key and rotation generation metadata. |
| `delivery_assignment_receipts.credential_content_digest` | SHA-256 over deployment IDs and credential epochs; no secret or ciphertext input. |

## Deprecated blank-only compatibility fields

`backup_storage_configs.access_key`, `backup_storage_configs.secret_key`,
`cluster_registry_configs.registry_password`, `cluster_registration_tokens.token`,
and `cluster_agent_tokens.token` remain structurally present for subsystems not
being redesigned in this delivery cutover. Production writers store empty
strings and use their encrypted or hashed counterparts. The credential
migration worker fails closed if it encounters non-empty historical values.
They are not used by the Flux delivery implementation.
