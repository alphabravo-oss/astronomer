# Secret Handling Policy

Date: 2026-06-14
Status: Active engineering policy

This policy defines how Astronomer stores, moves, displays, audits, backs up,
and exports secret material. New work that introduces credentials must comply
with this document, `docs/secret-column-inventory.md`, `docs/threat-model.md`,
and `docs/control-plane-state-contract.md`.

## Core Rules

1. Do not persist plaintext secrets unless a design review explicitly approves
   the exception and documents rotation, retention, and blast radius.
2. Authentication tokens must be hash-only after initial display or handoff.
3. Required reusable credentials must be encrypted at rest with
   `ASTRONOMER_ENCRYPTION_KEY` through `auth.Encryptor`.
4. Operator-owned runtime secrets should prefer external references, especially
   Vault references, instead of DB-originated cleartext.
5. APIs, audit rows, logs, support bundles, diagnostics bundles, CRD status, and
   UI responses must not emit plaintext secret values.
6. Secret-bearing operations must be permission-gated, audited without secret
   values, and covered by tests.

## Storage Classes

| Class | Storage | Examples | Required behavior |
| --- | --- | --- | --- |
| Password verifier | One-way password hash | `users.password` | Store bcrypt or approved legacy hash during migration only; never expose in API/export. |
| One-time or bearer token | Hash only | API tokens, password reset tokens, registration tokens after migration, agent tokens | Display plaintext once, store hash, compare by hash, audit lifecycle without token value. |
| Reusable integration credential | Fernet ciphertext | Argo CD auth token, SMTP password, SSO client secret, Vault auth blob, cluster registry password, cloud credentials | Encrypt before persistence, decrypt only at the consumer boundary, redact on read. |
| External reference | Reference string | `${vault://connection/mount/path#key}` | Persist the reference, resolve at execution/install time, never write resolved value back to Postgres. |
| Kubernetes Secret | Kubernetes Secret object | Helm bootstrap Secret, chart-managed TLS Secret, pull secret | Prefer existing/external Secret in production; do not copy raw data into support bundles or CRD status. |
| Public certificate metadata | Metadata only | TLS certificate subject, DNS names, not-after date | Certificate PEM bytes and private keys stay out of bundles; status can include bounded public metadata. |

## Approved Encrypted Columns

Every secret-looking Postgres column must appear in
`docs/secret-column-inventory.md` and be classified as `hashed`, `encrypted`,
`legacy-migrating`, or `approved-plaintext`.

The current policy expects these major categories:

- `*_token_hash`: hash-only token lookup.
- `*_encrypted`: Fernet ciphertext under `auth.Encryptor`.
- Legacy plaintext columns must have a migration path and test coverage proving
  new writes do not reintroduce plaintext.

The migration guard in `internal/db/migrations/migration_secret_columns_test.go`
must fail if a new secret-looking column is added without inventory coverage.

## Encryption-Key Requirements

`ASTRONOMER_ENCRYPTION_KEY` is a platform root secret.

- Production must not use the development key from chart defaults.
- The key must be restored with the database during disaster recovery.
- Backups must never be stored with the encryption key.
- Key rotation must use the key-rotation tooling and preserve old keys until
  all ciphertext is rewritten.
- If the key is lost, encrypted integration credentials must be re-entered or
  rotated at the source system.

See `docs/management-plane-dr-runbook.md` and
`docs/secret-rotation-runbook.md`.

## External Secret Manager

Vault references are the preferred external-secret path for operator-owned
values used during install or reconciliation.

Accepted reference forms are documented in `docs/vault-integration.md`.

Required behavior:

- Persist the Vault reference, not the resolved secret.
- Resolve as late as possible, at the execution boundary.
- Audit the reference path and outcome, not the value.
- Never include resolved values in logs, support bundles, diagnostics, or task
  failure payloads.
- On Vault failure, fail closed for the operation that needs the secret.

## API and UI Rules

- Create/update APIs may accept a secret value only when the handler encrypts,
  hashes, or externalizes it before persistence.
- Read APIs must return a sentinel such as `[redacted]`, `<encrypted>`, or a
  provider-specific placeholder.
- UIs must not preserve returned redaction sentinels as new secret values unless
  the handler explicitly supports "leave unchanged" semantics.
- Bulk export, diagnostics, support bundles, and CSV downloads must run through
  a redaction pass before writing bytes to the response.
