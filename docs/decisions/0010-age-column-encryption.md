# 0010 — age column encryption with a (key_id, algorithm, ciphertext, nonce) row shape

- Status: accepted
- Date: 2026-10-20
- Guide: Technology Selection Guide ch.06 (age via a thin `pkg/crypto` wrapper,
  key from file/env, key_id-selected reads, background rotation)

## Context

Secret column values were Fernet tokens wrapped in a string envelope
(`astronomer:v1:fernet:<key-id>:<token>`) in `<col>_encrypted TEXT` columns,
produced by `internal/auth.Encryptor` and called from ~100 files. Rotation was
a separate `cmd/keyrotate` binary (database/sql) with its own hand-maintained
target list. A plaintext-sweep worker task, raw-Fernet "legacy" reads, a
plaintext `dex_settings.public_clients` compatibility copy, blank-only
plaintext columns and per-field Fernet strings inside JSONB
(`dex_connectors.config`, `deferred_operations.operation_spec`) kept the
compatibility surface alive. The guide requires age through a wrapper package
that owns all encryption, and an explicit key_id/algorithm/ciphertext row
shape with key_id-selected reads and a resumable rotation job.

## Decision

- **Library.** `pkg/crypto` wraps `filippo.io/age` v1.3 (X25519 recipient +
  ChaCha20-Poly1305 payload). It is the only importer of age. API:
  `NewEncryptor(secretKey)`, `NewEncryptorFromFile(path)`,
  `NewEncryptorFromEnv(envVar)`, `ParseKeyring(spec)`, `Load(inline, path)`,
  `GenerateKey()`, `(*Encryptor).Encrypt([]byte) (Sealed, error)`,
  `Decrypt(Sealed) ([]byte, error)` (+ `EncryptString`/`DecryptString`),
  `CurrentKeyID`, `HasKey`, `KeyIDs`, `KeyInventory`. Errors are wrapped with
  `%w` around sentinels (`ErrNoKey`, `ErrUnknownKey`, `ErrDecrypt`,
  `ErrUnsupportedAlgorithm`, `ErrEmpty`). Callers treat ciphertext as `[]byte`.
- **Keyring.** One or more entries `[label:]AGE-SECRET-KEY-1...`, newline- or
  comma-separated, `#` comments allowed (age-keygen output works). The first
  entry is the current key (all writes); later entries are previous keys kept
  only for reads. The key id is the operator label, or `age-<hex>` — the first
  8 bytes of SHA-256 over the public recipient — when unlabelled. Source:
  `ASTRONOMER_ENCRYPTION_KEY_FILE` or `ASTRONOMER_ENCRYPTION_KEY` (setting both
  is a startup error); chart `secrets.encryptionKey` (same env var). A key that
  is configured but does not parse fails server/worker startup; no key at all
  is allowed only outside production.
- **Row shape: real columns, per secret.** Every encrypted value `<c>` is
  four columns: `<c>_key_id text NOT NULL DEFAULT ''`,
  `<c>_algorithm text NOT NULL DEFAULT ''`, `<c>_ciphertext bytea`,
  `<c>_nonce bytea`, plus a CHECK that key_id, algorithm and ciphertext are
  all set or all empty. A table with several secrets has several families
  (`delivery_sources.credential_*` and `.ca_bundle_*`). Real columns were
  chosen over a JSONB-embedded triple: the rotation predicate
  (`WHERE <c>_key_id = $old`) is a plain column comparison, constraints are
  enforceable, and sqlc types them (`string`, `string`, `[]byte`, `[]byte`).
  `Sealed.Parts()` / `crypto.FromParts(...)` move the four values between
  generated params/rows and the library.
- **Algorithm and nonce.** The only algorithm is
  `age-x25519-chacha20poly1305`. age carries its per-file nonce (and the
  ephemeral X25519 share) inside the ciphertext, so `<c>_nonce` is always
  NULL for it; Decrypt rejects an age value that carries a nonce. The column
  exists so an AEAD algorithm that needs an external nonce — a FIPS
  AES-256-GCM mode for `make fips-build` deployments, where age's primitives
  are not FIPS-approved — can be added as a new `algorithm` value without a
  schema change; rotation would then move rows between algorithms the same
  way it moves them between keys.
- **Reads select the key by key_id** (no trial decryption); an unknown key id
  is `ErrUnknownKey`. **Writes stamp key_id and algorithm** and every
  INSERT/UPDATE sets all four columns.
- **Registry + rotation.** `internal/db/sealedcols.Columns` is the one Go
  table of sealed families (29 at migration 00082). An integration test fails
  when `information_schema` has a `*_ciphertext` column that is not
  registered, or a registered family is missing a column. `sealedcols.Rotate`
  — exposed as `astro admin rotate-keys` (direct pgx connection) — first
  verifies that every key id still referenced by a row is loaded (otherwise
  `ErrMissingKey`, nothing written), then per family repeatedly selects
  `WHERE <c>_key_id = <old> LIMIT N FOR UPDATE`, decrypts, re-encrypts with the
  current key and updates key_id/algorithm/ciphertext/nonce with a
  compare-and-set on the old key id, one transaction per batch, until none
  remain. Resumable and idempotent. The previous key stays in the keyring until
  a run reports zero remaining rows. `cmd/keyrotate` is deleted.
- **Hard cutover (pre-production).** Migration 00082 drops every
  `*_encrypted` column; existing Fernet ciphertexts are discarded, not
  converted, and **development installs must re-enter their secrets** (SSO
  client secrets, SMTP password, Dex connector secrets, registry/repository/
  cloud/Vault/SIEM/webhook credentials, backup destinations). Rows that are
  useless without their sealed input are deleted (Charlie action receipts,
  node/resource/Dex operations, SSO sessions, TOTP enrollments + recovery
  codes, gate-deferred operations). Also removed: the Fernet code, `fernet-go`,
  the legacy envelope and raw-token reads, the
  `security:migrate_plaintext_credentials` task, `dex_settings.public_clients`
  (plaintext copy) and its cutover marker, the blank-only plaintext columns
  `backup_storage_configs.access_key/secret_key`,
  `cluster_registry_configs.registry_password`,
  `cluster_registration_tokens.token`, `cluster_agent_tokens.token`. Dex
  connector secrets move from Fernet strings inside `dex_connectors.config` to
  a `dex_connectors.secrets_*` family; the maintenance-gate request moves from
  a JSON envelope in `deferred_operations.operation_spec` to
  `deferred_operations.sealed_spec_*`.
- The restore drill proves the backed-up keyring with the age CLI
  (`age --decrypt -i`) against a sampled `<c>_ciphertext` instead of the
  embedded Python Fernet HMAC check; the DR image installs `age`.

## Consequences

- One place to audit encryption; rotation cannot silently skip a column.
- age ciphertexts are not bound to their row (no associated data): someone
  with write access to the database can swap two sealed values of the same
  key. Accepted: that attacker can already replace values outright; the
  threat age protects against here is disclosure from dumps and backups.
- Key ids are stored in rows, so a key must keep its label (or stay
  unlabelled) for as long as rows reference it.
- Follow-ups (not sealed yet, recorded in `docs/secret-column-inventory.md`):
  operator-authored config bags `logging_outputs.configuration`,
  `notification_channels.configuration`, `webhook_subscriptions.extra_headers`,
  and rendered emails in `email_messages.body_text/body_html`.
