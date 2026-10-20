# Secret rotation runbook

This runbook is the operator-facing procedure for rotating every
long-lived secret the Astronomer management plane depends on, with no
downtime where possible and with explicit re-pairing only where
unavoidable. It is the counterpart to
[`management-plane-dr-runbook.md`](./management-plane-dr-runbook.md):
that one covers "the DB is corrupt, restore it"; this one covers "the
keys are compromised or due for routine rotation, replace them".

The chart exposes four classes of secret. Each has a different blast
radius and a different rotation procedure, summarised here and detailed
below:

| Secret                                | Encryption surface                       | Online rotation? | Forces re-pairing? |
|---------------------------------------|------------------------------------------|------------------|--------------------|
| `secrets.encryptionKey` (age keyring) | At-rest sealed columns (`sealedcols.Columns`: SSO, SMTP, delivery-source, registry, backup, cloud, webhook, SIEM, Vault credentials, Charlie local trust, Dex clients) | **yes**, with `astro admin rotate-keys` | no |
| `secrets.secretKey` (JWT HMAC)        | Every issued access + refresh JWT        | **yes**, two-key window for one refresh-token lifetime (7d) | no |
| Durable agent tokens                  | Per-cluster adopted-agent identity       | **yes**, ACK-delivered rotation with grace | no |
| Admin bootstrap password              | First-login admin account                | n/a — change via the normal password-change UI | n/a |

What survives `pg_restore` from the nightly dump (cross-reference with
`management-plane-dr-runbook.md`):

- **encryptionKey ciphertexts**: the encrypted bytes are in the dump.
  The dump is useless without the age keys that sealed them. Keep
  the current key list in a key-management system separate from the
  dump artifacts — anything stored alongside the dump is equivalent to
  the dump being unencrypted.
- **JWT signing key**: not stored in the DB, only in the Helm secret.
  After a `pg_restore` you don't need to do anything special; the
  server reads whatever is in its Helm secret.
- **Agent credentials**: token hashes and registration state are restored with
  the database. The agent's durable plaintext exists only in its managed
  cluster's `astronomer-agent-identity` Secret and is **not** in the dump — see the
  agent rotation section below.

---

## 1. Rotating `secrets.encryptionKey` (age keyring)

`secrets.encryptionKey` (env `ASTRONOMER_ENCRYPTION_KEY`, or a file named by
`ASTRONOMER_ENCRYPTION_KEY_FILE`) is an **age keyring**: one or more entries,
newline- or comma-separated, each `AGE-SECRET-KEY-1...` or
`label:AGE-SECRET-KEY-1...`. The **first** entry is the current key: every
write seals with it and stamps its id into `<col>_key_id`. Later entries are
previous keys, used only to decrypt rows whose `<col>_key_id` names them.
Reads select the key by `key_id`; nothing is tried by trial decryption. See
[ADR-0010](decisions/0010-age-column-encryption.md).

The key id is the label when one is given, otherwise a fingerprint of the
public recipient (`age-<16 hex>`). Ids are stored in every row, so **never
relabel a key that rows reference** — relabelling makes those rows look like
they belong to an unknown key.

The set of sealed columns is the Go table `sealedcols.Columns`
(`internal/db/sealedcols`); an integration test fails if the schema has a
`*_ciphertext` column that is not registered, so rotation cannot skip one.

### Step 1 — generate a new key

```bash
astro admin generate-encryption-key --label k2026b > new-key
# or: age-keygen | grep AGE-SECRET-KEY | sed 's/^/k2026b:/' > new-key
```

### Step 2 — promote the new key, keep the old one as a previous key

```bash
# new key FIRST, old key second
printf '%s\n%s\n' "$(grep AGE-SECRET-KEY new-key)" "k2026a:AGE-SECRET-KEY-1...OLD" > keyring
helm upgrade astronomer ./deploy/chart --reuse-values --set-file secrets.encryptionKey=./keyring
kubectl -n astronomer rollout restart deploy/astronomer-server deploy/astronomer-worker
```

From now on every write uses `k2026b`; rows sealed under `k2026a` still decrypt.
`GET /api/v1/admin/key-status` reports `encryption_keys: 2` and the inventory
(`id`, `primary`) while the rotation is pending.

### Step 3 — `astro admin rotate-keys`

Run it with the **same keyring** the server now uses, against the same
database (it connects to PostgreSQL directly; the API need not be up):

```bash
export DATABASE_URL='postgres://…'            # sslmode=require in production
export ASTRONOMER_ENCRYPTION_KEY_FILE=./keyring # or ASTRONOMER_ENCRYPTION_KEY=…
astro admin rotate-keys --dry-run              # per-column counts still sealed under old keys
astro admin rotate-keys                        # rewrite them
astro admin rotate-keys                        # second run must report TOTAL 0
```