- Secret reads from adopted clusters must require `secrets:read`, `secrets:list`,
  or `secrets:watch` as appropriate and emit `cluster.secret.read` without
  secret values.

## Audit, Logs, and Bundles

Audit detail may include:

- Secret reference names.
- Operation IDs.
- Resource names.
- Hash algorithm names.
- Redaction markers.
- Rotation counts.

Audit detail must not include:

- Passwords.
- Bearer tokens.
- API tokens.
- OAuth/OIDC client secrets.
- TOTP shared secrets or recovery codes.
- Private keys.
- Kubeconfigs.
- Vault-resolved values.
- Raw Kubernetes Secret data.

Shared diagnostic/support-bundle redaction lives in `internal/redaction`.
Domain-specific redactors may remain specialized only when they need stronger
semantics, such as provider-specific cloud credential fields or Vault auth
sentinels.

## Redaction Ownership Decision

The project uses two intentionally different redaction classes:

- `internal/redaction` is the shared, best-effort safety net for diagnostic
  JSON, support bundles, pod logs, exported operational summaries, and other
  payloads where preserving exact API semantics is not required.
- Domain-specific redactors stay in the domain package when the redaction marker
  is part of an API contract or update/merge behavior.

Current specialized owners:

- Cloud credentials: `internal/handler/cloud_credentials.go` owns
  provider-aware secret field redaction and the `cloudcreds.SecretSentinel`
  preserve-stored-value contract. The shared redactor is too broad for this
  path because non-secret provider fields must round-trip unchanged.
- Vault connections: `internal/handler/vault.go` and `internal/vault` own
  auth-method-specific sentinels such as `<encrypted>`. Token and AppRole
  secret parts are redacted, while non-secret fields such as AppRole `role_id`
  and Kubernetes auth metadata may round-trip.
- Audit detail: `internal/audit` owns deterministic JSONB audit sanitization.
  Audit rows need stable, queryable detail shape and should not inherit future
  broad log-line heuristics without an audit migration review.
- Argo API/UI payloads: `internal/argosecurity` owns recursive Application
  source, history, operation, manifest, and URL-credential sanitation plus the
  matching reference-only mutation policy.

New diagnostic/export surfaces must use `internal/redaction` by default. New
credential APIs must either reuse an existing domain-specific sentinel contract
or document a new one in this policy and cover create, read, update, audit, and
support-bundle behavior with tests.

## CRD and Argo Rules

- CRD specs must use `secretRef` or external references for secret material.
- CRD status must never include rendered secret values or raw Secret data.
- Generated Argo `ApplicationSet` and `Application` resources must not embed
  inline credentials.
- Argo cluster Secret labels may include bounded non-secret targeting metadata
  only.
- Support bundles can include Argo health and label metadata, but never Argo
  auth tokens or raw cluster Secret data.
- The `/argocd/api/*` proxy sanitizes complete JSON and `+json` documents,
  including gzip responses. Protocol upgrades and non-empty SSE, NDJSON, log,
  and other non-JSON API responses fail closed: opaque streaming would bypass
  source and legacy-log redaction, while unbounded buffering is unsafe. Static
  HTML/assets outside the API prefix remain pass-through. Restoring an Argo API
  stream requires a bounded, format-aware streaming sanitizer and canary tests.

## Backup and Restore Rules

- Postgres backups contain encrypted credential rows and token hashes.
- Redis/asynq state is not a durable secret store.
- Kubernetes bootstrap and encryption-key Secrets must be protected separately
  from database backups.
- Restore tests must include a decryption sanity check for encrypted columns
  without printing the decrypted values.

## Review Checklist

For any change that adds or touches secrets:

- [ ] Is plaintext persistence avoided?
- [ ] If a token is created, is the persisted value hash-only after handoff?
- [ ] If a reusable credential is stored, is it Fernet-encrypted?
- [ ] If an external secret manager can own the secret, is a reference path
      supported or documented?
- [ ] Is the column present in `docs/secret-column-inventory.md`?
- [ ] Are API read responses redacted?
- [ ] Are audit rows useful but value-free?
- [ ] Are logs and task errors value-free?
- [ ] Are support/diagnostics bundles redacted?
- [ ] Are RBAC and tests present for any secret read surface?
- [ ] Is rotation or revocation documented?

## Definition of Done

A secret-handling change is complete only when storage classification,
redaction, audit behavior, permissions, tests, and rotation/recovery behavior
are all explicitly handled.
