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
| `secrets.encryptionKey` (Fernet)      | At-rest column secrets (SSO client secret, ArgoCD auth token, backup creds, Dex connector secrets) | **yes**, with `keyrotate` | no |
| `secrets.secretKey` (JWT HMAC)        | Every issued access + refresh JWT        | **yes**, two-key window for one refresh-token lifetime (7d) | no |
| Durable agent tokens                  | Per-cluster adopted-agent identity       | **yes**, ACK-delivered rotation with grace | no |
| Admin bootstrap password              | First-login admin account                | n/a — change via the normal password-change UI | n/a |

What survives `pg_restore` from the nightly dump (cross-reference with
`management-plane-dr-runbook.md`):

- **encryptionKey ciphertexts**: the encrypted bytes are in the dump.
  The dump is useless without the Fernet key that signed them. Keep
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

## 1. Rotating `secrets.encryptionKey` (Fernet)

The encryption key wraps every column-stored secret: SSO client
secrets, ArgoCD repo auth tokens, S3 backup credentials, Dex connector
secrets (LDAP bind password, OIDC client secret, etc.).

The chart and binary support **multi-key** Fernet from
[`internal/auth/crypto.go`](../internal/auth/crypto.go): `encryptionKey`
accepts a comma-separated list. The first key is the primary (used to
encrypt); every listed key is tried in order on decrypt.

The safe procedure is:

### Step 1 — generate a new key

```bash
python -c "from cryptography.fernet import Fernet; print(Fernet.generate_key().decode())"
# or, if you have the keyrotate binary built:
go run ./cmd/keyrotate -h    # the README mentions GenerateKey internally
```

Store the new key in your secret manager. Don't lose the old one — you
need it through the entire procedure.

### Step 2 — promote new key, keep old as fallback

Update the Helm values and roll the server (and worker — it shares the
same key):

```bash
helm upgrade --reuse-values astronomer ./deploy/chart \
  --set-string secrets.encryptionKey="${NEW_KEY},${OLD_KEY}"
```

Verify the server picked up two keys:

```bash
kubectl -n astronomer logs deploy/astronomer-server | grep -m1 "encryption keys loaded"
# → should say "encryption keys loaded count=2"  (or check via /api/v1/admin)
```

At this point:

- new ciphertexts (writes from the live server) are signed with `NEW_KEY`
- old ciphertexts (already in the DB) still decrypt because `OLD_KEY`
  is in the fallback list

### Step 3 — run `keyrotate` to rewrite historical rows

```bash
# Dry run first — reports what would change without writing.
keyrotate \
  --database-url "$DATABASE_URL" \
  --encryption-key "${NEW_KEY},${OLD_KEY}" \
  --dry-run

# Then for real:
keyrotate \
  --database-url "$DATABASE_URL" \
  --encryption-key "${NEW_KEY},${OLD_KEY}"
```

The tool rewrites every row in every column-stored Fernet secret. As of this
writing that is:

- `sso_configurations.client_secret_encrypted`
- `argocd_instances.auth_token_encrypted`
- `backup_storage_configs.encrypted_credentials`
- `vault_connections.auth_encrypted`
- `gitops_registration_sources.auth_encrypted`
- `prometheus_datasources.auth_encrypted`
- `siem_forwarders.auth_encrypted`
- `cloud_credentials.data_encrypted`
- `smtp_settings.password_encrypted`
- `cluster_registry_configs.registry_password_encrypted`
- `webhook_subscriptions.secret_encrypted`
- `argocd_cluster_proxy_tokens.token_encrypted`
- `user_totp_enrollments.secret_encrypted` (TOTP/MFA secrets)
- `sso_sessions.upstream_id_token_encrypted`

The authoritative list is `rewriteTargets` in `cmd/keyrotate/main.go`; a build
test (`cmd/keyrotate/coverage_test.go`) fails if a migration adds an encrypted
column that is not swept, so this list cannot silently drift.

It also prints the count of `dex_connectors` rows: their encrypted
fields live inside a JSONB blob (per-connector-type schema), so the
tool does NOT auto-rewrite them. Re-save each Dex connector via the UI
or:

```bash
curl -sX PATCH -H "Authorization: Bearer $TOKEN" \
  $URL/api/v1/dex/connectors/$ID -d '{}'    # zero-diff PATCH forces re-encrypt
```

When the `keyrotate` summary shows `failed=0`, every column secret is
now under the new primary.

### Step 4 — drop the old key

```bash
helm upgrade --reuse-values astronomer ./deploy/chart \
  --set-string secrets.encryptionKey="${NEW_KEY}"
```

Verify:

```bash
kubectl -n astronomer logs deploy/astronomer-server | grep -m1 "encryption keys loaded"
# → "encryption keys loaded count=1"
```

If anything fails to decrypt after this, the row was missed in step 3.
Add `OLD_KEY` back to the fallback list and re-run `keyrotate`.

### Rollback

If something goes wrong at any step before step 4: just keep
`encryptionKey="${OLD_KEY}"` — the old key has been in the fallback
list the whole time, so reverting is a single Helm upgrade.

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
At the end of this window, every active session has refreshed at least
once and now holds a token signed under `NEW_JWT`.

If you cannot wait 7 days (active compromise, compliance pressure),
force-invalidate all sessions by deleting the row-level session state
— but there isn't any: JWTs are stateless. Bumping the secret early
just kicks every logged-in user back to the login page. Decide which
hurts more.

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
The agent patches only `data.token` on `astronomer-agent-identity`, reconnects with it, and the
server retires the previous hash after adoption. Verify the cluster reconnects
and `agent_last_seen_at` advances. If durable persistence fails, inspect the
agent's `credential_source` diagnostic and its name-scoped Secret RBAC; never
print either Secret's data while troubleshooting.

Deleting the durable Secret is a recovery action, not normal rotation. On the
next restart the agent falls back only to the bootstrap Secret; the server still
enforces registration-token expiry and cluster binding. Generate a fresh
manifest if that bootstrap has expired, then apply it with:

```bash
kubectl apply --server-side --field-manager=astronomer-bootstrap -f -
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
| `users` (password hashes) | `secrets.encryptionKey` — the Fernet key that decrypts the columns the dump contains |
| `sso_configurations` (with `client_secret_encrypted` ciphertext) | `secrets.secretKey` — the JWT signing key |
| `argocd_instances` (with `auth_token_encrypted` ciphertext) | `bootstrap.password` (irrelevant once an admin exists) |
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

## Appendix B — `keyrotate` command reference

```text
keyrotate
  --database-url <DSN>            (or env DATABASE_URL)
  --encryption-key "<new>,<old>"  (or env ENCRYPTION_KEY)
  --dry-run                       no writes, report what would change
  --batch-size 100                rows per UPDATE
```

Exit codes:
- 0 — all rows re-encrypted (or, with `--dry-run`, all rows would
  successfully re-encrypt)
- 1 — at least one row failed to decrypt with the configured key list
  (an old key is missing from `--encryption-key`)
- 2 — invalid flags or DB connection failure