For every registered column it reads up to `--batch-size` rows
`WHERE <col>_key_id = <old>` `FOR UPDATE`, decrypts each, re-encrypts with the
current key and updates `key_id`/`algorithm`/`ciphertext`/`nonce` in the same
transaction (compare-and-set on the old key id), until no row references an old
key. It prints one line per column and a total. It is resumable (rerun after an
interruption) and idempotent. `--from-key-id k2026a` limits a run to one old
key. If any row references a key id that is not in the keyring, it refuses
before writing anything and names the missing ids.

### Step 4 — drop the old key

Only after `rotate-keys` reports `TOTAL 0` (and no retained backup you might
still restore needs the old key — see §5):

```bash
grep -v k2026a keyring > keyring.next
helm upgrade astronomer ./deploy/chart --reuse-values --set-file secrets.encryptionKey=./keyring.next
kubectl -n astronomer rollout restart deploy/astronomer-server deploy/astronomer-worker
```

Keep the old key in your secret manager until every database backup sealed
under it has aged out.

### Rollback

Before Step 4 nothing is lost: put the old key back first in the keyring and
run `rotate-keys` again; rows move back to it. After Step 4, a row sealed under
a dropped key is unreadable until that key is loaded again as a previous key.

---

## 2. Rotating `secrets.secretKey` (JWT HMAC)

The JWT secret signs every access + refresh token the server issues.
The longest-lived token type is the refresh token (7 days, see
[`internal/auth/jwt.go`](../internal/auth/jwt.go)), so the rotation
window must outlast that.

`JWTManager` is multi-key under the same scheme as the encryption key:
the primary signs new tokens; any listed key validates.

### Step 1 — generate a new key

```bash
openssl rand -base64 48     # any high-entropy string ≥32 bytes works
```

### Step 2 — promote new key, keep old as fallback

```bash
helm upgrade --reuse-values astronomer ./deploy/chart \
  --set-string secrets.secretKey="${NEW_JWT},${OLD_JWT}"
```

New tokens are now signed under `NEW_JWT`. Existing user sessions
continue to validate because `OLD_JWT` is still in the validator list.

### Step 3 — wait out the refresh-token lifetime

7 days by default (`refreshTokenLifetime` in `internal/auth/jwt.go`).
Measure the window from the last old-key issuer being stopped. Tokens signed by
that issuer remain valid until their own expiry unless separately revoked;
refreshing once is not proof that every issued token has expired. The refresh
lifetime is seven days in the current implementation; account for the configured
access lifetime and any other issued token purpose when selecting the window.

Browser refresh sessions have durable `refresh_session_families` and
`refresh_session_tokens` state. Refresh reuse revokes the family, and access-token
validation checks family revocation. Logout and administrative session revocation
use the supported authenticated operations and mandatory audit paths; do not
delete session rows manually. If a compromised signing key must be removed
before its tokens expire, coordinate explicit session revocation and operator
reauthentication. Removing a verification key rejects tokens still signed by it.

### Step 4 — drop the old key

```bash
helm upgrade --reuse-values astronomer ./deploy/chart \
  --set-string secrets.secretKey="${NEW_JWT}"
```

Any user still presenting an old-signed token at this point is logged
out and must reauthenticate. No further action needed.

---

## 3. Rotating an agent credential

Adopted agents use two separate Secrets in `astronomer-system`:

- `astronomer-agent-registration-token` is installer-owned, short-lived
  bootstrap material. Server-side manifest reapply may refresh it.
- `astronomer-agent-identity` is the active durable identity. The installer owns
  its empty labeled container; the agent owns only `data.token`, so every
  bootstrap reapply preserves it.
- `astronomer-agent-token` is legacy migration input and is ignored after an
  accepted migration into the active identity.

Do not patch either Secret with a token returned by an API or database query.
Request durable rotation through the control plane:

```bash
curl -fsSL -X POST \
  -H "Authorization: Bearer $ASTRONOMER_API_TOKEN" \
  "$ASTRONOMER_URL/api/v1/clusters/$CLUSTER_ID/agent-token/rotate/"
```

The next authenticated CONNECT delivers the replacement credential in its ACK.
The agent patches only `data.token` on `astronomer-agent-identity`, reconnects
with it, and the server retires the previous hash after adoption. Verify the
cluster reconnects and `agent_last_seen_at` advances. If durable persistence
fails, inspect the agent's `credential_source` diagnostic and its name-scoped
Secret RBAC; never print either Secret's data while troubleshooting.

