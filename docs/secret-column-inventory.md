# Secret Column Inventory

Date: 2026-09-17

The greenfield database is defined only by `001_initial.up.sql`. Every
secret-looking text, JSON, UUID, or byte column in that file is classified by
`internal/db/migrations/migration_secret_columns_test.go`; adding an
unclassified column fails CI.

## Encrypted or hashed material

| Table / column | Classification | Runtime rule |
| --- | --- | --- |
| `users.password` | Password hash | Never plaintext. |
| `sso_configurations.client_secret_encrypted` | Versioned authenticated ciphertext | Write-only secret. |
| `smtp_settings.password_encrypted` | Versioned authenticated ciphertext | Write-only secret. |
| `user_totp_enrollments.secret_encrypted` | Versioned authenticated ciphertext | Never returned. |
| `webhook_subscriptions.secret_encrypted` | Versioned authenticated ciphertext | Never returned. |
| `sso_sessions.upstream_id_token_encrypted` | Versioned authenticated ciphertext | Used only for upstream session lifecycle. |
| `backup_storage_configs.encrypted_credentials` | Versioned authenticated ciphertext | Complete object-store credential envelope. |
| `management_backup_destinations.encrypted_credentials` | Versioned authenticated ciphertext | Complete management-plane dump object-store credential envelope. |
| `cluster_registry_configs.registry_password_encrypted` | Versioned authenticated ciphertext | Complete cluster registry password. |
| `project_registry_credentials.registry_credential_encrypted` | Versioned authenticated ciphertext | Complete project registry credential. |
| `delivery_sources.credential_encrypted` | Versioned authenticated ciphertext | Complete write-only delivery-source credential map. |
| `gitops_registration_sources.webhook_secret_encrypted` | Versioned authenticated ciphertext | Per-source GitHub webhook HMAC secret; write-only and never logged. |
| `dex_operations.payload_encrypted` | Versioned authenticated ciphertext | Durable, bounded Dex SSO-finalization input; never returned or logged. |
| `api_tokens.token_hash` | Password-style token hash | Plaintext is returned once. |
| `cluster_registration_tokens.token_hash` | Token hash | Registration authentication uses only the hash. |
| `cluster_agent_tokens.token_hash` | Token hash | Active agent authentication uses only the hash. |
| `cluster_agent_tokens.previous_token_hash` | Token hash | Rotation grace window only. |
| `password_reset_tokens.token_hash` | Token hash | Plaintext is returned once. |
| `password_reset_tokens.password_hash_at_issue` | Password-hash snapshot | Invalidates reset tokens after a password change. |
| `scim_tokens.token_hash` | Token hash | Plaintext is returned once. |
| `refresh_session_families.family_hash` | SHA-256 lookup hash | Hash of the random JWT session-family ID; raw family IDs are never stored. |
| `refresh_session_tokens.jti_hash` | SHA-256 lookup hash | One-time refresh-token lookup; raw JTIs are never stored. |
| `refresh_session_tokens.replaced_by_jti_hash` | SHA-256 lookup hash | Rotation lineage without retaining a bearer identifier. |
| `charlie_connections.local_trust_material_encrypted` | Versioned authenticated ciphertext | Astronomer-owned local CA/private-key and bridge/MCP TLS material only. |
| `charlie_action_receipts.arguments_encrypted` | Versioned authenticated ciphertext | Bounded postcondition-reconciliation input; excluded from logs and support bundles. |
| `charlie_action_receipts.result_encrypted` | Versioned authenticated ciphertext | Bounded idempotent replay result; excluded from logs and support bundles. |
| `charlie_delegations.authorization_hash` | SHA-256 lookup hash | Hash of an opaque, short-lived authorization reference. |
| `charlie_connections.agent_secret_hmac` | Keyed digest | Reconciles deterministic Kubernetes Secret content without retaining the secret. |
| `loki_ingest_tokens.token_hash` | Token hash | SHA-256 of the hosted Loki ingest bearer. Projected into the management-cluster hash Secret; never plaintext. |
| `loki_ingest_tokens.token_encrypted` | Versioned authenticated ciphertext | Re-renders the member Kubernetes Secret `astronomer-loki-ingest-token`. Fluent Bit OUTPUT uses `bearer_token_file`. List APIs never return it. |

Every new encrypted write uses
`astronomer:v1:fernet:<key-id>:<ciphertext>`. The envelope authenticates the
payload through Fernet and identifies the exact read key without trial
decryption. Raw Fernet tokens remain a measured, read-only compatibility path
during migration (`astronomer_ciphertext_decryptions_total{format="legacy"}`);
`keyrotate` rewrites them to the primary-key envelope before fallback removal.

`logging_outputs.configuration` is not a secret column. System (`is_system`) Loki rows store only `host`, `port`, `tls`, `tenant_id`, and `labels`. The member copy lives in Secret `astronomer-loki-ingest-token` (mounted via fluent-bit Helm `extraVolumes` / `extraVolumeMounts`); the ConfigMap references `bearer_token_file` only. Plaintext is loaded at apply time from `loki_ingest_tokens.token_encrypted` and is never stored in JSONB or returned by list/get.

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

The migration classifier also sees the function-local variable `token` in
`017_durable_audit_siem_fanout.up.sql`. It is not a database column: it holds
one character from a bounded SIEM/webhook glob pattern while
`astronomer_event_glob_match` evaluates `*` and `?`. It never contains or
persists authentication material.

## Deprecated blank-only compatibility fields

`backup_storage_configs.access_key`, `backup_storage_configs.secret_key`,
`cluster_registry_configs.registry_password`, `cluster_registration_tokens.token`,
and `cluster_agent_tokens.token` remain structurally present for subsystems not
being redesigned in this delivery cutover. Production writers store empty
strings and use their encrypted or hashed counterparts. The credential
migration worker fails closed if it encounters non-empty historical values.
They are not used by the Flux delivery implementation.