Deleting the durable Secret is a recovery action, not normal rotation. On the
next restart a current-layout agent fails closed because the required identity
container is absent; it does not directly fall back. Reapply the current manifest
to recreate the empty labeled identity container. Startup then prefers any valid
legacy `astronomer-agent-token` material before bootstrap. If legacy is absent,
the server still enforces bootstrap registration-token expiry and cluster
binding. Generate a fresh manifest if bootstrap has expired, then apply it with:

```bash
kubectl apply --server-side --force-conflicts --field-manager=astronomer-bootstrap -f -
```

See [agent-credential-ownership.md](agent-credential-ownership.md) for ownership,
upgrade compatibility, and the exact-name RBAC contract.

---

## 4. Bootstrap password

The bootstrap password (`bootstrap.password` value, env
`ASTRONOMER_BOOTSTRAP_PASSWORD`) is the initial admin password on an empty
database. If `bootstrap.password` is empty, the chart generates a random value
and keeps it in the `<release>-bootstrap` Secret so operators can retrieve it:

```bash
kubectl -n astronomer get secret <release>-bootstrap \
  -o jsonpath='{.data.password}' | base64 -d
```

If `bootstrap.password` is set in values, that value is used as-is. The
bootstrap admin is not forced through a first-login reset.

To "rotate" it, just change the admin user's password via the normal
profile UI or `PATCH /api/v1/users/me/password`. The chart's bootstrap
value isn't consulted again.

If you forgot the admin password entirely and there's no other admin:

```bash
# Reset directly in the DB. password is bcrypt-hashed.
HASH=$(python3 -c "
import bcrypt, sys
print(bcrypt.hashpw(sys.argv[1].encode(), bcrypt.gensalt(rounds=10)).decode())
" 'new-admin-password')

psql "$DATABASE_URL" -c "
  UPDATE users SET password='$HASH', must_change_password=false
  WHERE username='admin';
"
```

---

## 5. What survives a `pg_restore` — and what doesn't

When you restore from the nightly dump per
[`management-plane-dr-runbook.md`](./management-plane-dr-runbook.md):

| In the dump          | Not in the dump (operator must supply) |
|----------------------|----------------------------------------|
| `users` (password hashes) | `secrets.encryptionKey` — the age keyring that decrypts the sealed columns the dump contains |
| `sso_configurations` (with `client_secret_encrypted` ciphertext) | `secrets.secretKey` — the JWT signing key |
| `delivery_sources` (with write-only credential ciphertext) | `bootstrap.password` (irrelevant once an admin exists) |
| `cluster_registration_tokens` | Agent-side Secret in each managed cluster (the agent reads the token from its own Secret, not from the dump) |
| `dex_connectors` (with encrypted JSONB fields) | TLS certificate Secret (if `tls.source=secret`) — operator owns the cert lifecycle |

**The two non-obvious gotchas** that the regular DR runbook glosses
over:

1. After restore, every agent in every managed cluster must
   re-authenticate. If you restored to a NEW management cluster
   (different DNS), the agents must also be reconfigured with the new
   server URL. Use the agent's `kubectl rollout restart` after
   patching the URL in its config map.
2. If you restored from a dump that predates your current
   `encryptionKey` rotation, you need the **OLD** key in your
   fallback list to decrypt the restored rows. Don't delete old keys
   from your secret manager until every dump that used them has
   passed its retention window.

---

## Appendix A — verifying rotation state in production

The `Encryptor` and `JWTManager` both expose `KeyCount()`. A small
admin endpoint surfaces the live count for ops:

```bash
curl -sH "Authorization: Bearer $TOKEN" $URL/api/v1/admin/key-status
# → {"encryption_keys":1,"jwt_keys":1,"as_of":"…"}
```

`> 1` means a rotation is mid-flight — fine for hours/days, alarming
for weeks. The recommended SLO is "no key rotation lingers more than
14 days"; alert on `encryption_keys > 1 AND last_rotation_started_at <
now() - 14d`.

## Appendix B — `astro admin rotate-keys` reference

```text
astro admin rotate-keys
  --database-url <DSN>     (env DATABASE_URL)
  --key-file <path>        keyring file, new key first (env ASTRONOMER_ENCRYPTION_KEY_FILE;
                           otherwise ASTRONOMER_ENCRYPTION_KEY is read)
  --from-key-id <id>       only rewrite rows sealed under this key (default: every non-current key)
  --batch-size 100         rows per transaction
  --dry-run                report per-column counts, write nothing
```

Exit status is non-zero when the keyring cannot be loaded, the database is
unreachable, a referenced key id is missing (nothing is written), or a row
fails to decrypt (the failing batch is rolled back; earlier batches stay
rotated and a rerun resumes).
